package clipvault

import (
	"errors"
	"fmt"

	"clipboard_ext/internal/clipvault/domain"
)

// ErrTextLimitExceeded means that a text snapshot exceeded maxTextChars.
// Capture rejects the whole value instead of persisting a truncated secret.
var ErrTextLimitExceeded = errors.New("clipboard text exceeds configured character limit")

type clipboardTextLimitError struct {
	CharLen int
	Limit   int
}

// clipboardTextPayloadLimitError is returned before copying an HGLOBAL when
// its advertised byte size exceeds the process safety budget. It matches the
// public text-limit sentinel so the recorder emits a size-limit event.
type clipboardTextPayloadLimitError struct {
	ByteLen int
	Limit   int
}

func (e *clipboardTextPayloadLimitError) Error() string {
	return fmt.Sprintf("%v: raw payload %d bytes > %d bytes", ErrTextLimitExceeded, e.ByteLen, e.Limit)
}

func (e *clipboardTextPayloadLimitError) Is(target error) bool {
	return target == ErrTextLimitExceeded
}

func (e *clipboardTextLimitError) Error() string {
	return fmt.Sprintf("%v: %d > %d", ErrTextLimitExceeded, e.CharLen, e.Limit)
}

func (e *clipboardTextLimitError) Is(target error) bool {
	return target == ErrTextLimitExceeded
}

func validateCapturedTextLimit(cfg Config, snapshot *ClipboardSnapshot) (*ClipboardSnapshot, error) {
	if snapshot == nil || snapshot.Kind != domain.KindText || cfg.MaxTextChars == 0 {
		return snapshot, nil
	}
	charLen := textRuneLen(snapshot.Text)
	snapshot.CharLen = charLen
	if charLen > cfg.MaxTextChars {
		return nil, &clipboardTextLimitError{CharLen: charLen, Limit: cfg.MaxTextChars}
	}
	return snapshot, nil
}

// inspectClipboardUTF16 finds the required NUL terminator and counts the runes
// utf16.Decode will produce, without allocating or scanning the payload twice.
func inspectClipboardUTF16(raw []uint16) (charLen, end int, terminated bool) {
	count := 0
	for index := 0; index < len(raw); index++ {
		value := raw[index]
		if value == 0 {
			return count, index, true
		}
		if value >= 0xD800 && value <= 0xDBFF && index+1 < len(raw) {
			next := raw[index+1]
			if next >= 0xDC00 && next <= 0xDFFF {
				index++
			}
		}
		count++
	}
	return count, len(raw), false
}
