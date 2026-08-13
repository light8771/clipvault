package backup

import (
	"testing"

	"clipboard_ext/internal/clipvault/transfer"
)

func TestResolveScope(t *testing.T) {
	scope, fallback, err := ResolveScope(transfer.Scope{Mode: "incremental"}, Watermark{LastItemID: 42}, 7, false)
	if err != nil || fallback || scope.SinceID != 42 || scope.Mode != "incremental" {
		t.Fatalf("incremental = %+v fallback=%v err=%v", scope, fallback, err)
	}
	scope, fallback, err = ResolveScope(transfer.Scope{Mode: "incremental"}, Watermark{}, 7, false)
	if err != nil || !fallback || scope.Mode != "full" {
		t.Fatalf("missing watermark = %+v fallback=%v err=%v", scope, fallback, err)
	}
	scope, fallback, err = ResolveScope(transfer.Scope{Mode: "incremental"}, Watermark{LastItemID: 42, IncrementalCount: 7}, 7, false)
	if err != nil || !fallback || scope.Mode != "full" {
		t.Fatalf("full interval = %+v fallback=%v err=%v", scope, fallback, err)
	}
	scope, fallback, err = ResolveScope(transfer.Scope{Mode: "range", From: "bad", To: "bad"}, Watermark{}, 7, false)
	if err == nil || fallback {
		t.Fatalf("invalid range = %+v fallback=%v err=%v", scope, fallback, err)
	}
}
