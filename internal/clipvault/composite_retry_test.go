package clipvault

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func retryCompositeFixture(t *testing.T, raw map[string][]byte, issues []domain.ContentIssue, missingImage bool) *ClipboardSnapshot {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	snapshot, err := buildCompositeSnapshot(cfg, &ClipboardSnapshot{Format: domain.FormatUnicodeText, Text: "kept"}, nil, raw, issues,
		func(string, int) ([]byte, error) {
			if missingImage {
				return nil, errors.New("missing image")
			}
			return managedImageTestPNG(t), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestCompositeRetryPreservesComplementaryFormats(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "with asset", true: "with missing asset"}[missing], func(t *testing.T) {
			previous := retryCompositeFixture(t, map[string][]byte{"HTML Format": []byte(`<p>kept<img src="C:\image.png"></p>`)}, []domain.ContentIssue{{Format: "Rich Text Format", Reason: "data_unavailable"}}, missing)
			current := retryCompositeFixture(t, map[string][]byte{"Rich Text Format": []byte(`{\rtf1 kept}`)}, []domain.ContentIssue{{Format: "HTML Format", Reason: "data_unavailable"}}, false)
			beforePrevious, beforeCurrent := compositeJSONString(previous.Content), compositeJSONString(current.Content)
			merged := mergeCompositeRetry(DefaultConfig(), previous, current)
			if merged == current || len(merged.Content.Representations) != 3 || len(merged.Content.Assets) != len(previous.Content.Assets) {
				t.Fatalf("formats or assets lost: %+v", merged)
			}
			if missing {
				if len(merged.Content.Issues) != 1 || merged.Content.Issues[0].Reason != "resource_unavailable" {
					t.Fatalf("real loss hidden: %v", merged.Content.Issues)
				}
			} else if len(merged.Content.Issues) != 0 {
				t.Fatalf("recovered formats still marked missing: %v", merged.Content.Issues)
			}
			text, size, err := validateComposite(merged.Content)
			if err != nil || text != merged.Text || size != merged.ByteLen || merged.CharLen != textRuneLen(text) || merged.SHA256 != compositeFingerprint(merged.Content) {
				t.Fatalf("merged metadata invalid: %v", err)
			}
			if beforePrevious != compositeJSONString(previous.Content) || beforeCurrent != compositeJSONString(current.Content) {
				t.Fatal("retry mutated a captured snapshot")
			}
		})
	}
}

func TestCompositeRetryKeepsLatestValidRepresentation(t *testing.T) {
	previous := retryCompositeFixture(t, map[string][]byte{"HTML Format": []byte(`<b>older formatting</b>`)}, nil, false)
	current := retryCompositeFixture(t, map[string][]byte{"HTML Format": []byte(`<i>new formatting</i>`)}, nil, false)
	if mergeCompositeRetry(DefaultConfig(), previous, current) != current {
		t.Fatal("older valid format replaced the latest read")
	}
	for _, reason := range []string{"decode_failed", "resource_limit", "images_disabled"} {
		current = retryCompositeFixture(t, nil, []domain.ContentIssue{{Format: "HTML Format", Reason: reason}}, false)
		if mergeCompositeRetry(DefaultConfig(), previous, current) != current {
			t.Fatalf("permanent failure overridden: %s", reason)
		}
	}
}

func TestCompositeRetryRevalidatesLimitsAndAssets(t *testing.T) {
	previous := retryCompositeFixture(t, map[string][]byte{"HTML Format": []byte(`<p>longer alternate text<img src="C:\image.png"></p>`)}, nil, false)
	current := retryCompositeFixture(t, nil, []domain.ContentIssue{{Format: "HTML Format", Reason: "data_unavailable"}}, false)
	cfg := DefaultConfig()
	cfg.MaxTextChars = 5
	if mergeCompositeRetry(cfg, previous, current) != current {
		t.Fatal("merged text bypassed configured text limit")
	}
	previous.Content.Assets[0].Data = []byte("corrupted")
	if mergeCompositeRetry(DefaultConfig(), previous, current) != current {
		t.Fatal("merged assets bypassed integrity check")
	}
}

func TestCompositeRetryRecoversBitmap(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	previous, err := buildCompositeSnapshot(cfg, &ClipboardSnapshot{Format: domain.FormatUnicodeText, Text: "kept"}, &ClipboardSnapshot{Format: domain.FormatImageFile, ImageBytes: managedImageTestPNG(t)}, nil,
		[]domain.ContentIssue{{Format: "HTML Format", Reason: "data_unavailable"}}, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	current := retryCompositeFixture(t, map[string][]byte{"HTML Format": []byte(`<p>kept</p>`)}, []domain.ContentIssue{{Format: "image/png", Reason: "data_unavailable"}}, false)
	merged := mergeCompositeRetry(cfg, previous, current)
	if merged == current || len(merged.Content.Issues) != 0 || !slices.Contains(merged.Content.Kinds, "image") || !strings.Contains(merged.Text, "kept") {
		t.Fatalf("bitmap retry lost content: %+v", merged)
	}
}

func TestCompositeRetryOnlyMergesReferencedAssets(t *testing.T) {
	previous := retryCompositeFixture(t, map[string][]byte{
		"HTML Format":   []byte(`<p>kept</p>`),
		compositeWeChat: []byte(`<WeChatRichEditFormat><EditElement type="1" filepath="C:\image.png"/></WeChatRichEditFormat>`),
	}, nil, false)
	if len(previous.Content.Assets) != 1 {
		t.Fatal("missing fixture asset")
	}
	current := retryCompositeFixture(t, map[string][]byte{compositeWeChat: []byte(`<WeChatRichEditFormat><EditElement type="0">kept</EditElement></WeChatRichEditFormat>`)}, []domain.ContentIssue{{Format: "HTML Format", Reason: "data_unavailable"}}, false)
	merged := mergeCompositeRetry(DefaultConfig(), previous, current)
	if merged == current || len(merged.Content.Assets) != 0 || len(merged.Content.Issues) != 0 {
		t.Fatal("retry imported resources from an unrelated representation")
	}
	if _, _, err := validateComposite(merged.Content); err != nil {
		t.Fatal(err)
	}
}

func TestCompositeRetryRetainsSuccessfulResourceBackup(t *testing.T) {
	raw := map[string][]byte{"HTML Format": []byte(`<p>kept<img src="C:\image.png"></p>`)}
	previous := retryCompositeFixture(t, raw, []domain.ContentIssue{{Format: "Rich Text Format", Reason: "data_unavailable"}}, false)
	current := retryCompositeFixture(t, raw, nil, true)
	before := compositeJSONString(current.Content)
	merged := mergeCompositeRetry(DefaultConfig(), previous, current)
	if merged == current || len(merged.Content.Assets) != 1 || len(merged.Content.Issues) != 0 {
		t.Fatal("successful backup lost on retry")
	}
	if _, _, err := validateComposite(merged.Content); err != nil {
		t.Fatal(err)
	}
	if compositeJSONString(current.Content) != before {
		t.Fatal("retry mutated snapshot")
	}
	changed := retryCompositeFixture(t, map[string][]byte{"HTML Format": []byte(`<p>new<img src="C:\image.png"></p>`)}, nil, true)
	if mergeCompositeRetry(DefaultConfig(), previous, changed) != changed {
		t.Fatal("older backup replaced changed markup")
	}
	previous.Content.Issues = append(previous.Content.Issues, domain.ContentIssue{Format: "HTML Format", Reason: "resource_unavailable"})
	if mergeCompositeRetry(DefaultConfig(), previous, current) != current {
		t.Fatal("incomplete old backup cleared diagnostics")
	}
}
