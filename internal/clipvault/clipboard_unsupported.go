//go:build !windows

package clipvault

import "errors"

func ReadClipboardSnapshot(cfg Config) (*ClipboardSnapshot, error) {
	return nil, errors.New("clipboard capture supports Windows only")
}
