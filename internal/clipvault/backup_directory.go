package clipvault

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// doDirBackup 执行一次目录备份。调用方必须已持有 execMu
// （runDirBackupLocked / RunDirBackup）。trigger 记录触发方式，
// 用于状态展示与历史记录。
func (bs *BackupService) doDirBackup(cfg Config, trigger string, forceFull bool) error {
	if bs.isStopped() {
		return errBackupStopped
	}

	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	db := cfg.Backup.DirBackup
	if msg := validateDirBackupEnabled(db); msg != "" {
		return fmt.Errorf("目录备份配置不完整: %s", msg)
	}
	bs.markDirRunning(trigger)
	fail := func(now time.Time, scope ExportScope, autoFallback bool, file string, err error) error {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		bs.setDirStatus(makeBackupStatus(now, false, scope, autoFallback, wm, err.Error(), file, trigger))
		bs.recordBackupHistory("dir", trigger, scope.Mode, false, err, db.Dir, db.Format, 0, 0)
		return err
	}
	normalizedScope, err := normalizeExportRange(db.Scope, cfg.Search.TimeZone)
	if err != nil {
		return fail(time.Now(), db.Scope, false, "", err)
	}
	db.Scope = normalizedScope
	scope, autoFallback, err := resolveBackupScope(bs.store, "dir", db.Scope, db.FullFallbackDays, forceFull)
	if err != nil {
		return fail(time.Now(), db.Scope, false, "", err)
	}
	now := time.Now()
	format := db.Format
	if format != "json" && format != "xlsx" && format != "cvexport" {
		return fail(now, scope, autoFallback, "", fmt.Errorf("不支持的备份格式: %s", format))
	}
	// 无数据可备份时跳过：增量没有新记录、或全量时库为空，
	// 都不产生空备份文件，水位线保持不动。
	total, err := bs.store.CountInScope(context.Background(), scope)
	if err != nil {
		return fail(now, scope, autoFallback, "", fmt.Errorf("统计备份数据失败: %w", err))
	}
	if total == 0 {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		status := makeBackupStatus(now, true, scope, autoFallback, wm, "", "", trigger)
		status.Skipped = true
		bs.setDirStatus(status)
		bs.recordBackupHistory("dir", trigger, "skipped", true, nil, db.Dir, format, 0, 0)
		bs.logger.Info("dir_backup_skipped_empty", map[string]any{"mode": scope.Mode, "trigger": trigger})
		return nil
	}
	fullPath, size, exportedMaxID, err := bs.writeBackupFileStream(db.Dir, now, format, db.Password, scope)
	if err != nil {
		return fail(now, scope, autoFallback, "", err)
	}

	wm, wmErr := updateBackupWatermark(bs.store, "dir", scope, exportedMaxID)
	if wmErr != nil {
		bs.logger.Error("dir_backup_watermark_failed", wmErr, map[string]any{"mode": scope.Mode})
		return fail(now, scope, autoFallback, fullPath, fmt.Errorf("备份文件已生成，但更新备份进度失败: %w", wmErr))
	}
	bs.setDirStatus(makeBackupStatus(now, true, scope, autoFallback, wm, "", fullPath, trigger))
	bs.recordBackupHistory("dir", trigger, scope.Mode, true, nil, db.Dir, format, 1, total)
	bs.logger.Info("dir_backup_ok", map[string]any{"file": fullPath, "size": size, "mode": scope.Mode, "autoFallback": autoFallback, "trigger": trigger})
	return nil
}

// writeBackupFileStream 把一份备份写入目标目录。无论选择哪种内容格式，
// 文件统一使用 .cvexport 加密容器扩展名（json/xlsx 会先打进 zip 再整体加密）。
func (bs *BackupService) writeBackupFileStream(dir string, now time.Time, format, password string, scope ExportScope) (string, int64, int64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, 0, fmt.Errorf("创建目录失败: %w", err)
	}
	file, err := os.CreateTemp(dir, fmt.Sprintf("clipvault-backup-%s-*.cvexport", now.Format("20060102-150405")))
	if err != nil {
		return "", 0, 0, fmt.Errorf("创建备份文件失败: %w", err)
	}
	path := file.Name()
	success := false
	defer func() {
		if !success {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	maxID, err := bs.exportBackupTo(file, format, password, scope)
	if err != nil {
		return "", 0, 0, err
	}
	if err := file.Sync(); err != nil {
		return "", 0, 0, fmt.Errorf("同步文件失败: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return "", 0, 0, fmt.Errorf("读取备份文件信息失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", 0, 0, fmt.Errorf("关闭文件失败: %w", err)
	}
	success = true
	return path, info.Size(), maxID, nil
}

// exportBackupTo 把一份备份写入 dst，永远使用 .cvexport 加密容器：
//   - format=cvexport：JSON 导出直接作为加密载荷；
//   - format=json/xlsx：明文导出先打进 zip（clipvault-export.json/.xlsx），
//     zip 字节再作为加密载荷——备份文件绝不出现明文数据。
//
// 目录备份与邮件备份共用此实现，加密密码规则一致。
// 返回最后一条记录的 id（无数据时为 0），供写水印使用。
func (bs *BackupService) exportBackupTo(dst io.Writer, format, password string, scope ExportScope) (int64, error) {
	if password == "" {
		return 0, errors.New("备份必须设置加密密码（仅支持加密导出）")
	}
	var maxID int64
	err := WriteCVExport(dst, password, func(plain io.Writer) error {
		switch format {
		case "cvexport":
			var streamErr error
			maxID, streamErr = bs.store.StreamExport(context.Background(), scope, plain)
			return streamErr
		case "json", "xlsx":
			zw := zip.NewWriter(plain)
			name := "clipvault-export.json"
			if format == "xlsx" {
				name = "clipvault-export.xlsx"
			}
			entry, err := zw.Create(name)
			if err != nil {
				_ = zw.Close()
				return fmt.Errorf("创建 zip 条目失败: %w", err)
			}
			var streamErr error
			if format == "xlsx" {
				maxID, streamErr = bs.store.WriteXLSXScope(context.Background(), scope, entry)
			} else {
				maxID, streamErr = bs.store.StreamExport(context.Background(), scope, entry)
			}
			if streamErr != nil {
				_ = zw.Close()
				return streamErr
			}
			if err := zw.Close(); err != nil {
				return fmt.Errorf("写入 zip 失败: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("不支持的备份格式: %s", format)
		}
	})
	if err != nil {
		return 0, fmt.Errorf("加密失败: %w", err)
	}
	return maxID, nil
}
