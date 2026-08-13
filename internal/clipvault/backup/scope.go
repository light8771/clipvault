package backup

import (
	"fmt"

	"clipboard_ext/internal/clipvault/transfer"
)

type Watermark struct {
	LastItemID       int64
	IncrementalCount int
}

// ResolveScope is a pure incremental-backup policy. Persistence stays behind
// the caller-owned watermark repository.
func ResolveScope(configured transfer.Scope, watermark Watermark, fullInterval int, forceFull bool) (transfer.Scope, bool, error) {
	if configured.Mode == "" {
		configured.Mode = "full"
	}
	if forceFull {
		return transfer.Scope{Mode: "full"}, false, nil
	}
	if configured.Mode == "range" {
		if err := transfer.ValidateScope(configured); err != nil {
			return configured, false, fmt.Errorf("range 备份配置非法: %w", err)
		}
		return configured, false, nil
	}
	if configured.Mode != "incremental" {
		return configured, false, nil
	}
	if watermark.LastItemID <= 0 || (fullInterval > 0 && watermark.IncrementalCount >= fullInterval) {
		configured.Mode = "full"
		configured.SinceID = 0
		return configured, true, nil
	}
	configured.SinceID = watermark.LastItemID
	return configured, false, nil
}
