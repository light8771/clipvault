//go:build windows

package clipvault

import (
	"encoding/binary"
	"errors"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"clipboard_ext/internal/clipvault/domain"
)

const (
	cfText                    = 1
	cfLocale                  = 16
	clipboardHTMLFormatName   = "HTML Format"
	clipboardRTFFormatName    = "Rich Text Format"
	localeDefaultANSICodePage = 0x00001004
)

var (
	clipboardTextFormatsOnce sync.Once
	clipboardHTMLFormatID    uint32
	clipboardRTFFormatID     uint32
	procGetLocaleInfoW       = kernel32.NewProc("GetLocaleInfoW")
)

type clipboardTextCandidate struct {
	id     uint32
	name   string
	decode func([]byte) (string, error)
}

func clipboardTextFallbackCandidates(ansiCodePage uint32) []clipboardTextCandidate {
	clipboardTextFormatsOnce.Do(func() {
		clipboardHTMLFormatID = registerClipboardFormat(clipboardHTMLFormatName)
		clipboardRTFFormatID = registerClipboardFormat(clipboardRTFFormatName)
	})
	return []clipboardTextCandidate{
		{id: clipboardHTMLFormatID, name: clipboardHTMLFormatName, decode: extractCFHTMLText},
		{id: clipboardRTFFormatID, name: clipboardRTFFormatName, decode: extractRTFText},
		{id: cfText, name: "CF_TEXT", decode: func(raw []byte) (string, error) {
			return decodeANSIClipboardStringWithCodePage(raw, ansiCodePage)
		}},
	}
}

// captureClipboardANSICodePage follows the CF_TEXT/CF_LOCALE contract. Missing
// or malformed locale metadata degrades to CP_ACP without hiding readable text.
func captureClipboardANSICodePage(available func(uint32) bool) uint32 {
	if !available(cfText) || !available(cfLocale) {
		return cpACP
	}
	raw, err := readClipboardBytesWithLimit(cfLocale, 16, errors.New("CF_LOCALE payload is too large"))
	if err != nil || len(raw) < 4 {
		return cpACP
	}
	lcid := binary.LittleEndian.Uint32(raw[:4])
	buf := make([]uint16, 16)
	written, _, _ := procGetLocaleInfoW.Call(
		uintptr(lcid), localeDefaultANSICodePage,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
	)
	if written == 0 {
		return cpACP
	}
	value := syscall.UTF16ToString(buf[:written])
	codePage, err := strconv.ParseUint(value, 10, 32)
	if err != nil || codePage == 0 {
		return cpACP
	}
	return uint32(codePage)
}

func readClipboardTextFallbackWith(candidates []clipboardTextCandidate, available func(uint32) bool, readBytes func(uint32) ([]byte, error)) (*ClipboardSnapshot, bool, error) {
	var failures clipboardCandidateErrors
	found := false
	for _, candidate := range candidates {
		if candidate.id == 0 || !available(candidate.id) {
			continue
		}
		found = true
		raw, err := readBytes(candidate.id)
		if err != nil {
			failures.add(err)
			continue
		}
		text, err := candidate.decode(raw)
		if err != nil {
			failures.add(&clipboardOperationError{stage: clipboardReadStageDecode, format: candidate.name, err: err})
			continue
		}
		if text == "" {
			failures.add(&clipboardOperationError{stage: clipboardReadStageDecode, format: candidate.name, err: errors.New("clipboard text payload is empty")})
			continue
		}
		return &ClipboardSnapshot{
			Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: text,
			ByteLen: len([]byte(text)), CharLen: textRuneLen(text), SHA256: hashString(text),
			Meta: map[string]any{
				"originalFormat":  candidate.name,
				"captureFallback": true,
				"captureDiagnostics": map[string]any{
					"primaryFormat": "CF_UNICODETEXT", "usedFormat": candidate.name,
				},
			},
		}, true, nil
	}
	return nil, found, failures.err()
}
