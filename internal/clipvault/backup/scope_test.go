package backup

import (
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/transfer"
)

func TestResolveScope(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	lastFull := now.AddDate(0, 0, -3)

	// 有水位线、兜底未到期：增量，SinceID 取水位线
	scope, fallback, err := ResolveScope(transfer.Scope{Mode: "incremental"},
		Watermark{LastItemID: 42, LastFullAt: lastFull}, 7, now, false)
	if err != nil || fallback || scope.SinceID != 42 || scope.Mode != "incremental" {
		t.Fatalf("incremental = %+v fallback=%v err=%v", scope, fallback, err)
	}

	// 无水位线：首次转全量
	scope, fallback, err = ResolveScope(transfer.Scope{Mode: "incremental"}, Watermark{}, 7, now, false)
	if err != nil || !fallback || scope.Mode != "full" {
		t.Fatalf("missing watermark = %+v fallback=%v err=%v", scope, fallback, err)
	}

	// 距上次全量已满兜底天数：转全量
	scope, fallback, err = ResolveScope(transfer.Scope{Mode: "incremental"},
		Watermark{LastItemID: 42, LastFullAt: now.AddDate(0, 0, -7)}, 7, now, false)
	if err != nil || !fallback || scope.Mode != "full" {
		t.Fatalf("stale full backup = %+v fallback=%v err=%v", scope, fallback, err)
	}

	// 有水位线但从未全量过（LastFullAt 为零值）：必须全量兜底
	scope, fallback, err = ResolveScope(transfer.Scope{Mode: "incremental"},
		Watermark{LastItemID: 42}, 7, now, false)
	if err != nil || !fallback || scope.Mode != "full" {
		t.Fatalf("never-full watermark = %+v fallback=%v err=%v", scope, fallback, err)
	}

	// fullFallbackDays=0：永不自动兜底
	scope, fallback, err = ResolveScope(transfer.Scope{Mode: "incremental"},
		Watermark{LastItemID: 42, LastFullAt: now.AddDate(-1, 0, 0)}, 0, now, false)
	if err != nil || fallback || scope.Mode != "incremental" || scope.SinceID != 42 {
		t.Fatalf("disabled fallback = %+v fallback=%v err=%v", scope, fallback, err)
	}

	// forceFull：强制全量，且不算自动兜底
	scope, fallback, err = ResolveScope(transfer.Scope{Mode: "incremental"},
		Watermark{LastItemID: 42, LastFullAt: lastFull}, 7, now, true)
	if err != nil || fallback || scope.Mode != "full" {
		t.Fatalf("forceFull = %+v fallback=%v err=%v", scope, fallback, err)
	}

	// range：校验失败时报错，不发生兜底
	scope, fallback, err = ResolveScope(transfer.Scope{Mode: "range", From: "bad", To: "bad"}, Watermark{}, 7, now, false)
	if err == nil || fallback {
		t.Fatalf("invalid range = %+v fallback=%v err=%v", scope, fallback, err)
	}
}
