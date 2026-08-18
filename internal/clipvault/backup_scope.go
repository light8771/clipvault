package clipvault

import (
	"bytes"
	"context"
	"fmt"
	"time"

	backuppolicy "clipboard_ext/internal/clipvault/backup"
)

// exportBackupData 导出单份数据（用于目录备份），按 scope 过滤。
// 返回 (data, ext, exportedMaxID)：无论选择哪种内容格式，文件都是 .cvexport
// 加密容器，ext 恒为 "cvexport"；exportedMaxID 是该次导出快照包含的
// 最后一条记录的 id（无数据时为 0），供写水印使用，避免二次查询竞态。
func (bs *BackupService) exportBackupData(format, password string, scope ExportScope) ([]byte, string, int64, error) {
	if format != "json" && format != "xlsx" && format != "cvexport" {
		return nil, "", 0, fmt.Errorf("不支持的备份格式: %s", format)
	}
	var buf bytes.Buffer
	maxID, err := bs.exportBackupTo(&buf, format, password, scope)
	if err != nil {
		return nil, "", 0, err
	}
	return buf.Bytes(), "cvexport", maxID, nil
}

// resolveBackupScope 决定本次备份实际执行的范围：incremental 模式读取水位线
// 填 SinceID；无水位线（首次）或距上次全量已满 fullFallbackDays 时转全量兜底。
// forceFull（手动触发）总是执行全量。返回 (scope, autoFallback)，autoFallback
// 仅在自动转全量时为 true（手动 forceFull 不算）。
func resolveBackupScope(store *Store, kind string, configured ExportScope, fullFallbackDays int, forceFull bool) (ExportScope, bool, error) {
	if configured.Mode != "incremental" || forceFull {
		return backuppolicy.ResolveScope(configured, backuppolicy.Watermark{}, fullFallbackDays, time.Now(), forceFull)
	}
	wm, err := store.GetBackupWatermark(context.Background(), kind)
	if err != nil {
		return configured, false, fmt.Errorf("读取备份水印失败: %w", err)
	}
	lastFullAt, _ := time.Parse(time.RFC3339, wm.LastFullAt) // 零值表示从未全量
	return backuppolicy.ResolveScope(configured,
		backuppolicy.Watermark{LastItemID: wm.LastItemID, LastFullAt: lastFullAt},
		fullFallbackDays, time.Now(), false)
}

// updateBackupWatermark 用实际导出的最大记录 id（exportedMaxID）推进水位线，
// 而不是重新查询——重新查询会与并发剪贴板写入竞态，把从未导出的记录
// 跳过，导致后续所有增量备份永久丢失它们。exportedMaxID<=0 表示没有导出
// 任何数据，水位线保持不动（返回现有值，避免界面状态闪动）。
// range 模式不参与水位线（一次性导出，不影响增量进度）。
// 邮件分片备份逐片调用本函数（scope 视为 incremental）：中途失败时已发出的
// 片段不回滚，下次备份从失败点续发，已发片段不会重复投递。
func updateBackupWatermark(store *Store, kind string, scope ExportScope, exportedMaxID int64) (BackupWatermark, error) {
	wm, err := store.GetBackupWatermark(context.Background(), kind)
	if err != nil {
		return BackupWatermark{}, fmt.Errorf("读取水印失败: %w", err)
	}

	if scope.Mode == "range" || exportedMaxID <= 0 {
		return wm, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	newWM := BackupWatermark{LastItemID: exportedMaxID, LastSentAt: now, LastFullAt: wm.LastFullAt}
	if scope.Mode != "incremental" {
		newWM.LastFullAt = now
	}
	if err := store.SetBackupWatermark(context.Background(), kind, newWM); err != nil {
		return wm, fmt.Errorf("写回水印失败: %w", err)
	}
	return newWM, nil
}

// makeBackupStatus 构造一次备份执行的状态快照。
// wm 由调用方提供（成功时取 updateBackupWatermark 的返回值，失败时取一次
// 当前读数），避免这里再查一次 settings。
func makeBackupStatus(now time.Time, ok bool, scope ExportScope, autoFallback bool, wm BackupWatermark, errMsg, file, trigger string) BackupStatus {
	return BackupStatus{
		LastTime:     now,
		LastOK:       ok,
		LastError:    errMsg,
		BackupFile:   file,
		ActualMode:   scope.Mode,
		AutoFallback: autoFallback,
		LastFullAt:   wm.LastFullAt,
		Trigger:      trigger,
	}
}
