//go:build windows

package clipvault

import (
	"errors"
	"syscall"
	"unsafe"
)

// maxTextChars is a user content policy and may be zero (unlimited). This
// separate hard cap is a process safety boundary and is never disabled.
const maxClipboardTextPayloadBytes = 16 << 20

func readClipboardTextBytes(format uint32) ([]byte, error) {
	return readClipboardBytesWithLimit(format, maxClipboardTextPayloadBytes,
		&clipboardTextPayloadLimitError{Limit: maxClipboardTextPayloadBytes})
}

// readClipboardBytesLimited checks GlobalSize before locking or copying an
// image payload. Image callers provide their validated format-specific cap.
func readClipboardBytesLimited(format uint32, maxBytes int) ([]byte, error) {
	return readClipboardBytesWithLimit(format, maxBytes, ErrImageLimitExceeded)
}

func readClipboardBytesWithLimit(format uint32, maxBytes int, limitErr error) ([]byte, error) {
	handle, _, err := procGetClipboardData.Call(uintptr(format))
	if handle == 0 {
		if err != syscall.Errno(0) {
			return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: clipboardFormatName(format), err: err}
		}
		return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: clipboardFormatName(format), err: errClipboardDataUnavailable}
	}
	size, _, _ := procGlobalSize.Call(handle)
	if size == 0 {
		return nil, nil
	}
	if err := checkClipboardPayloadSize(size, maxBytes, limitErr); err != nil {
		return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: clipboardFormatName(format), err: err}
	}
	ptr, _, err := procGlobalLock.Call(handle)
	if ptr == 0 {
		if err != syscall.Errno(0) {
			return nil, &clipboardOperationError{stage: clipboardReadStageLockData, format: clipboardFormatName(format), err: err}
		}
		return nil, &clipboardOperationError{stage: clipboardReadStageLockData, format: clipboardFormatName(format), err: errors.New("GlobalLock failed")}
	}
	defer procGlobalUnlock.Call(handle)
	raw := unsafe.Slice((*byte)(pointerFromWin32(ptr)), int(size))
	return append([]byte(nil), raw...), nil
}

func checkClipboardPayloadSize(size uintptr, maxBytes int, limitErr error) error {
	if maxBytes <= 0 {
		return errors.New("clipboard byte limit is not configured")
	}
	if size <= uintptr(maxBytes) {
		return nil
	}
	if payloadLimit, ok := limitErr.(*clipboardTextPayloadLimitError); ok {
		return &clipboardTextPayloadLimitError{ByteLen: int(size), Limit: payloadLimit.Limit}
	}
	return limitErr
}
