//go:build windows

package clipvault

import (
	"errors"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

func TestClipboardFormatNamePreservesLongRegisteredName(t *testing.T) {
	name := "ClipVault-Test-" + strings.Repeat("x", 180)
	format := registerClipboardFormat(name)
	if format == 0 {
		t.Fatal("RegisterClipboardFormatW failed")
	}
	if got := clipboardFormatName(format); got != name {
		t.Fatalf("clipboard format name = %q, want %q", got, name)
	}
}

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
	for _, err := range []error{
		&clipboardReadError{stage: clipboardReadStageOpen, err: errorClipboardNotOpen},
		&clipboardReadError{stage: clipboardReadStageGetData, err: errClipboardDataUnavailable},
	} {
		if !isRetryableClipboardReadError(err) {
			t.Fatalf("expected retryable error: %v", err)
		}
	}
	if isRetryableClipboardReadError(syscall.Errno(5)) {
		t.Fatal("unstructured errors must not restart the complete read lifecycle")
	}
}

func TestClipboardRetryScheduleReservesDelayedAttemptForUnavailableData(t *testing.T) {
	busy := &clipboardReadError{stage: clipboardReadStageOpen, err: errorClipboardNotOpen}
	for completed := 1; completed < len(clipboardCaptureSchedule)-1; completed++ {
		if !shouldRetryClipboardCapture(busy, completed) {
			t.Fatalf("clipboard busy stopped during fast retry %d", completed)
		}
	}
	if shouldRetryClipboardCapture(busy, len(clipboardCaptureSchedule)-1) {
		t.Fatal("clipboard busy must not use the delayed 600ms recovery attempt")
	}
	unavailable := &clipboardReadError{stage: clipboardReadStageGetData, err: errClipboardDataUnavailable}
	if !shouldRetryClipboardCapture(unavailable, len(clipboardCaptureSchedule)-1) {
		t.Fatal("advertised but unavailable data must receive the delayed 600ms recovery attempt")
	}
	if shouldRetryClipboardCapture(unavailable, len(clipboardCaptureSchedule)) {
		t.Fatal("clipboard capture retried past the configured schedule")
	}
	want := []time.Duration{0, 20 * time.Millisecond, 80 * time.Millisecond, 200 * time.Millisecond, 600 * time.Millisecond}
	if !slices.Equal(clipboardCaptureSchedule[:], want) {
		t.Fatalf("capture schedule = %v, want %v", clipboardCaptureSchedule, want)
	}
}

func TestClipboardNotOpenRetriesCompleteCaptureWithinFastSchedule(t *testing.T) {
	for _, err := range []error{
		&clipboardReadError{stage: clipboardReadStageClose, err: errorClipboardNotOpen},
		&clipboardReadError{stage: clipboardReadStageGetData, err: errorClipboardNotOpen},
		&clipboardReadError{stage: clipboardReadStageDecode, err: errors.New("decode failed"), closeErr: errorClipboardNotOpen},
	} {
		for completed := 1; completed < len(clipboardCaptureSchedule); completed++ {
			want := completed < len(clipboardCaptureSchedule)-1
			if got := shouldRetryClipboardCapture(err, completed); got != want {
				t.Fatalf("retry %v after attempt %d = %v, want %v", err, completed, got, want)
			}
		}
	}
	if shouldRetryClipboardCapture(&clipboardReadError{stage: clipboardReadStageClose, err: syscall.Errno(5)}, 1) {
		t.Fatal("unrelated close errors must not be retried")
	}
}

