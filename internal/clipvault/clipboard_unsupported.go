//go:build !windows

package clipvault

import (
	"context"
	"errors"
)

var ErrClipboardSuperseded = errors.New("clipboard event was superseded")

func readClipboardEventForSequence(_ context.Context, cfg Config, expectedSequence uint32) (*ClipboardSnapshot, ClipboardFormatSet, int, error) {
	return nil, ClipboardFormatSet{}, 1, errors.New("clipboard capture supports Windows only")
}

func clipboardSequenceIsCurrent(_ uint32) bool { return true }
