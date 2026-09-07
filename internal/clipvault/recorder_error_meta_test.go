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

func TestClipboardFailureDetailKeepsStructureWithoutRawErrorMessage(t *testing.T) {
	detail := clipboardFailureDetailFor(testClipboardDiagnosticError{})
	if detail.Stage != "open" {
		t.Fatalf("error stage not preserved: %#v", detail)
	}
	if detail.Win32Error != 5 || detail.ClipboardOpenerPID != 13700 {
		t.Fatalf("Win32 context not preserved: %#v", detail)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"stage":"open"`,
		`"openAttempts":8`,
		`"clipboardOpenerPid":13700`,
		`"win32Error":5`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("encoded metadata missing %s: %s", want, encoded)
		}
	}
	if strings.Contains(string(encoded), "access denied") || strings.Contains(string(encoded), "message") {
		t.Fatalf("raw error text entered persistent detail: %s", encoded)
	}
}
