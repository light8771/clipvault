package clipvault

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"sync"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

// Check persisted adjacency against an independent walk of the surviving
// timeline after mutations, then check the public projection and pagination.
func assertDuplicateTimeline(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	all, _, err := store.Search(ctx, SearchOptions{IncludeSensitive: true, IncludeAllRecords: true, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(all)
	wantPublic := map[int64]bool{}
	wantUnique := 0
	var previous *StoredItem
	for i := range all {
		item := &all[i]
		if item.RecordType == RecordTypeEvent {
			if item.ConsecutiveDuplicate {
				t.Fatalf("event %d marked duplicate", item.ID)
			}
			continue
		}
		duplicate := previous != nil && item.Kind == previous.Kind && item.Format == previous.Format && *item.ContentSHA256 == *previous.ContentSHA256
		if item.ConsecutiveDuplicate != duplicate {
			t.Fatalf("persisted id=%d duplicate=%v want=%v", item.ID, item.ConsecutiveDuplicate, duplicate)
		}
		if !item.Sensitive {
			wantPublic[item.ID] = duplicate && !previous.Sensitive
			if !wantPublic[item.ID] {
				wantUnique++
			}
		}
		previous = item
	}
	public, total, err := store.Search(ctx, SearchOptions{Limit: 500})
	if err != nil || total != len(wantPublic) || len(public) != total {
		t.Fatalf("public count=%d/%d want=%d err=%v", len(public), total, len(wantPublic), err)
	}
	for _, item := range public {
		want, exists := wantPublic[item.ID]
		if !exists || item.ConsecutiveDuplicate != want {
			t.Fatalf("public id=%d duplicate=%v want=%v exists=%v", item.ID, item.ConsecutiveDuplicate, want, exists)
		}
	}
	unique, total, err := store.Search(ctx, SearchOptions{Limit: 500, HideConsecutiveDuplicates: true})
	if err != nil || total != wantUnique || len(unique) != total {
		t.Fatalf("unique count=%d/%d want=%d err=%v", len(unique), total, wantUnique, err)
	}
	for _, item := range unique {
		if wantPublic[item.ID] {
			t.Fatalf("hidden duplicate %d returned", item.ID)
		}
	}
	svc := NewExternalReadService(store, Environment{})
	seen := map[int64]bool{}
	cursor := ""
	for page := 0; ; page++ {
		if page > len(wantPublic) {
			t.Fatal("cursor pagination did not terminate")
		}
		result, err := svc.Search(ctx, deniedAccess, SearchRequest{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range result.Items {
			id, err := strconv.ParseInt(item.ID, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			want, exists := wantPublic[id]
			if !exists || seen[id] || item.ConsecutiveDuplicate != want {
				t.Fatalf("cursor id=%d duplicate=%v want=%v exists=%v seen=%v", id, item.ConsecutiveDuplicate, want, exists, seen[id])
			}
			seen[id] = true
		}
		if !result.HasMore {
			break
		}
		if result.NextCursor == nil {
			t.Fatal("missing cursor")
		}
		cursor = *result.NextCursor
	}
	if len(seen) != len(wantPublic) {
		t.Fatalf("cursor returned %d records, want %d", len(seen), len(wantPublic))
	}
}

func TestDuplicateVisibilityLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openDataAPITestStore(t)
	seeds := []ClipboardItem{
		{RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "A", Sensitive: true},
		{RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "A"},
		{RecordType: RecordTypeEvent, EventCode: domain.EventClipboardEmpty},
		{RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "A", Starred: true},
		{RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "B"},
		{RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "A", Starred: true},
		{RecordType: RecordTypeContent, Kind: domain.KindFiles, Format: domain.FormatFileDrop, Text: "A"},
		{RecordType: RecordTypeContent, Kind: domain.KindFiles, Format: domain.FormatFileDrop, Text: "A"},
	}
	ids := make([]int64, len(seeds))
	for i, seed := range seeds {
		// Timestamp order deliberately disagrees with capture ID order.
		seed.CapturedAtMS = int64(10000 - i*1000)
		if err := store.Save(ctx, seed); err != nil {
			t.Fatal(err)
		}
		if err := store.db.QueryRowContext(ctx, "SELECT MAX(id) FROM clipboard_items").Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	assertDuplicateTimeline(t, store)
	for _, id := range []int64{ids[0], ids[2], ids[4], ids[6]} {
		if err := store.Delete(ctx, id); err != nil {
			t.Fatal(err)
		}
		assertDuplicateTimeline(t, store)
	}
	for _, sensitive := range []bool{true, false} {
		if err := store.SetSensitive(ctx, ids[1], sensitive); err != nil {
			t.Fatal(err)
		}
		assertDuplicateTimeline(t, store)
	}
	if _, err := store.Cleanup(ctx, CleanupOptions{Mode: "all", PreserveStarred: true}); err != nil {
		t.Fatal(err)
	}
	assertDuplicateTimeline(t, store)
	export := exportAllForTest(t, store)
	// Imported flags are derived data, not a trusted assertion from the file.
	for i := range export.Items {
		export.Items[i].ConsecutiveDuplicate = !export.Items[i].ConsecutiveDuplicate
	}
	payload, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ImportFullJSON(ctx, payload); err != nil {
		t.Fatal(err)
	}
	assertDuplicateTimeline(t, store)
	if err := store.importExportData(ctx, *export); err != nil {
		t.Fatal(err)
	}
	assertDuplicateTimeline(t, store)
	if _, err := store.Cleanup(ctx, CleanupOptions{Mode: "all"}); err != nil {
		t.Fatal(err)
	}
	assertDuplicateTimeline(t, store)
}

func TestManagedImageDuplicateUsesContentNotPath(t *testing.T) {
	ctx := context.Background()
	store := openDataAPITestStore(t)
	for i, path := range []string{"images/first.png", "images/second.png"} {
		if err := store.Save(ctx, ClipboardItem{
			RecordType: RecordTypeContent, Kind: domain.KindImage, Format: domain.FormatImageFile,
			Text: path, ByteLen: 100, ContentSHA256: hashString("same encoded image bytes"), Sensitive: i == 0,
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertDuplicateTimeline(t, store)
	items, _, err := store.Search(ctx, SearchOptions{IncludeSensitive: true})
	if err != nil || len(items) != 2 || !items[0].ConsecutiveDuplicate {
		t.Fatalf("image duplicate missing: count=%d err=%v", len(items), err)
	}
	if err := store.SetSensitive(ctx, items[1].ID, false); err != nil {
		t.Fatal(err)
	}
	assertDuplicateTimeline(t, store)
}

func TestConcurrentCapturesPreserveDuplicateTimeline(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()
	const captures = 24
	start := make(chan struct{})
	errors := make(chan error, captures)
	var workers sync.WaitGroup
	for i := range captures {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			item := ClipboardItem{RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "concurrent sample"}
			if i%4 == 0 {
				item = ClipboardItem{RecordType: RecordTypeEvent, EventCode: domain.EventClipboardEmpty}
			}
			errors <- store.Save(ctx, item)
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	items, total, err := store.Search(ctx, SearchOptions{IncludeSensitive: true})
	if err != nil || total != 18 || len(items) != 18 {
		t.Fatalf("concurrent count=%d/%d err=%v", len(items), total, err)
	}
	assertDuplicateTimeline(t, store)
}
