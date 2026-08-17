//go:build !windows

package clipvault

import "errors"

var ErrClipboardSuperseded = errors.New("clipboard event was superseded")

func ReadClipboardSnapshot(cfg Config) (*ClipboardSnapshot, error) {
	return nil, errors.New("clipboard capture supports Windows only")
}

func readClipboardEventForSequence(cfg Config, expectedSequence uint32) (*ClipboardSnapshot, []string, error) {
	return nil, nil, errors.New("clipboard capture supports Windows only")
}
