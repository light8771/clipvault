//go:build windows

package clipvault

import (
	"encoding/binary"
	"errors"
	"testing"
	"unicode/utf16"

	"clipboard_ext/internal/clipvault/domain"
)

func TestReadClipboardURLFallsBackFromUnicodeToANSI(t *testing.T) {
	const (
		unicodeID = 0xC101
		ansiID    = 0xC102
	)
	access := clipboardURLAccess{
		candidates: []clipboardURLCandidate{
			{formatID: unicodeID, formatName: clipboardURLFormatUnicodeName, decode: decodeUTF16ClipboardString},
			{formatID: ansiID, formatName: clipboardURLFormatANSIName, decode: decodeANSIClipboardString},
		},
		available: func(uint32) bool { return true },
		readBytes: func(format uint32) ([]byte, error) {
			if format == unicodeID {
				return nil, errClipboardDataUnavailable
			}
			return append([]byte("https://example.com/ansi"), 0), nil
		},
	}

	snapshot, ok, err := readClipboardURLWith(access)
	if err != nil || !ok {
		t.Fatalf("ANSI URL fallback failed: ok=%v err=%v", ok, err)
	}
	if snapshot.Kind != domain.KindText || snapshot.Format != domain.FormatUnicodeText {
		t.Fatalf("URL was not normalized as searchable text: %#v", snapshot)
	}
	if snapshot.Text != "https://example.com/ansi" ||
		snapshot.Meta["sourceFormat"] != clipboardURLFormatANSIName {
		t.Fatalf("unexpected ANSI URL snapshot: %#v", snapshot)
	}
}

func TestReadClipboardURLPrefersUnicode(t *testing.T) {
	const (
		unicodeID = 0xC101
		ansiID    = 0xC102
	)
	unicodePayload := encodeUTF16ClipboardString("https://例子.test/路径")
	ansiRead := false
	access := clipboardURLAccess{
		candidates: []clipboardURLCandidate{
			{formatID: unicodeID, formatName: clipboardURLFormatUnicodeName, decode: decodeUTF16ClipboardString},
			{formatID: ansiID, formatName: clipboardURLFormatANSIName, decode: decodeANSIClipboardString},
		},
		available: func(uint32) bool { return true },
		readBytes: func(format uint32) ([]byte, error) {
			if format == unicodeID {
				return unicodePayload, nil
			}
			ansiRead = true
			return append([]byte("https://example.com/ansi"), 0), nil
		},
	}

	snapshot, ok, err := readClipboardURLWith(access)
	if err != nil || !ok || snapshot.Text != "https://例子.test/路径" {
		t.Fatalf("Unicode URL was not preferred: snapshot=%#v ok=%v err=%v", snapshot, ok, err)
	}
	if ansiRead {
		t.Fatal("ANSI URL was read after Unicode URL succeeded")
	}
}

func TestReadClipboardURLReportsNoRecognizedFormat(t *testing.T) {
	access := clipboardURLAccess{
		candidates: []clipboardURLCandidate{
			{formatID: 0xC101, formatName: clipboardURLFormatUnicodeName, decode: decodeUTF16ClipboardString},
		},
		available: func(uint32) bool { return false },
		readBytes: func(uint32) ([]byte, error) {
			t.Fatal("unavailable URL format must not be read")
			return nil, nil
		},
	}

	snapshot, ok, err := readClipboardURLWith(access)
	if snapshot != nil || ok || err != nil {
		t.Fatalf("unexpected absent URL result: snapshot=%#v ok=%v err=%v", snapshot, ok, err)
	}
}

func TestReadClipboardURLPreservesDelayedUnicodeForRetry(t *testing.T) {
	wantErr := &clipboardOperationError{
		stage: clipboardReadStageGetData, format: clipboardURLFormatUnicodeName, err: errClipboardDataUnavailable,
	}
	access := clipboardURLAccess{
		candidates: []clipboardURLCandidate{
			{formatID: 1, formatName: clipboardURLFormatUnicodeName, decode: decodeUTF16ClipboardString},
			{formatID: 2, formatName: clipboardURLFormatANSIName, decode: func([]byte) (string, error) {
				return "", errors.New("bad ANSI URL")
			}},
		},
		available: func(uint32) bool { return true },
		readBytes: func(format uint32) ([]byte, error) {
			if format == 1 {
				return nil, wantErr
			}
			return []byte("bad payload"), nil
		},
	}

	snapshot, found, err := readClipboardURLWith(access)
	if snapshot != nil || !found || err != wantErr {
		t.Fatalf("delayed Unicode URL was not preserved: snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
}

func TestDecodeUTF16ClipboardString(t *testing.T) {
	got, err := decodeUTF16ClipboardString(encodeUTF16ClipboardString("https://例子.test/路径"))
	if err != nil || got != "https://例子.test/路径" {
		t.Fatalf("decoded URL=%q err=%v", got, err)
	}
	if _, err := decodeUTF16ClipboardString([]byte{1}); err == nil {
		t.Fatal("odd-length UTF-16 payload must fail")
	}
}

func encodeUTF16ClipboardString(text string) []byte {
	units := append(utf16.Encode([]rune(text)), 0)
	raw := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(raw[i*2:], unit)
	}
	return raw
}