func TestReadOpenedClipboardFallsBackFromUnavailableFileDropToURL(t *testing.T) {
	want := newURLClipboardSnapshot("https://example.com/from-weixin", clipboardURLFormatUnicodeName)
	access := openedClipboardAccess{
		available: func(format uint32) bool { return format == cfHDrop },
		readUnicodeText: func(Config) (*ClipboardSnapshot, error) {
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
	imageCalled := false
	access := openedClipboardAccess{
		available: func(format uint32) bool { return format == cfHDrop || format == cfUnicodeText },
		readUnicodeText: func(Config) (*ClipboardSnapshot, error) {
			t.Fatal("Unicode fallback ran after a successful CF_HDROP read")
			return nil, nil
		},
		readImage: func(Config) (*ClipboardSnapshot, bool, error) {
			imageCalled = true
			return nil, false, nil
		},
		readFileDrop: func(Config) (*ClipboardSnapshot, error) { return want, nil },
		readURL: func() (*ClipboardSnapshot, bool, error) {
			urlCalled = true
			return nil, false, nil
		},
	}

	got, err := readOpenedClipboardWith(DefaultConfig(), access)
	if err != nil || got != want {
		t.Fatalf("file drop priority drifted: snapshot=%#v err=%v", got, err)
	}
	if imageCalled || urlCalled {
		t.Fatalf("fallback ran after a successful CF_HDROP read: image=%v url=%v", imageCalled, urlCalled)
	}
}

func TestReadOpenedClipboardFallsBackFromFileDropToImage(t *testing.T) {
	want := &ClipboardSnapshot{Kind: domain.KindImage, Format: domain.FormatImageFile, ImageBytes: []byte("png")}
	access := openedClipboardAccess{
		available:       func(format uint32) bool { return format == cfHDrop },
		readUnicodeText: func(Config) (*ClipboardSnapshot, error) { return nil, nil },
		readFileDrop: func(Config) (*ClipboardSnapshot, error) {
			return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_HDROP", err: errClipboardDataUnavailable}
		},
		readImage: func(Config) (*ClipboardSnapshot, bool, error) { return want, true, nil },
		readURL:   func() (*ClipboardSnapshot, bool, error) { return nil, false, nil },
	}

	got, err := readOpenedClipboardWith(DefaultConfig(), access)
	if err != nil || got != want {
		t.Fatalf("image fallback after unavailable file drop: snapshot=%#v err=%v", got, err)
	}
}

func TestReadOpenedClipboardPreservesFileDropErrorWithoutFallback(t *testing.T) {
	wantErr := &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_HDROP", err: errClipboardDataUnavailable}
	access := openedClipboardAccess{
		available:       func(format uint32) bool { return format == cfHDrop },
		readUnicodeText: func(Config) (*ClipboardSnapshot, error) { return nil, nil },
		readImage:       func(Config) (*ClipboardSnapshot, bool, error) { return nil, false, nil },
		readFileDrop:    func(Config) (*ClipboardSnapshot, error) { return nil, wantErr },
		readURL:         func() (*ClipboardSnapshot, bool, error) { return nil, false, nil },
	}

	snapshot, err := readOpenedClipboardWith(DefaultConfig(), access)
	if snapshot != nil || !errors.Is(err, errClipboardDataUnavailable) {
		t.Fatalf("file drop error was masked: snapshot=%#v err=%v", snapshot, err)
	}
}

func TestReadOpenedClipboardDoesNotBypassImageSizePolicyAfterUnavailableFileDrop(t *testing.T) {
	fileDropCalled := false
	urlCalled := false
	access := openedClipboardAccess{
		available:       func(format uint32) bool { return format == cfHDrop },
		readUnicodeText: func(Config) (*ClipboardSnapshot, error) { return nil, nil },
		readImage:       func(Config) (*ClipboardSnapshot, bool, error) { return nil, true, ErrImageLimitExceeded },
		readFileDrop: func(Config) (*ClipboardSnapshot, error) {
			fileDropCalled = true
			return nil, &clipboardOperationError{
				stage:  clipboardReadStageGetData,
				format: "CF_HDROP",
				err:    errClipboardDataUnavailable,
			}
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
	if !fileDropCalled || urlCalled {
		t.Fatal("expected file drop first, then terminal image policy failure without URL fallback")
	}
}

func TestReadOpenedClipboardFallsBackFromUnicodeToRecoveredText(t *testing.T) {
	want := &ClipboardSnapshot{Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "HTML recovered"}
	access := openedClipboardAccess{
		available:       func(format uint32) bool { return format == cfUnicodeText },
		readUnicodeText: func(Config) (*ClipboardSnapshot, error) { return nil, errClipboardDataUnavailable },
		readTextFallback: func() (*ClipboardSnapshot, bool, error) {
			return want, true, nil
		},
		readImage: func(Config) (*ClipboardSnapshot, bool, error) {
			t.Fatal("image should not run")
			return nil, false, nil
		},
		readFileDrop: func(Config) (*ClipboardSnapshot, error) { t.Fatal("files should not run"); return nil, nil },
		readURL:      func() (*ClipboardSnapshot, bool, error) { t.Fatal("URL should not run"); return nil, false, nil },
	}
	got, err := readOpenedClipboardWith(DefaultConfig(), access)
	if err != nil || got != want {
		t.Fatalf("fallback result = %#v, err=%v", got, err)
	}
}

func TestReadOpenedClipboardPreservesDelayedFormatForRetry(t *testing.T) {
	wantErr := &clipboardOperationError{
		stage:  clipboardReadStageGetData,
		format: "CF_UNICODETEXT",
		err:    errClipboardDataUnavailable,
	}
	access := openedClipboardAccess{
		available:       func(format uint32) bool { return format == cfUnicodeText },
		readUnicodeText: func(Config) (*ClipboardSnapshot, error) { return nil, wantErr },
		readTextFallback: func() (*ClipboardSnapshot, bool, error) {
			return nil, true, &clipboardOperationError{
				stage: clipboardReadStageDecode, format: clipboardHTMLFormatName, err: errors.New("bad HTML"),
			}
		},
		readImage:    func(Config) (*ClipboardSnapshot, bool, error) { return nil, false, nil },
		readFileDrop: func(Config) (*ClipboardSnapshot, error) { return nil, nil },
		readURL:      func() (*ClipboardSnapshot, bool, error) { return nil, false, nil },
	}

	snapshot, err := readOpenedClipboardWith(DefaultConfig(), access)
	if snapshot != nil || !errors.Is(err, errClipboardDataUnavailable) {
		t.Fatalf("delayed format was hidden by a later decode error: snapshot=%#v err=%v", snapshot, err)
	}
	if err != wantErr {
		t.Fatalf("selected error = %v, want highest-priority delayed error %v", err, wantErr)
	}
}

func TestReadOpenedClipboardTextLimitIsTerminal(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxTextChars = 3
	fallbackCalled := false
	access := openedClipboardAccess{
		available: func(format uint32) bool { return format == cfUnicodeText },
		readUnicodeText: func(gotCfg Config) (*ClipboardSnapshot, error) {
			if gotCfg.MaxTextChars != cfg.MaxTextChars {
				t.Fatalf("reader config limit = %d", gotCfg.MaxTextChars)
			}
			return nil, &clipboardOperationError{
				stage: clipboardReadStageDecode, format: "CF_UNICODETEXT",
				err: &clipboardTextLimitError{CharLen: 4, Limit: 3},
			}
		},
		readTextFallback: func() (*ClipboardSnapshot, bool, error) {
			fallbackCalled = true
			return &ClipboardSnapshot{Kind: domain.KindText, Text: "abc"}, true, nil
		},
		readImage:    func(Config) (*ClipboardSnapshot, bool, error) { return nil, false, nil },
		readFileDrop: func(Config) (*ClipboardSnapshot, error) { return nil, nil },
		readURL:      func() (*ClipboardSnapshot, bool, error) { return nil, false, nil },
	}

	snapshot, err := readOpenedClipboardWith(cfg, access)
	if snapshot != nil || !errors.Is(err, ErrTextLimitExceeded) {
		t.Fatalf("text limit result = %#v, err=%v", snapshot, err)
	}
	if fallbackCalled {
		t.Fatal("a lower-priority representation bypassed the text limit")
	}
}

func TestNewUnicodeTextClipboardSnapshotRejectsEmptyText(t *testing.T) {
	snapshot, err := newUnicodeTextClipboardSnapshot(DefaultConfig(), []uint16{0})
	var operationErr *clipboardOperationError
	if snapshot != nil || !errors.As(err, &operationErr) {
		t.Fatalf("empty Unicode result: snapshot=%#v err=%v", snapshot, err)
	}
	if operationErr.stage != clipboardReadStageDecode || operationErr.format != "CF_UNICODETEXT" {
		t.Fatalf("empty Unicode classification = %s/%s", operationErr.stage, operationErr.format)
	}
}

func TestInspectClipboardUTF16BytesMatchesDecodedRuneCount(t *testing.T) {
	// A, one surrogate pair, one unpaired high surrogate, NUL.
	raw := []byte{'A', 0, 0x3d, 0xd8, 0x00, 0xde, 0x00, 0xd8, 0, 0}
	charLen, terminated := inspectClipboardUTF16Bytes(raw)
	if !terminated || charLen != 3 {
		t.Fatalf("UTF-16 byte inspection = (%d, %v), want (3, true)", charLen, terminated)
	}
}

func TestReadOpenedClipboardValidatesFallbackTextBeforeReturning(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxTextChars = 3
	access := openedClipboardAccess{
		available:       func(uint32) bool { return false },
		readUnicodeText: func(Config) (*ClipboardSnapshot, error) { return nil, nil },
		readTextFallback: func() (*ClipboardSnapshot, bool, error) {
			return &ClipboardSnapshot{Kind: domain.KindText, Text: "four", CharLen: 4}, true, nil
		},
		readImage:    func(Config) (*ClipboardSnapshot, bool, error) { return nil, false, nil },
		readFileDrop: func(Config) (*ClipboardSnapshot, error) { return nil, nil },
		readURL:      func() (*ClipboardSnapshot, bool, error) { return nil, false, nil },
	}

	snapshot, err := readOpenedClipboardWith(cfg, access)
	if snapshot != nil || !errors.Is(err, ErrTextLimitExceeded) {
		t.Fatalf("fallback text limit result = %#v, err=%v", snapshot, err)
	}
}

func TestTextFallbackContinuesFromBadHTMLToRTF(t *testing.T) {
	candidates := []clipboardTextCandidate{
		{id: 1, name: clipboardHTMLFormatName, decode: func([]byte) (string, error) { return "", errors.New("bad HTML") }},
		{id: 2, name: clipboardRTFFormatName, decode: func([]byte) (string, error) { return "RTF recovered", nil }},
		{id: 3, name: "CF_TEXT", decode: func([]byte) (string, error) { t.Fatal("ANSI should not run"); return "", nil }},
	}
	readOrder := make([]uint32, 0, 2)
	snapshot, found, err := readClipboardTextFallbackWith(candidates,
		func(uint32) bool { return true },
		func(format uint32) ([]byte, error) {
			readOrder = append(readOrder, format)
			return []byte("payload"), nil
		})
	if err != nil || !found || snapshot == nil || snapshot.Text != "RTF recovered" {
		t.Fatalf("RTF fallback = %#v, found=%v, err=%v", snapshot, found, err)
	}
	if len(readOrder) != 2 || readOrder[0] != 1 || readOrder[1] != 2 {
		t.Fatalf("candidate order = %v", readOrder)
	}
}

func TestTextFallbackContinuesFromUnsupportedRTFFontEncodingToANSI(t *testing.T) {
	const (
		rtfID  = 1
		ansiID = 2
	)
	candidates := []clipboardTextCandidate{
		{id: rtfID, name: clipboardRTFFormatName, decode: extractRTFText},
		{id: ansiID, name: "CF_TEXT", decode: func([]byte) (string, error) { return "ANSI recovered", nil }},
	}
	readOrder := make([]uint32, 0, len(candidates))
	snapshot, found, err := readClipboardTextFallbackWith(candidates,
		func(uint32) bool { return true },
		func(format uint32) ([]byte, error) {
			readOrder = append(readOrder, format)
			if format == rtfID {
				return []byte(`{\rtf1\ansi{\fonttbl{\f1\fnil\fcharset2 Symbol;}}\f1 abc}`), nil
			}
			return []byte("ANSI payload"), nil
		})
	if err != nil || !found || snapshot == nil || snapshot.Text != "ANSI recovered" {
		t.Fatalf("ANSI fallback = %#v, found=%v, err=%v", snapshot, found, err)
	}
	if len(readOrder) != 2 || readOrder[0] != rtfID || readOrder[1] != ansiID {
		t.Fatalf("candidate order = %v", readOrder)
	}
}

func TestTextFallbackPreservesDelayedFormatForRetry(t *testing.T) {
	wantErr := &clipboardOperationError{
		stage: clipboardReadStageGetData, format: clipboardHTMLFormatName, err: errClipboardDataUnavailable,
	}
	candidates := []clipboardTextCandidate{
		{id: 1, name: clipboardHTMLFormatName, decode: func([]byte) (string, error) { return "", nil }},
		{id: 2, name: clipboardRTFFormatName, decode: func([]byte) (string, error) { return "", errors.New("bad RTF") }},
	}
	snapshot, found, err := readClipboardTextFallbackWith(candidates,
		func(uint32) bool { return true },
		func(format uint32) ([]byte, error) {
			if format == 1 {
				return nil, wantErr
			}
			return []byte("bad payload"), nil
		})
	if snapshot != nil || !found || err != wantErr {
		t.Fatalf("delayed text format was not preserved: snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
}

func TestTextFallbackPreservesLimitOverLaterDecodeFailure(t *testing.T) {
	wantErr := &clipboardTextPayloadLimitError{ByteLen: maxClipboardTextPayloadBytes + 1, Limit: maxClipboardTextPayloadBytes}
	candidates := []clipboardTextCandidate{
		{id: 1, name: clipboardHTMLFormatName, decode: func([]byte) (string, error) { return "", nil }},
		{id: 2, name: clipboardRTFFormatName, decode: func([]byte) (string, error) { return "", errors.New("bad RTF") }},
	}
	snapshot, found, err := readClipboardTextFallbackWith(candidates,
		func(uint32) bool { return true },
		func(format uint32) ([]byte, error) {
			if format == 1 {
				return nil, wantErr
			}
			return []byte("bad payload"), nil
		})
	if snapshot != nil || !found || err != wantErr || !errors.Is(err, ErrTextLimitExceeded) {
		t.Fatalf("text limit was hidden by a later decode failure: snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
}

func TestClipboardSequenceSupersededRequiresKnownDifferentSequence(t *testing.T) {
	for _, test := range []struct {
		name               string
		expected, observed uint32
		want               bool
	}{
		{name: "same sequence", expected: 42, observed: 42},
		{name: "changed during read", expected: 42, observed: 43, want: true},
		{name: "unknown expected sequence", expected: 0, observed: 43},
		{name: "observed sequence cleared", expected: 42, observed: 0, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := clipboardSequenceSuperseded(test.expected, test.observed); got != test.want {
				t.Fatalf("clipboardSequenceSuperseded(%d, %d) = %v, want %v", test.expected, test.observed, got, test.want)
			}
		})
	}
}

func TestClassifyFileDropKindIsDeterministicForMixedFiles(t *testing.T) {
	if got := classifyFileDropKind([]string{"report.pdf", "photo.png"}); got != domain.KindFiles {
		t.Fatalf("mixed tied file kinds = %q, want %q", got, domain.KindFiles)
	}
	if got := classifyFileDropKind([]string{"first.png", "second.jpg", "report.pdf"}); got != domain.KindImage {
		t.Fatalf("majority file kind = %q, want %q", got, domain.KindImage)
	}
}

func TestNewFileDropSnapshotRejectsAdvertisedEmptyDrop(t *testing.T) {
	snapshot, err := newFileDropSnapshot(nil, 0)
	var operationErr *clipboardOperationError
	if snapshot != nil || !errors.As(err, &operationErr) {
		t.Fatalf("empty file drop result: snapshot=%#v err=%v", snapshot, err)
	}
	if operationErr.stage != clipboardReadStageDecode || operationErr.format != "CF_HDROP" {
		t.Fatalf("empty file drop classification = %s/%s", operationErr.stage, operationErr.format)
	}
}

func TestReadFileDropPathRequiresCompleteSecondRead(t *testing.T) {
	query := func(_ uint32, dst []uint16) (uint32, error) {
		if dst == nil {
			return 3, nil
		}
		copy(dst, []uint16{'a', 'b'})
		return 2, nil
	}
	if _, err := readFileDropPath(0, query); err == nil {
		t.Fatal("changed DragQueryFileW length was accepted")
	}
}

func TestReadFileDropPathReturnsExactUTF16Path(t *testing.T) {
	query := func(_ uint32, dst []uint16) (uint32, error) {
		if dst == nil {
			return 4, nil
		}
		copy(dst, []uint16{'C', ':', '\\', 'x', 0})
		return 4, nil
	}
	path, err := readFileDropPath(0, query)
	if err != nil {
		t.Fatalf("readFileDropPath: %v", err)
	}
	if path != `C:\x` {
		t.Fatalf("path = %q, want %q", path, `C:\x`)
	}
}

func TestClipboardFileDropLimitAlwaysAppliesSafetyCap(t *testing.T) {
	tests := []struct {
		name       string
		total      uint32
		configured int
		want       int
	}{
		{name: "below cap", total: 12, configured: 0, want: 12},
		{name: "zero config uses safety cap", total: 5000, configured: 0, want: maxClipboardFileDropPaths},
		{name: "configured lower limit wins", total: 5000, configured: 25, want: 25},
		{name: "available count wins", total: 7, configured: 25, want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clipboardFileDropLimit(tt.total, tt.configured); got != tt.want {
				t.Fatalf("clipboardFileDropLimit(%d, %d) = %d, want %d", tt.total, tt.configured, got, tt.want)
			}
		})
	}
}
