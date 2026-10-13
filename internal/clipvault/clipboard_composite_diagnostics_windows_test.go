//go:build windows

package clipvault

import (
	"context"
	"errors"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestClipboardCompositeDelayedCapture(t *testing.T) {
	limitIssue := clipboardRepresentationReadIssue("HTML Format", &clipboardTextPayloadLimitError{Limit: 1, ByteLen: 2})
	if limitIssue.Reason != "resource_limit" {
		t.Fatal("permanent payload limit was classified as temporarily unavailable")
	}
	partial := &ClipboardSnapshot{Format: domain.FormatComposite, Text: "kept", Content: &domain.CompositeContent{Issues: []domain.ContentIssue{{Format: "HTML Format", Reason: "data_unavailable"}}}}
	complete := &ClipboardSnapshot{Format: domain.FormatComposite, Text: "kept", Content: &domain.CompositeContent{}}
	recovered := &ClipboardSnapshot{Format: domain.FormatComposite, Text: "kept", Content: &domain.CompositeContent{
		Representations: []domain.ContentRepresentation{{Format: compositeHTML, Data: []byte("<p>kept</p>")}},
		Issues:          []domain.ContentIssue{{Format: "HTML Format", Reason: "resource_unavailable"}, {Format: "Rich Text Format", Reason: "decode_failed"}},
	}}
	retryingRecovered := &ClipboardSnapshot{Format: domain.FormatComposite, Text: "kept", Content: &domain.CompositeContent{
		Representations: recovered.Content.Representations,
		Issues:          []domain.ContentIssue{{Format: "HTML Format", Reason: "resource_unavailable"}, {Format: "Rich Text Format", Reason: "data_unavailable"}},
	}}
	formats := ClipboardFormatSet{Names: []string{"CF_UNICODETEXT", "HTML Format"}}
	unavailable := &clipboardReadError{stage: clipboardReadStageGetData, err: errClipboardDataUnavailable}
	for _, tc := range []struct {
		name     string
		next     *ClipboardSnapshot
		err      error
		sequence uint32
		want     *ClipboardSnapshot
		wantErr  error
		attempts int
	}{
		{"delayed HTML recovers", complete, nil, 10, complete, nil, 2},
		{"recovered HTML has more detailed diagnostics", recovered, nil, 10, recovered, nil, 2},
		{"recovered HTML survives remaining retries", retryingRecovered, nil, 10, retryingRecovered, nil, 5},
		{"partial stays available", partial, nil, 10, partial, nil, 5},
		{"later reads fail", nil, unavailable, 10, partial, nil, 5},
		{"later read empty", nil, nil, 10, partial, nil, 2},
		{"sequence changed", nil, nil, 11, nil, ErrClipboardSuperseded, 1},
		{"new clipboard during read", nil, ErrClipboardSuperseded, 10, nil, ErrClipboardSuperseded, 2},
		{"content limit remains terminal", nil, ErrTextLimitExceeded, 10, nil, ErrTextLimitExceeded, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, attempts, err := readClipboardEventWith(context.Background(), DefaultConfig(), 10,
				func(_ Config, _ uint32, attempt int) (*ClipboardSnapshot, ClipboardFormatSet, error) {
					if attempt == 1 {
						return partial, formats, nil
					}
					return tc.next, formats, tc.err
				}, func() uint32 { return tc.sequence })
			if got != tc.want || !errors.Is(err, tc.wantErr) || attempts != tc.attempts {
				t.Fatalf("got=%p want=%p attempts=%d want=%d err=%v", got, tc.want, attempts, tc.attempts, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, _, _, err := readClipboardEventWith(ctx, DefaultConfig(), 10,
		func(Config, uint32, int) (*ClipboardSnapshot, ClipboardFormatSet, error) {
			cancel()
			return partial, formats, nil
		},
		func() uint32 { return 10 })
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %+v %v", got, err)
	}
	for _, reason := range []string{"decode_failed", "resource_limit", "resource_unavailable", "images_disabled", "virtual_files_unsupported"} {
		snapshot := &ClipboardSnapshot{Content: &domain.CompositeContent{Issues: []domain.ContentIssue{{Reason: reason}}}}
		if shouldRetryCompositeCapture(snapshot, 1) {
			t.Fatalf("permanent issue retried: %s", reason)
		}
	}
}

func TestCapturedCompositeFileAlternatives(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		primary                    *ClipboardSnapshot
		virtual, brokenImage, warn bool
	}{
		{"files with virtual alternative", &ClipboardSnapshot{Format: domain.FormatFileDrop, Text: "C:\\a.txt\nC:\\b.png"}, true, false, false},
		{"files with broken thumbnail", &ClipboardSnapshot{Format: domain.FormatFileDrop, Text: "C:\\a.txt\nC:\\b.png"}, true, true, false},
		{"text with broken image", &ClipboardSnapshot{Format: domain.FormatUnicodeText, Text: "kept"}, false, true, true},
		{"text with virtual files", &ClipboardSnapshot{Format: domain.FormatUnicodeText, Text: "kept"}, true, false, true},
		{"truncated file list", &ClipboardSnapshot{Format: domain.FormatFileDrop, Text: `C:\a.txt`, Meta: map[string]any{"truncated": true}}, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			formats := ClipboardFormatSet{}
			if tc.virtual {
				formats.Names = []string{"FileGroupDescriptorW", "FileContents"}
			}
			reader := func(Config) (*ClipboardSnapshot, bool, error) {
				if tc.brokenImage {
					return nil, true, errors.New("image unavailable")
				}
				return nil, false, nil
			}
			snapshot, err := buildCapturedComposite(context.Background(), DefaultConfig(), formats, tc.primary, nil, nil, reader)
			if err != nil || snapshot == nil || snapshot.Content == nil || (len(snapshot.Content.Issues) > 0) != tc.warn {
				t.Fatalf("unexpected capture: %+v %v", snapshot, err)
			}
			if !strings.Contains(snapshot.Text, strings.Split(tc.primary.Text, "\n")[0]) {
				t.Fatal("primary content was lost")
			}
		})
	}
}

func TestClipboardRetryMergesOnlyTrackedSequence(t *testing.T) {
	previous := retryCompositeFixture(t, map[string][]byte{"HTML Format": []byte(`<p>kept</p>`)}, []domain.ContentIssue{{Format: "Rich Text Format", Reason: "data_unavailable"}}, false)
	current := retryCompositeFixture(t, map[string][]byte{"Rich Text Format": []byte(`{\rtf1 kept}`)}, []domain.ContentIssue{{Format: "HTML Format", Reason: "data_unavailable"}}, false)
	for _, expected := range []uint32{0, 10} {
		got, _, attempts, err := readClipboardEventWith(context.Background(), DefaultConfig(), expected,
			func(_ Config, _ uint32, attempt int) (*ClipboardSnapshot, ClipboardFormatSet, error) {
				if attempt == 1 {
					return previous, ClipboardFormatSet{}, nil
				}
				return current, ClipboardFormatSet{}, nil
			}, func() uint32 { return 10 })
		if err != nil {
			t.Fatal(err)
		}
		if expected == 0 {
			if got != current || attempts != 5 {
				t.Fatal("untracked events were merged")
			}
		} else if got == current || len(got.Content.Representations) != 3 || len(got.Content.Issues) != 0 || attempts != 2 {
			t.Fatalf("tracked event lost a recovered format: %+v", got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, _, _, err := readClipboardEventWith(ctx, DefaultConfig(), 10,
		func(Config, uint32, int) (*ClipboardSnapshot, ClipboardFormatSet, error) {
			cancel()
			return current, ClipboardFormatSet{}, nil
		},
		func() uint32 { return 10 })
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled read returned recoverable content")
	}
	attempts := 0
	got, _, _, err = readClipboardEventWith(context.Background(), DefaultConfig(), 10,
		func(Config, uint32, int) (*ClipboardSnapshot, ClipboardFormatSet, error) {
			attempts++
			if attempts == 1 {
				return previous, ClipboardFormatSet{}, nil
			}
			return current, ClipboardFormatSet{}, nil
		}, func() uint32 {
			if attempts > 1 {
				return 11
			}
			return 10
		})
	if got != nil || !errors.Is(err, ErrClipboardSuperseded) {
		t.Fatal("recovery merged across a sequence change")
	}
}
