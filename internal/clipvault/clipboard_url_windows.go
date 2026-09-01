//go:build windows

package clipvault

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"clipboard_ext/internal/clipvault/domain"
)

const (
	clipboardURLFormatUnicodeName = "UniformResourceLocatorW"
	clipboardURLFormatANSIName    = "UniformResourceLocator"
	cpACP                         = 0
)

var (
	clipboardURLFormatsOnce sync.Once
	clipboardURLUnicodeID   uint32
	clipboardURLANSIID      uint32
	procMultiByteToWideChar = kernel32.NewProc("MultiByteToWideChar")
)

type clipboardURLCandidate struct {
	formatID   uint32
	formatName string
	decode     func([]byte) (string, error)
}

type clipboardURLAccess struct {
	candidates []clipboardURLCandidate
	available  func(uint32) bool
	readBytes  func(uint32) ([]byte, error)
}

func clipboardURLFormats() []clipboardURLCandidate {
	clipboardURLFormatsOnce.Do(func() {
		clipboardURLUnicodeID = registerClipboardFormat(clipboardURLFormatUnicodeName)
		clipboardURLANSIID = registerClipboardFormat(clipboardURLFormatANSIName)
	})
	return []clipboardURLCandidate{
		{formatID: clipboardURLUnicodeID, formatName: clipboardURLFormatUnicodeName, decode: decodeUTF16ClipboardString},
		{formatID: clipboardURLANSIID, formatName: clipboardURLFormatANSIName, decode: decodeANSIClipboardString},
	}
}

// readClipboardURL reads the Shell CFSTR_INETURLW/A representations used by
// browsers, chat clients, and OLE data objects. The stored item remains normal
// Unicode text so it can be searched and restored by the existing text path;
// sourceFormat preserves the actual clipboard representation.
func readClipboardURL() (*ClipboardSnapshot, bool, error) {
	return readClipboardURLWith(clipboardURLAccess{
		candidates: clipboardURLFormats(),
		available:  isClipboardFormatAvailable,
		readBytes:  readClipboardBytes,
	})
}

func readClipboardURLWith(access clipboardURLAccess) (*ClipboardSnapshot, bool, error) {
	var lastErr error
	found := false
	for _, candidate := range access.candidates {
		if candidate.formatID == 0 || !access.available(candidate.formatID) {
			continue
		}
		found = true
		raw, err := access.readBytes(candidate.formatID)
		if err != nil {
			lastErr = err
			continue
		}
		text, err := candidate.decode(raw)
		if err != nil {
			lastErr = &clipboardOperationError{stage: clipboardReadStageDecode, format: candidate.formatName, err: err}
			continue
		}
		if text == "" {
			lastErr = &clipboardOperationError{stage: clipboardReadStageDecode, format: candidate.formatName, err: errors.New("clipboard URL payload is empty")}
			continue
		}
		return newURLClipboardSnapshot(text, candidate.formatName), true, nil
	}
	if lastErr != nil {
		return nil, found, lastErr
	}
	return nil, found, nil
}

func newURLClipboardSnapshot(text, sourceFormat string) *ClipboardSnapshot {
	return &ClipboardSnapshot{
		Kind:    domain.KindText,
		Format:  domain.FormatUnicodeText,
		Text:    text,
		ByteLen: len([]byte(text)),
		CharLen: textRuneLen(text),
		SHA256:  hashString(text),
		Meta: map[string]any{
			"sourceFormat": sourceFormat,
		},
	}
}

func decodeUTF16ClipboardString(raw []byte) (string, error) {
	if len(raw)%2 != 0 {
		return "", errors.New("UTF-16 clipboard payload has odd byte length")
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		unit := binary.LittleEndian.Uint16(raw[i : i+2])
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return strings.TrimPrefix(string(utf16.Decode(units)), "\ufeff"), nil
}

func decodeANSIClipboardString(raw []byte) (string, error) {
	if nul := bytes.IndexByte(raw, 0); nul >= 0 {
		raw = raw[:nul]
	}
	if len(raw) == 0 {
		return "", nil
	}
	count, _, callErr := procMultiByteToWideChar.Call(
		cpACP,
		0,
		uintptr(unsafe.Pointer(&raw[0])),
		uintptr(len(raw)),
		0,
		0,
	)
	if count == 0 {
		if callErr != syscall.Errno(0) {
			return "", callErr
		}
		return "", errors.New("MultiByteToWideChar size query failed")
	}
	units := make([]uint16, int(count))
	written, _, callErr := procMultiByteToWideChar.Call(
		cpACP,
		0,
		uintptr(unsafe.Pointer(&raw[0])),
		uintptr(len(raw)),
		uintptr(unsafe.Pointer(&units[0])),
		count,
	)
	if written == 0 {
		if callErr != syscall.Errno(0) {
			return "", callErr
		}
		return "", errors.New("MultiByteToWideChar conversion failed")
	}
	return string(utf16.Decode(units[:written])), nil
}
