//go:build !windows

package clipvault

import "errors"

var ErrClipboardSuperseded = errors.New("clipboard event was superseded")

func readClipboardEventForSequence(cfg Config, expectedSequence uint32) (*ClipboardSnapshot, ClipboardFormatSet, error) {
	return nil, ClipboardFormatSet{}, errors.New("clipboard capture supports Windows only")
}
