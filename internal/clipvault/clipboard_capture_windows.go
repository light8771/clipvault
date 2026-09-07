//go:build windows

package clipvault

import (
	"encoding/binary"
	"errors"
	"time"
)

var clipboardCaptureSchedule = [...]time.Duration{0, 20 * time.Millisecond, 80 * time.Millisecond, 200 * time.Millisecond, 600 * time.Millisecond}

func isRetryableClipboardReadError(err error) bool {
	var readErr *clipboardReadError
	return errors.As(err, &readErr) && (readErr.stage == clipboardReadStageOpen || errors.Is(err, errClipboardDataUnavailable))
}

func shouldRetryClipboardCapture(err error, completedAttempts int) bool {
	if completedAttempts >= len(clipboardCaptureSchedule) || !isRetryableClipboardReadError(err) {
		return false
	}
	// T+600ms is the one delayed-recovery attempt reserved for formats that
	// were advertised but had not produced data yet. OpenClipboard contention
	// receives only the four fast attempts at T+0/20/80/200ms.
	if completedAttempts == len(clipboardCaptureSchedule)-1 {
		return errors.Is(err, errClipboardDataUnavailable)
	}
	return true
}

// capturedClipboardBytes is an immutable copy of one HGLOBAL-backed clipboard
// representation. Replaying it after CloseClipboard keeps parsing and image
// validation out of the system-wide clipboard critical section.
type capturedClipboardBytes struct {
	raw []byte
	err error
}

func capturedByteReader(values map[uint32]capturedClipboardBytes) func(uint32) ([]byte, error) {
	return func(format uint32) ([]byte, error) {
		value, ok := values[format]
		if !ok {
			return nil, nil
		}
		return value.raw, value.err
	}
}

func captureByteFormats(ids []uint32, available func(uint32) bool, read func(uint32) ([]byte, error)) map[uint32]capturedClipboardBytes {
	values := make(map[uint32]capturedClipboardBytes, len(ids))
	for _, id := range ids {
		if id == 0 || !available(id) {
			continue
		}
		raw, err := read(id)
		values[id] = capturedClipboardBytes{raw: raw, err: err}
	}
	return values
}

// captureOpenedClipboardAccess performs only operations that require an open
// clipboard. File drops are cheap to resolve and can stop the acquisition
// early. Potentially expensive text decoding, HTML/RTF parsing, PNG validation,
// DIB conversion and PNG encoding are represented by closures over copied data
// and run only after CloseClipboard.
func captureOpenedClipboardAccess(cfg Config, formats ClipboardFormatSet) openedClipboardAccess {
	availableFormats := make(map[uint32]bool, len(formats.IDs))
	for _, id := range formats.IDs {
		availableFormats[id] = true
	}
	available := func(format uint32) bool { return availableFormats[format] }
	access := openedClipboardAccess{available: available}

	var fileDropPaths []string
	var fileDropTotal int
	var fileDropErr error
	if available(cfHDrop) {
		fileDropPaths, fileDropTotal, fileDropErr = captureFileDropClipboardPaths(cfg)
	}
	access.readFileDrop = func(Config) (*ClipboardSnapshot, error) {
		if fileDropErr != nil {
			return nil, fileDropErr
		}
		return newFileDropSnapshot(fileDropPaths, fileDropTotal)
	}
	if len(fileDropPaths) > 0 && fileDropErr == nil {
		return access
	}

	var unicodeRaw []byte
	var unicodeErr error
	if available(cfUnicodeText) {
		unicodeRaw, unicodeErr = captureUnicodeTextClipboard(cfg)
	}
	access.readUnicodeText = func(Config) (*ClipboardSnapshot, error) {
		if unicodeErr != nil {
			return nil, unicodeErr
		}
		return decodeCapturedUnicodeText(cfg, unicodeRaw)
	}
	if len(unicodeRaw) > 0 || isTerminalClipboardCandidateError(unicodeErr) {
		return access
	}

	textCandidates := clipboardTextFallbackCandidates(captureClipboardANSICodePage(available))
	textIDs := make([]uint32, 0, len(textCandidates))
	for _, candidate := range textCandidates {
		textIDs = append(textIDs, candidate.id)
	}
	textValues := captureByteFormats(textIDs, available, readClipboardTextBytes)
	access.readTextFallback = func() (*ClipboardSnapshot, bool, error) {
		return readClipboardTextFallbackWith(textCandidates, available, capturedByteReader(textValues))
	}

	if cfg.Image.Enabled {
		pngIDs := []uint32{clipboardPNGFormat(), clipboardMIMEPNGFormat()}
		imageIDs := append(append([]uint32{}, pngIDs...), cfDIBV5, cfDIB)
		maxBytes := configuredImageMaxBytes(cfg)
		imageValues := captureByteFormats(imageIDs, available, func(format uint32) ([]byte, error) {
			limit := maxClipboardDIBBytes
			for _, pngID := range pngIDs {
				if format == pngID {
					limit = maxBytes
					break
				}
			}
			return readClipboardBytesLimited(format, limit)
		})
		var bitmap *clipboardBitmapCapture
		var bitmapErr error
		if available(cfBitmap) {
			bitmap, bitmapErr = captureClipboardBitmap()
		}
		imageAccess := clipboardImageAccess{
			pngFormatIDs: pngIDs,
			available:    available,
			readBytes:    capturedByteReader(imageValues),
			readBitmap: func() ([]byte, error) {
				if bitmapErr != nil {
					return nil, bitmapErr
				}
				if bitmap == nil {
					return nil, nil
				}
				return bitmap.toPNG()
			},
		}
		access.readImage = func(Config) (*ClipboardSnapshot, bool, error) {
			return readClipboardImageWith(cfg, imageAccess)
		}
	}

	urlCandidates := clipboardURLFormats()
	urlIDs := make([]uint32, 0, len(urlCandidates))
	for _, candidate := range urlCandidates {
		urlIDs = append(urlIDs, candidate.formatID)
	}
	urlValues := captureByteFormats(urlIDs, available, readClipboardTextBytes)
	urlAccess := clipboardURLAccess{
		candidates: urlCandidates,
		available:  available,
		readBytes:  capturedByteReader(urlValues),
	}
	access.readURL = func() (*ClipboardSnapshot, bool, error) {
		return readClipboardURLWith(urlAccess)
	}
	return access
}

