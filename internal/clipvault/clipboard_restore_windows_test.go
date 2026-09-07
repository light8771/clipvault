//go:build windows

package clipvault

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"
	"unsafe"

	"clipboard_ext/internal/clipvault/domain"
)

func TestClipboardRestoreRequiresOwnerWindow(t *testing.T) {
	seq, err := restoreItemToClipboard(0, StoredItem{
		RecordType: domain.HistoryRecordContent, Format: domain.FormatUnicodeText, Text: "test",
	}, t.TempDir())
	if err == nil || seq != 0 {
		t.Fatalf("missing owner: sequence=%d err=%v", seq, err)
	}
}

// Exercise the real Win32 open call without clearing or writing the clipboard.
func TestOpenClipboardWithOwnerWindow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, err := syscall.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}
	// HWND_MESSAGE creates an invisible message-only window on this thread.
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0,
		0, 0, 0, 0, ^uintptr(2), 0, 0, 0)
	if hwnd == 0 {
		t.Fatalf("create clipboard owner window: %v", err)
	}
	defer procDestroyWindow.Call(hwnd)
	if _, err := openClipboardWithRetry(hwnd, clipboardOpenAttempts); err != nil {
		t.Fatal(err)
	}
	defer closeClipboard()
	openedBy, _, _ := procGetOpenClipboardWindow.Call()
	if openedBy != hwnd {
		t.Fatalf("clipboard opened by %#x, want owner window %#x", openedBy, hwnd)
	}
}

func TestPrepareClipboardRestorePayloadText(t *testing.T) {
	format, payload, err := prepareClipboardRestorePayload(StoredItem{
		RecordType: domain.HistoryRecordContent,
		Format:     domain.FormatUnicodeText,
		Text:       "你好\U0001f44b",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if format != cfUnicodeText {
		t.Fatalf("format=%d, want CF_UNICODETEXT", format)
	}
	wantUnits := append(utf16.Encode([]rune("你好\U0001f44b")), 0)
	gotUnits := make([]uint16, len(payload)/2)
	for i := range gotUnits {
		gotUnits[i] = uint16(payload[2*i]) | uint16(payload[2*i+1])<<8
	}
	if !reflect.DeepEqual(gotUnits, wantUnits) {
		t.Fatalf("UTF-16 payload=%v, want %v", gotUnits, wantUnits)
	}
}

func TestPrepareClipboardRestorePayloadFileDrop(t *testing.T) {
	format, payload, err := prepareClipboardRestorePayload(StoredItem{
		RecordType: domain.HistoryRecordContent,
		Format:     domain.FormatFileDrop,
		Text:       "C:\\one.txt\r\nD:\\two.txt\n",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if format != cfHDrop || len(payload) < 24 || payload[0] != 20 || payload[16] != 1 {
		t.Fatalf("invalid DROPFILES payload: format=%d bytes=%v", format, payload)
	}
	if payload[len(payload)-1] != 0 || payload[len(payload)-2] != 0 ||
		payload[len(payload)-3] != 0 || payload[len(payload)-4] != 0 {
		t.Fatal("DROPFILES path list is not double-NUL terminated")
	}
}

func TestPrepareClipboardRestorePayloadRejectsUnavailableContent(t *testing.T) {
	for name, item := range map[string]StoredItem{
		"event": {
			RecordType: domain.HistoryRecordEvent,
		},
		"empty file list": {
			RecordType: domain.HistoryRecordContent,
			Format:     domain.FormatFileDrop,
			Text:       "\r\n",
		},
		"unknown format": {
			RecordType: domain.HistoryRecordContent,
			Format:     "UNKNOWN",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := prepareClipboardRestorePayload(item, t.TempDir()); !errors.Is(err, errItemContentUnavailable) {
				t.Fatalf("err=%v, want errItemContentUnavailable", err)
			}
		})
	}
}

func TestPrepareClipboardRestorePayloadRejectsEmbeddedNUL(t *testing.T) {
	for name, item := range map[string]StoredItem{
		"text": {
			RecordType: domain.HistoryRecordContent,
			Format:     domain.FormatUnicodeText,
			Text:       "visible\x00hidden",
		},
		"file drop": {
			RecordType: domain.HistoryRecordContent,
			Format:     domain.FormatFileDrop,
			Text:       "C:\\visible.txt\x00D:\\hidden.txt",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := prepareClipboardRestorePayload(item, t.TempDir()); err == nil {
				t.Fatal("embedded NUL was accepted")
			}
		})
	}
}

func TestPrepareClipboardRestorePayloadEnforcesTextSafetyLimit(t *testing.T) {
	text := strings.Repeat("a", maxClipboardTextPayloadBytes/2)
	if got := clipboardUTF16PayloadByteLen(text); got != maxClipboardTextPayloadBytes+2 {
		t.Fatalf("payload bytes=%d, want %d", got, maxClipboardTextPayloadBytes+2)
	}
	_, _, err := prepareClipboardRestorePayload(StoredItem{
		RecordType: domain.HistoryRecordContent,
		Format:     domain.FormatUnicodeText,
		Text:       text,
	}, t.TempDir())
	if !errors.Is(err, ErrTextLimitExceeded) {
		t.Fatalf("err=%v, want ErrTextLimitExceeded", err)
	}
}

func TestValidateClipboardFileDropRestoreLimits(t *testing.T) {
	tooMany := make([]string, maxClipboardFileDropPaths+1)
	for index := range tooMany {
		tooMany[index] = `C:\item.txt`
	}
	if err := validateClipboardFileDropRestore(tooMany); err == nil {
		t.Fatal("too many file-drop paths were accepted")
	}
	if err := validateClipboardFileDropRestore([]string{strings.Repeat("a", maxFileDropPathUTF16Units+1)}); err == nil {
		t.Fatal("overlong file-drop path was accepted")
	}
	if err := validateClipboardFileDropRestore([]string{`C:\ok.txt`}); err != nil {
		t.Fatalf("valid file-drop path rejected: %v", err)
	}
}

func TestPrepareClipboardRestorePayloadValidatesManagedImageBeforeClipboardMutation(t *testing.T) {
	dataDir := t.TempDir()
	base := StoredItem{
		RecordType: domain.HistoryRecordContent,
		Format:     domain.FormatImageFile,
	}
	for name, text := range map[string]string{
		"traversal": "images/../../outside.png",
		"absolute":  filepath.Join(dataDir, "images", "capture.png"),
		"missing":   "images/2026/09/missing.png",
	} {
		t.Run(name, func(t *testing.T) {
			item := base
			item.Text = text
			if _, _, err := prepareClipboardRestorePayload(item, dataDir); err == nil {
				t.Fatal("invalid managed image was accepted")
			}
		})
	}

	storedPath := "images/2026/09/capture.png"
	fullPath := filepath.Join(dataDir, filepath.FromSlash(storedPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		t.Fatal(err)
	}
	pngBytes, err := dibToPNG(onePixelDIB())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	wrongHash := hashString("different")
	item := base
	item.Text = storedPath
	item.ByteLen = len(pngBytes)
	item.ContentSHA256 = &wrongHash
	if _, _, err := prepareClipboardRestorePayload(item, dataDir); err == nil {
		t.Fatal("managed image with a mismatched content hash was accepted")
	}

	item.ByteLen = maxImageMaxBytes + 1
	if _, _, err := prepareClipboardRestorePayload(item, dataDir); err == nil {
		t.Fatal("managed image above the hard size limit was accepted")
	}
}
