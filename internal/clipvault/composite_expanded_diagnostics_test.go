package clipvault

import (
	"errors"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestCompositeAlternateFormatDoesNotHideUnreadFormats(t *testing.T) {
	for _, reason := range []string{"data_unavailable", "decode_failed", "resource_limit", "resource_unavailable"} {
		t.Run(reason, func(t *testing.T) {
			issues := []domain.ContentIssue{{Format: "HTML Format", Reason: reason}}
			raw := map[string][]byte{"WeChat_RichEdit_Format": []byte(`<WeChatRichEditFormat><EditElement type="0">kept</EditElement></WeChatRichEditFormat>`)}
			snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, raw, issues, readCompositeImage)
			if err != nil || snapshot == nil || snapshot.Content == nil {
				t.Fatalf("adapter failed: %+v %v", snapshot, err)
			}
			wantIssues := 1
			if len(snapshot.Content.Issues) != wantIssues || snapshot.Text != "kept" || len(issues) != 1 {
				t.Fatalf("wrong recovery diagnostics: %v", snapshot.Content.Issues)
			}
		})
	}
	// Successful parsing must not hide a missing image in the recovered format.
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{
		"HTML Format":                []byte("Version:1.0\r\nStartHTML:invalid\r\n"),
		"QQ_Unicode_RichEdit_Format": []byte(`<QQRichEditFormat><EditElement type="0">kept</EditElement><EditElement type="1" filepath="C:\missing.png"/></QQRichEditFormat>`),
	}, nil, func(string, int) ([]byte, error) { return nil, errors.New("missing") })
	if err != nil || snapshot == nil || snapshot.Content == nil || len(snapshot.Content.Issues) != 1 || snapshot.Content.Issues[0].Reason != "resource_unavailable" {
		t.Fatalf("recovered image diagnostic incorrect: %+v %v", snapshot, err)
	}
}

func TestCompositeRetryRecoversFileDrop(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		previous, err := buildCompositeSnapshot(DefaultConfig(), &ClipboardSnapshot{Format: domain.FormatFileDrop, Text: `C:\a.txt`, Meta: map[string]any{"truncated": truncated}}, nil, nil,
			[]domain.ContentIssue{{Format: "HTML Format", Reason: "data_unavailable"}}, readCompositeImage)
		if err != nil {
			t.Fatal(err)
		}
		current := retryCompositeFixture(t, map[string][]byte{"HTML Format": []byte(`<p>kept</p>`)}, []domain.ContentIssue{
			{Format: "CF_HDROP", Reason: "data_unavailable"},
			{Format: "FileContents", Reason: "virtual_files_unsupported"},
			{Format: "image/png", Reason: "data_unavailable"},
		}, false)
		merged := mergeCompositeRetry(DefaultConfig(), previous, current)
		found := false
		for _, r := range merged.Content.Representations {
			found = found || r.Format == compositeFiles
		}
		wantIssues := 0
		if truncated {
			wantIssues = 3
		}
		limited := false
		for _, issue := range merged.Content.Issues {
			limited = limited || issue.Format == "CF_HDROP" && issue.Reason == "resource_limit"
		}
		if !found || len(merged.Content.Issues) != wantIssues || limited != truncated {
			t.Fatalf("file recovery lost data or diagnostics: %+v", merged.Content)
		}
	}
}
