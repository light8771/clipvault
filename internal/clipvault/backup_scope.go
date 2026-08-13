package clipvault

import (
	"bytes"
	"context"
	"fmt"
	"time"
)

// exportBackupData 导出单份数据（用于目录备份），按 scope 过滤。
// 返回 (data, ext, exportedMaxID)：exportedMaxID 是该次导出快照包含的
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
	return buf.Bytes(), format, maxID, nil
}

// resolveBackupScope resolves the actual scope to run: for incremental mode it
// reads the watermark to fill SinceID; if no watermark exists or the incremental
// chain reached fullInterval, it falls back to full. forceFull (manual trigger)
// always runs full. Returns (scope, autoFallback) where autoFallback is true only
// for automatic degradation (not manual forceFull).
func resolveBackupScope(store *Store, kind string, configured ExportScope, fullInterval int, forceFull bool) (ExportScope, bool, error) {
	if configured.Mode == "" {
		configured.Mode = "full"
	}
	if forceFull {
		configured.Mode = "full"
		configured.From = ""
		configured.To = ""
		configured.SinceID = 0
		configured.MaxID = 0
		return configured, false, nil
	}
	if configured.Mode == "range" {

		if err := validateExportScope(configured); err != nil {
			return configured, false, fmt.Errorf("range 备份配置非法: %w", err)
		}
		return configured, false, nil
	}
	if configured.Mode != "incremental" {
		return configured, false, nil
	}
	wm, err := store.GetBackupWatermark(context.Background(), kind)
	if err != nil {
		return configured, false, fmt.Errorf("读取备份水印失败: %w", err)
	}
	if wm.LastItemID <= 0 {
		configured.Mode = "full"
		return configured, true, nil
	}
	if fullInterval > 0 && wm.IncrementalCount >= fullInterval {
		configured.Mode = "full"
		return configured, true, nil
	}
	configured.SinceID = wm.LastItemID
	return configured, false, nil
}

// updateBackupWatermark records the backup position from the id that was ACTUALLY
// exported (exportedMaxID), not from a fresh query — a later query would race
// with concurrent clipboard writes and could advance the watermark past rows
// that were never exported, silently losing them from every future incremental
// backup. exportedMaxID<=0 means "no data was exported" and the watermark is
// left untouched (the existing watermark is returned so the UI status does not
// flash a reset).
func updateBackupWatermark(store *Store, kind string, scope ExportScope, exportedMaxID int64) (BackupWatermark, error) {
	wm, err := store.GetBackupWatermark(context.Background(), kind)
	if err != nil {
		return BackupWatermark{}, fmt.Errorf("读取水印失败: %w", err)
	}

	if scope.Mode == "range" || exportedMaxID <= 0 {
		return wm, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	newWM := BackupWatermark{LastItemID: exportedMaxID, LastSentAt: now}
	if scope.Mode == "incremental" {
		newWM.IncrementalCount = wm.IncrementalCount + 1
		newWM.LastFullAt = wm.LastFullAt
	} else {
		newWM.IncrementalCount = 0
		newWM.LastFullAt = now
	}
	if err := store.SetBackupWatermark(context.Background(), kind, newWM); err != nil {
		return wm, fmt.Errorf("写回水印失败: %w", err)
	}
	return newWM, nil
}

// makeBackupStatus builds a BackupStatus with transparent incremental-chain info.
// wm is provided by the caller (updateBackupWatermark's return on success, or a
// fresh read on failure) to avoid a redundant settings lookup here.
func makeBackupStatus(now time.Time, ok bool, scope ExportScope, autoFallback bool, wm BackupWatermark, fullInterval int, errMsg, file string) BackupStatus {
	return BackupStatus{
		LastTime:         now,
		LastOK:           ok,
		LastError:        errMsg,
		BackupFile:       file,
		ActualMode:       scope.Mode,
		AutoFallback:     autoFallback,
		IncrementalCount: wm.IncrementalCount,
		FullInterval:     fullInterval,
		LastFullAt:       wm.LastFullAt,
	}
}
