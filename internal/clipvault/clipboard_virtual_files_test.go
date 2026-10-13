package clipvault

import (
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestVirtualFileWarningRequiresMissingFileCapture(t *testing.T) {
	formats := ClipboardFormatSet{Names: []string{"CF_HDROP", "FileGroupDescriptorW", "FileContents"}}
	for _, tc := range []struct {
		name    string
		primary *ClipboardSnapshot
		warn    bool
	}{
		{"no captured files", nil, true},
		{"text fallback", &ClipboardSnapshot{Format: domain.FormatUnicodeText, Text: "file.txt"}, true},
		{"empty file list", &ClipboardSnapshot{Format: domain.FormatFileDrop}, true},
		{"complete file list", &ClipboardSnapshot{Format: domain.FormatFileDrop, Text: "C:\\files\\note.txt\nC:\\files\\photo.png"}, false},
		{"truncated file list", &ClipboardSnapshot{Format: domain.FormatFileDrop, Text: "C:\\files\\note.txt", Meta: map[string]any{"truncated": true}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasUncapturedVirtualFiles(formats, tc.primary); got != tc.warn {
				t.Fatalf("warning=%v want=%v", got, tc.warn)
			}
		})
	}
	if hasUncapturedVirtualFiles(ClipboardFormatSet{Names: []string{"CF_HDROP"}}, nil) {
		t.Fatal("ordinary file format classified as virtual")
	}
	// Advertising CF_HDROP is insufficient if its read failed: true virtual
	// files must retain their empty-capture event and incomplete diagnostics.
	if classifyEmptyCapture(formats) != domain.EventVirtualFile {
		t.Fatal("virtual-file event classification changed")
	}
}
