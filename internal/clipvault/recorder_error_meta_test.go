package clipvault

import (
	"encoding/json"
	"strings"
	"testing"
)

type testClipboardDiagnosticError struct{}

func (testClipboardDiagnosticError) Error() string { return "clipboard open failed: access denied" }

func (testClipboardDiagnosticError) ClipboardFailureDetail() clipboardFailureDetail {
	return clipboardFailureDetail{
		Stage:              "open",
		Format:             "unknown",
		OpenAttempts:       8,
		ClipboardOpenerPID: 13700,
		Win32Error:         5,
	}
}

func TestClipboardReadFailureMetaPreservesStructuredDetails(t *testing.T) {
	meta := clipboardReadFailureMeta(testClipboardDiagnosticError{})
	detail, ok := meta["error"].(clipboardFailureDetail)
	if !ok {
		t.Fatalf("missing nested error metadata: %#v", meta)
	}
	if detail.Message != "clipboard open failed: access denied" || detail.Stage != "open" {
		t.Fatalf("error message/stage not preserved: %#v", detail)
	}
	if detail.Win32Error != 5 || detail.ClipboardOpenerPID != 13700 {
		t.Fatalf("Win32 context not preserved: %#v", detail)
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"message":"clipboard open failed: access denied"`,
		`"stage":"open"`,
		`"openAttempts":8`,
		`"clipboardOpenerPid":13700`,
		`"win32Error":5`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("encoded metadata missing %s: %s", want, encoded)
		}
	}
}

func TestBoundedClipboardErrorMessageUsesRuneLimit(t *testing.T) {
	message := strings.Repeat("错", clipboardErrorMessageMaxRunes+10)
	got := boundedClipboardErrorMessage(message)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated message has no ellipsis: %q", got)
	}
	if count := len([]rune(strings.TrimSuffix(got, "…"))); count != clipboardErrorMessageMaxRunes {
		t.Fatalf("truncated rune count = %d, want %d", count, clipboardErrorMessageMaxRunes)
	}
}
