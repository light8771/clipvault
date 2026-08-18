package backup

import (
	"time"

	"clipboard_ext/internal/clipvault/transfer"
)

// Watermark 是备份通道的最小进度视图：已备份到的记录 id 与上次全量时间。
// 全量兜底按时间判定（距 LastFullAt 超过配置天数），不再需要连续增量计数，
// 因此手动备份不会干扰定时兜底节奏。
type Watermark struct {
	LastItemID int64
	LastFullAt time.Time
}

// ResolveScope 决定本次备份实际执行的范围。
//
// 判定规则（仅 incremental 配置会改变）：
//   - forceFull：强制全量（手动"立即全量"），不算自动兜底；
//   - 无水印（LastItemID <= 0，首次备份）：转全量，记 autoFallback；
//   - fullFallbackDays > 0 且距上次全量已满该天数（或从未全量过）：
//     转全量兜底，记 autoFallback，防止增量永远补不回已删除记录；
//   - 否则按水位线增量导出。
//
// full 与 range 配置原样通过（range 由 transfer.ValidateScope 校验）。
// 返回的 bool 表示是否发生了"自动转全量"，用于状态展示。
func ResolveScope(configured transfer.Scope, watermark Watermark, fullFallbackDays int, now time.Time, forceFull bool) (transfer.Scope, bool, error) {
	if forceFull {
		return transfer.Scope{Mode: "full"}, false, nil
	}
	switch configured.Mode {
	case "", "full", "range":
		if err := transfer.ValidateScope(configured); err != nil {
			return transfer.Scope{}, false, err
		}
		return configured, false, nil
	case "incremental":
		if watermark.LastItemID <= 0 {
			return transfer.Scope{Mode: "full"}, true, nil
		}
		if fullFallbackDays > 0 &&
			(watermark.LastFullAt.IsZero() || !now.Before(watermark.LastFullAt.AddDate(0, 0, fullFallbackDays))) {
			return transfer.Scope{Mode: "full"}, true, nil
		}
		return transfer.Scope{Mode: "incremental", SinceID: watermark.LastItemID}, false, nil
	default:
		if err := transfer.ValidateScope(configured); err != nil {
			return transfer.Scope{}, false, err
		}
		return configured, false, nil
	}
}
