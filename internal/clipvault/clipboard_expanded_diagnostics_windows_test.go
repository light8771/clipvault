//go:build windows

package clipvault

import (
	"context"
	"errors"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestCompositeCaptureFailureCategories(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		reason string
	}{
		{"delayed", errClipboardDataUnavailable, "data_unavailable"},
		{"corrupt", &clipboardOperationError{stage: clipboardReadStageDecode, err: errors.New("invalid path")}, "decode_failed"},
		{"image limit", ErrImageLimitExceeded, "resource_limit"},
		{"text limit", ErrTextLimitExceeded, "resource_limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issue := clipboardRepresentationReadIssue("CF_HDROP", tc.err)
			if issue.Reason != tc.reason {
				t.Fatalf("wrong reason: %+v", issue)
			}
			snapshot, err := buildCapturedComposite(context.Background(), DefaultConfig(), ClipboardFormatSet{}, &ClipboardSnapshot{Format: domain.FormatUnicodeText, Text: "kept"}, nil, nil,
				func(Config) (*ClipboardSnapshot, bool, error) { return nil, true, tc.err })
			if err != nil || snapshot == nil || snapshot.Content == nil || len(snapshot.Content.Issues) != 1 || snapshot.Content.Issues[0].Reason != tc.reason {
				t.Fatalf("wrong image reason: %+v %v", snapshot, err)
			}
		})
	}
}

func TestCompositeFileDropFallbackRetriesAndRecovers(t *testing.T) {
	cfg := DefaultConfig()
	formats := ClipboardFormatSet{IDs: []uint32{cfHDrop, cfUnicodeText}, Names: []string{"CF_HDROP", "CF_UNICODETEXT"}}
	got, _, attempts, err := readClipboardEventWith(context.Background(), cfg, 10,
		func(_ Config, _ uint32, attempt int) (*ClipboardSnapshot, ClipboardFormatSet, error) {
			var issue []domain.ContentIssue
			access := openedClipboardAccess{
				available: func(id uint32) bool { return id == cfHDrop || id == cfUnicodeText },
				readFileDrop: func(Config) (*ClipboardSnapshot, error) {
					if attempt == 1 {
						return nil, errClipboardDataUnavailable
					}
					return newFileDropSnapshot([]string{`C:\a.txt`}, 1)
				},
				readUnicodeText: func(Config) (*ClipboardSnapshot, error) {
					return &ClipboardSnapshot{Format: domain.FormatUnicodeText, Text: "kept"}, nil
				},
				readComposite: func(cfg Config, primary *ClipboardSnapshot) (*ClipboardSnapshot, error) {
					return buildCapturedComposite(context.Background(), cfg, formats, primary, nil, issue, nil)
				},
			}
			if attempt == 1 {
				issue = []domain.ContentIssue{clipboardRepresentationReadIssue("CF_HDROP", errClipboardDataUnavailable)}
			}
			snapshot, err := readOpenedClipboardWith(cfg, access)
			if attempt == 1 && (snapshot == nil || snapshot.Content == nil || len(snapshot.Content.Issues) != 1) {
				t.Fatal("file failure hidden by text fallback")
			}
			return snapshot, formats, err
		}, func() uint32 { return 10 })
	if err != nil || got == nil || got.Format != domain.FormatFileDrop || got.Text != `C:\a.txt` || attempts != 2 {
		t.Fatalf("file capture did not recover: %+v, attempts=%d err=%v", got, attempts, err)
	}
}
