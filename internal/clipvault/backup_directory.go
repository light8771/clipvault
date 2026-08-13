package clipvault

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

func (bs *BackupService) doDirBackup(cfg Config, forceFull bool) error {
	bs.execMu.Lock()
	defer bs.execMu.Unlock()
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
	normalizedScope, err := normalizeExportRange(db.Scope, cfg.Search.TimeZone)
	if err != nil {
		return err
	}
	db.Scope = normalizedScope
	scope, autoFallback, err := resolveBackupScope(bs.store, "dir", db.Scope, db.IncrementalFullInterval, forceFull)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		bs.setDirStatus(makeBackupStatus(time.Now(), false, db.Scope, false, wm, db.IncrementalFullInterval, err.Error(), ""))
		return err
	}
	now := time.Now()
	ext := db.Format
	if ext != "json" && ext != "xlsx" && ext != "cvexport" {
		err := fmt.Errorf("不支持的备份格式: %s", ext)
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		bs.setDirStatus(makeBackupStatus(now, false, scope, autoFallback, wm, db.IncrementalFullInterval, err.Error(), ""))
		return err
	}
	fullPath, size, exportedMaxID, err := bs.writeBackupFileStream(db.Dir, now, ext, db.Password, scope)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		bs.setDirStatus(makeBackupStatus(now, false, scope, autoFallback, wm, db.IncrementalFullInterval, err.Error(), ""))
		return err
	}

	wm, wmErr := updateBackupWatermark(bs.store, "dir", scope, exportedMaxID)
	if wmErr != nil {
		bs.logger.Error("dir_backup_watermark_failed", wmErr, map[string]any{"mode": scope.Mode})
		bs.setDirStatus(makeBackupStatus(now, false, scope, autoFallback, wm, db.IncrementalFullInterval, wmErr.Error(), fullPath))
		return wmErr
	}
	bs.setDirStatus(makeBackupStatus(now, true, scope, autoFallback, wm, db.IncrementalFullInterval, "", fullPath))
	bs.logger.Info("dir_backup_ok", map[string]any{"file": fullPath, "size": size, "mode": scope.Mode, "autoFallback": autoFallback})
	return nil
}

func (bs *BackupService) writeBackupFileStream(dir string, now time.Time, format, password string, scope ExportScope) (string, int64, int64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, 0, fmt.Errorf("创建目录失败: %w", err)
	}
	file, err := os.CreateTemp(dir, fmt.Sprintf("clipvault-backup-%s-*.%s", now.Format("20060102-150405"), format))
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

func (bs *BackupService) exportBackupTo(dst io.Writer, format, password string, scope ExportScope) (int64, error) {
	switch format {
	case "json":
		maxID, err := bs.store.StreamExport(context.Background(), scope, dst)
		if err != nil {
			return 0, fmt.Errorf("导出数据失败: %w", err)
		}
		return maxID, nil
	case "cvexport":
		if password == "" {
			return 0, errors.New("cvexport 格式需要加密密码")
		}
		var maxID int64
		err := WriteCVExport(dst, password, func(plain io.Writer) error {
			var streamErr error
			maxID, streamErr = bs.store.StreamExport(context.Background(), scope, plain)
			return streamErr
		})
		if err != nil {
			return 0, fmt.Errorf("加密失败: %w", err)
		}
		return maxID, nil
	case "xlsx":
		maxID, err := bs.store.WriteXLSXScope(context.Background(), scope, dst)
		if err != nil {
			return 0, fmt.Errorf("Excel 导出失败: %w", err)
		}
		return maxID, nil
	default:
		return 0, fmt.Errorf("不支持的备份格式: %s", format)
	}
}

func writeBackupFile(dir string, now time.Time, ext string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}
	file, err := os.CreateTemp(dir, fmt.Sprintf("clipvault-backup-%s-*.%s", now.Format("20060102-150405"), ext))
	if err != nil {
		return "", fmt.Errorf("创建备份文件失败: %w", err)
	}
	path := file.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("写入文件失败: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("同步文件失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("关闭文件失败: %w", err)
	}
	success = true
	return path, nil
}
