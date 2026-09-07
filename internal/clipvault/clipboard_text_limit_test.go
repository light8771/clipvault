package clipvault

import (
	"errors"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestInspectClipboardUTF16(t *testing.T) {
	raw := []uint16{'A', 0xD83D, 0xDE00, 0xD800, 'B', 0, 'C'}
	charLen, end, terminated := inspectClipboardUTF16(raw)
	if charLen != 4 || end != 5 || !terminated {
		t.Fatalf("inspection = charLen=%d end=%d terminated=%v", charLen, end, terminated)
	}
	charLen, end, terminated = inspectClipboardUTF16([]uint16{'A'})
	if charLen != 1 || end != 1 || terminated {
		t.Fatalf("unterminated inspection = charLen=%d end=%d terminated=%v", charLen, end, terminated)
	}
}

func TestValidateCapturedTextLimitRejectsOnlyText(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxTextChars = 2

	textSnapshot := &ClipboardSnapshot{Kind: domain.KindText, Text: "A😀B", CharLen: 999}
	got, err := validateCapturedTextLimit(cfg, textSnapshot)
	if got != nil || !errors.Is(err, ErrTextLimitExceeded) {
		t.Fatalf("over-limit text = %#v, err=%v", got, err)
	}
	var limitErr *clipboardTextLimitError
	if !errors.As(err, &limitErr) || limitErr.CharLen != 3 || limitErr.Limit != 2 {
		t.Fatalf("limit detail = %#v", limitErr)
	}

	fileSnapshot := &ClipboardSnapshot{Kind: domain.KindFiles, Text: "C:\\a\nC:\\b", CharLen: 999}
	got, err = validateCapturedTextLimit(cfg, fileSnapshot)
	if err != nil || got != fileSnapshot {
		t.Fatalf("file paths were treated as text: snapshot=%#v err=%v", got, err)
	}

	cfg.MaxTextChars = 0
	got, err = validateCapturedTextLimit(cfg, textSnapshot)
	if err != nil || got != textSnapshot {
		t.Fatalf("unlimited text was rejected: snapshot=%#v err=%v", got, err)
	}
}
