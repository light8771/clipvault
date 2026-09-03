package clipvault

import (
	"context"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

func TestUnavailableReasonForNoSnapshotDistinguishesEmptyClipboard(t *testing.T) {
	tests := []struct {
		name    string
		formats ClipboardFormatSet
		want    string
	}{
		{name: "empty", want: domain.ReasonClipboardEmpty},
		{name: "format ids", formats: ClipboardFormatSet{IDs: []uint32{13}}, want: domain.ReasonUnsupportedFormat},
		{name: "format names", formats: ClipboardFormatSet{Names: []string{"FileContents"}}, want: domain.ReasonUnsupportedFormat},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := unavailableReasonForNoSnapshot(test.formats); got != test.want {
				t.Fatalf("reason = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEventOnlyDedupeIncludesReasonAndFormatSet(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	recorder := &Recorder{store: store}
	base := time.Now().UnixMilli()
	formatA := ClipboardFormatSet{
		IDs:   []uint32{49161, 49444},
		Names: []string{"DataObject", "FileGroupDescriptorW"},
	}
	formatAReordered := ClipboardFormatSet{
		IDs:   []uint32{49444, 49161, 49161},
		Names: []string{"FileGroupDescriptorW", "DataObject", "DataObject"},
	}
	formatB := ClipboardFormatSet{
		IDs:   []uint32{49402},
		Names: []string{"FileContents"},
	}

	recorder.saveEventOnly(SourceInfo{}, base, domain.ReasonClipboardEmpty, ClipboardFormatSet{}, nil)
	recorder.saveEventOnly(SourceInfo{}, base+1, domain.ReasonUnsupportedFormat, formatA, nil)
	recorder.saveEventOnly(SourceInfo{}, base+2, domain.ReasonUnsupportedFormat, formatAReordered, nil)
	recorder.saveEventOnly(SourceInfo{}, base+3, domain.ReasonUnsupportedFormat, formatB, nil)

	items, _, err := store.Search(context.Background(), SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4", len(items))
	}
	byID := make(map[int64]StoredItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	if got := byID[1]; got.UnavailableReason != domain.ReasonClipboardEmpty || got.IsDuplicate || got.MetaJSON != "{}" {
		t.Fatalf("empty event = %+v", got)
	}
	if got := byID[2]; got.UnavailableReason != domain.ReasonUnsupportedFormat || got.IsDuplicate {
		t.Fatalf("first format A event = %+v", got)
	}
	if got := byID[3]; !got.IsDuplicate {
		t.Fatalf("same format set in different order should be duplicate: %+v", got)
	}
	if got := byID[4]; got.IsDuplicate {
		t.Fatalf("different format set must not be duplicate: %+v", got)
	}
}
