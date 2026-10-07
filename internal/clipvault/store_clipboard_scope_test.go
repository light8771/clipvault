package clipvault

import (
	"context"
	"errors"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestPublicClipboardScopeMatchesContentSearchWithEvents(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i, text := range []string{"outside", "visible oldest", "private", "visible newest"} {
		if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: domain.KindText,
			Format: domain.FormatUnicodeText, Text: text, Sensitive: i == 2}); err != nil {
			t.Fatal(err)
		}
		if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeEvent, EventCode: domain.EventClipboardEmpty}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := exportAll(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range []int{3, 0} {
		visible, _, err := store.Search(ctx, SearchOptions{Limit: 100, RestrictToLatestN: window})
		if err != nil {
			t.Fatal(err)
		}
		wantCount := 2
		if window == 0 {
			wantCount = 3
		}
		if len(visible) != wantCount {
			t.Fatalf("window %d search returned %d items, want %d", window, len(visible), wantCount)
		}
		ids := map[int64]bool{}
		for _, item := range visible {
			ids[item.ID] = true
		}
		for _, item := range all {
			got, err := store.getPublicClipboardItem(ctx, item.ID, window)
			if ids[item.ID] {
				if err != nil || got.Text != item.Text {
					t.Fatalf("visible item %d cannot restore in window %d: %+v %v", item.ID, window, got, err)
				}
			} else if !errors.Is(err, ErrItemNotFound) {
				t.Fatalf("hidden/event item %d accessible in window %d: %v", item.ID, window, err)
			}
		}
	}
}
