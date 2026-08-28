//go:build windows

package clipvault

import (
	"errors"
	"syscall"
	"testing"
	"time"
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
}