func captureUnicodeTextClipboard(cfg Config) ([]byte, error) {
	raw, err := readClipboardTextBytes(cfUnicodeText)
	if err != nil {
		return nil, err
	}
	return validateCapturedUnicodeText(cfg, raw)
}

// The HGLOBAL allocation may contain unused bytes after the NUL terminator.
// Allocation size is bounded by the reader's independent safety cap; only
// Unicode characters before the terminator count against the user policy.
func validateCapturedUnicodeText(cfg Config, raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw)%2 != 0 {
		return nil, &clipboardOperationError{
			stage: clipboardReadStageDecode, format: "CF_UNICODETEXT",
			err: errors.New("unicode clipboard payload has an invalid byte length"),
		}
	}
	charLen, terminated := inspectClipboardUTF16Bytes(raw)
	if !terminated {
		return nil, &clipboardOperationError{
			stage: clipboardReadStageDecode, format: "CF_UNICODETEXT",
			err: errors.New("unicode clipboard payload is not NUL-terminated"),
		}
	}
	if charLen == 0 {
		return nil, &clipboardOperationError{
			stage: clipboardReadStageDecode, format: "CF_UNICODETEXT",
			err: errors.New("unicode clipboard payload is empty"),
		}
	}
	if cfg.MaxTextChars > 0 && charLen > cfg.MaxTextChars {
		return nil, &clipboardOperationError{
			stage: clipboardReadStageDecode, format: "CF_UNICODETEXT",
			err: &clipboardTextLimitError{CharLen: charLen, Limit: cfg.MaxTextChars},
		}
	}
	return raw, nil
}

func inspectClipboardUTF16Bytes(raw []byte) (charLen int, terminated bool) {
	for offset := 0; offset+1 < len(raw); offset += 2 {
		unit := binary.LittleEndian.Uint16(raw[offset : offset+2])
		if unit == 0 {
			return charLen, true
		}
		if unit >= 0xd800 && unit <= 0xdbff && offset+3 < len(raw) {
			next := binary.LittleEndian.Uint16(raw[offset+2 : offset+4])
			if next >= 0xdc00 && next <= 0xdfff {
				offset += 2
			}
		}
		charLen++
	}
	return charLen, false
}

func decodeCapturedUnicodeText(cfg Config, raw []byte) (*ClipboardSnapshot, error) {
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[i*2 : i*2+2])
	}
	return newUnicodeTextClipboardSnapshot(cfg, units)
}

func isTerminalClipboardCandidateError(err error) bool {
	return errors.Is(err, ErrTextLimitExceeded) || errors.Is(err, ErrImageLimitExceeded)
}
