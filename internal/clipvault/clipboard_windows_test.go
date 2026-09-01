//go:build windows

package clipvault

import (
	"errors"
	"syscall"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

func TestClipboardReadErrorMatchesPrimaryAndCloseErrors(t *testing.T) {
	err := &clipboardReadError{
		err:      errClipboardDataUnavailable,
		closeErr: errorClipboardNotOpen,
	}
	if !errors.Is(err, errClipboardDataUnavailable) {
		t.Fatal("primary clipboard error was not preserved")
	}
	if !errors.Is(err, errorClipboardNotOpen) {
		t.Fatal("CloseClipboard 1418 must trigger the bounded retry path")
	}
	if errors.Is(err, syscall.Errno(5)) {
		t.Fatal("unrelated Win32 error unexpectedly matched")
	}
}

func TestClipboardReadErrorDiagnosticFieldsAreStructured(t *testing.T) {
	err := &clipboardReadError{
		stage:              clipboardReadStageGetData,
		format:             "CF_DIBV5",
		err:                errorClipboardNotOpen,
		attempt:            2,
		openAttempts:       1,
		threadIDBefore:     7,
		threadIDAfter:      7,
		expectedSequence:   10,
		sequenceBeforeOpen: 10,
		sequenceAfterOpen:  10,
		sequenceAfterRead:  10,
		clipboardOwnerPid:  20,
		clipboardOpenerPid: 30,
		duration:           318 * time.Millisecond,
		closeErr:           errorClipboardNotOpen,
	}
	fields := err.DiagnosticFields()
	if fields["stage"] != "get_data" || fields["format"] != "CF_DIBV5" {
		t.Fatalf("unexpected stage/format fields: %#v", fields)
	}
	if fields["win32Error"] != int64(1418) || fields["closeWin32Error"] != int64(1418) {
		t.Fatalf("Win32 error codes were not normalized: %#v", fields)
	}
	if fields["threadIdBefore"] != uint32(7) || fields["threadIdAfter"] != uint32(7) {
		t.Fatalf("thread identity fields were not preserved: %#v", fields)
	}
	detail := err.ClipboardFailureDetail()
	if detail.Stage != clipboardReadStageGetData || detail.Format != "CF_DIBV5" || detail.Attempt != 2 {
		t.Fatalf("persistent detail drifted from diagnostics: %#v", detail)
	}
	if detail.Win32Error != 1418 || detail.CloseWin32Error != 1418 || detail.DurationMS != 318 {
		t.Fatalf("persistent Win32 context was not normalized: %#v", detail)
	}
}

func TestRetryableClipboardReadErrors(t *testing.T) {
	for _, err := range []error{errorClipboardNotOpen, errClipboardDataUnavailable} {
		if !isRetryableClipboardReadError(err) {
			t.Fatalf("expected retryable error: %v", err)
		}
	}
	if isRetryableClipboardReadError(syscall.Errno(5)) {
		t.Fatal("access denied must not restart the complete read lifecycle")
	}
}

func TestReadOpenedClipboardFallsBackFromUnavailableFileDropToURL(t *testing.T) {
	want := newURLClipboardSnapshot("https://example.com/from-weixin", clipboardURLFormatUnicodeName)
	access := openedClipboardAccess{
		available: func(format uint32) bool { return format == cfHDrop },
		readUnicodeText: func() (*ClipboardSnapshot, error) {
			t.Fatal("Unicode text should not be read when it is not advertised")
			return nil, nil
		},
		readImage: func(Config) (*ClipboardSnapshot, bool, error) { return nil, false, nil },
		readFileDrop: func(Config) (*ClipboardSnapshot, error) {
			return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_HDROP", err: errClipboardDataUnavailable}
		},
		readURL: func() (*ClipboardSnapshot, bool, error) { return want, true, nil },
	}

	got, err := readOpenedClipboardWith(DefaultConfig(), access)
	if err != nil {
		t.Fatalf("URL fallback failed: %v", err)
	}
	if got != want || got.Kind != domain.KindText || got.Format != domain.FormatUnicodeText {
		t.Fatalf("unexpected URL fallback snapshot: %#v", got)
	}
}

func TestReadOpenedClipboardPreservesSuccessfulFileDropPriority(t *testing.T) {
	want := &ClipboardSnapshot{Kind: domain.KindFiles, Format: domain.FormatFileDrop, Text: `C:\tmp\report.pdf`}
	urlCalled := false
	access := openedClipboardAccess{
		available:       func(format uint32) bool { return format == cfHDrop },
		readUnicodeText: func() (*ClipboardSnapshot, error) { return nil, nil },
		readImage:       func(Config) (*ClipboardSnapshot, bool, error) { return nil, false, nil },
		readFileDrop:    func(Config) (*ClipboardSnapshot, error) { return want, nil },
		readURL: func() (*ClipboardSnapshot, bool, error) {
			urlCalled = true
			return nil, false, nil
		},
	}

	got, err := readOpenedClipboardWith(DefaultConfig(), access)
	if err != nil || got != want {
		t.Fatalf("file drop priority drifted: snapshot=%#v err=%v", got, err)
	}
	if urlCalled {
		t.Fatal("URL fallback ran after a successful CF_HDROP read")
	}
}

func TestReadOpenedClipboardPreservesFileDropErrorWithoutFallback(t *testing.T) {
	wantErr := &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_HDROP", err: errClipboardDataUnavailable}
	access := openedClipboardAccess{
		available:       func(format uint32) bool { return format == cfHDrop },
		readUnicodeText: func() (*ClipboardSnapshot, error) { return nil, nil },
		readImage:       func(Config) (*ClipboardSnapshot, bool, error) { return nil, false, nil },
		readFileDrop:    func(Config) (*ClipboardSnapshot, error) { return nil, wantErr },
		readURL:         func() (*ClipboardSnapshot, bool, error) { return nil, false, nil },
	}

	snapshot, err := readOpenedClipboardWith(DefaultConfig(), access)
	if snapshot != nil || !errors.Is(err, errClipboardDataUnavailable) {
		t.Fatalf("file drop error was masked: snapshot=%#v err=%v", snapshot, err)
	}
}

func TestReadOpenedClipboardDoesNotBypassImageSizePolicy(t *testing.T) {
	fileDropCalled := false
	urlCalled := false
	access := openedClipboardAccess{
		available:       func(format uint32) bool { return format == cfHDrop },
		readUnicodeText: func() (*ClipboardSnapshot, error) { return nil, nil },
		readImage:       func(Config) (*ClipboardSnapshot, bool, error) { return nil, true, ErrImageLimitExceeded },
		readFileDrop: func(Config) (*ClipboardSnapshot, error) {
			fileDropCalled = true
			return nil, nil
		},
		readURL: func() (*ClipboardSnapshot, bool, error) {
			urlCalled = true
			return nil, false, nil
		},
	}

	snapshot, err := readOpenedClipboardWith(DefaultConfig(), access)
	if snapshot != nil || !errors.Is(err, ErrImageLimitExceeded) {
		t.Fatalf("image size policy was bypassed: snapshot=%#v err=%v", snapshot, err)
	}
	if fileDropCalled || urlCalled {
		t.Fatal("lower-priority formats ran after image size rejection")
	}
}
