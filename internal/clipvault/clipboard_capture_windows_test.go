//go:build windows

package clipvault

import (
	"errors"
	"strings"
	"testing"
)

func TestCapturedUnicodeTextCountsContentNotAllocation(t *testing.T) {
	for _, test := range []struct {
		name      string
		text      string
		limit     int
		wantLimit bool
	}{
		{"short text in oversized allocation", "a", 10, false},
		{"surrogate pair at limit", "A😀", 2, false},
		{"over limit", "A😀B", 2, true},
		{"unlimited", strings.Repeat("x", 100), 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MaxTextChars = test.limit
			raw := append(encodeClipboardText(test.text), make([]byte, 64)...)
			got, err := validateCapturedUnicodeText(cfg, raw)
			if errors.Is(err, ErrTextLimitExceeded) != test.wantLimit {
				t.Fatalf("text limit result: %v", err)
			}
			if test.wantLimit {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := decodeCapturedUnicodeText(cfg, got)
			if err != nil || snapshot.Text != test.text {
				t.Fatalf("snapshot = %#v, err=%v", snapshot, err)
			}
		})
	}
}
