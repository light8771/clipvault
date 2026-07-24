package clipvault

import (
	"context"
	"testing"
)

func TestStoreSaveAndSearch(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(context.Background(), ClipboardItem{
		Kind:     "text",
		Format:   "CF_UNICODETEXT",
		Text:     "alpha project note",
		ByteLen:  18,
		CharLen:  18,
		SHA256:   hashString("alpha project note"),
		MetaJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}

	items, total, err := store.Search(context.Background(), SearchOptions{Query: "project", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if total != 1 {
		t.Fatalf("expected total 1, got %d", total)
	}
	if items[0].Text != "alpha project note" {
		t.Fatalf("unexpected text %q", items[0].Text)
	}
}

func TestStoreSearchMatchesAllSearchableFields(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	items := []ClipboardItem{
		{
			Kind:     "files",
			Format:   "CF_HDROP",
			Text:     "C:\\work\\release\\notes.txt",
			ByteLen:  26,
			CharLen:  26,
			SHA256:   "a1b2c3d4",
			MetaJSON: `{"source":"Explorer","label":"Release notes"}`,
		},
		{
			Kind:      "image",
			Format:    "CF_DIBV5",
			Text:      "",
			ByteLen:   128,
			CharLen:   0,
			SHA256:    "deadbeef",
			MetaJSON:  `{"application":"Screenshot Tool"}`,
			Sensitive: true,
		},
		{
			Kind:     "text",
			Format:   "CF_UNICODETEXT",
			Text:     "发布计划 🚀",
			ByteLen:  18,
			CharLen:  7,
			SHA256:   "emoji123",
			MetaJSON: "{}",
		},
	}
	for _, item := range items {
		if err := store.Save(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		query string
		want  string
	}{
		{query: "RELEASE", want: "files"},
		{query: "file", want: "files"},
		{query: "hdrop", want: "files"},
		{query: "a1b2", want: "files"},
		{query: "explorer", want: "files"},
		{query: "release file", want: "files"},
		{query: "🚀", want: "text"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			found, _, err := store.Search(context.Background(), SearchOptions{Query: tt.query, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if len(found) != 1 || found[0].Kind != tt.want {
				t.Fatalf("query %q returned %+v, want one %q item", tt.query, found, tt.want)
			}
		})
	}

	matched, _, err := store.Search(context.Background(), SearchOptions{Query: "release", Limit: 1})
	if err != nil || len(matched) != 1 {
		t.Fatalf("find item to group: items=%+v err=%v", matched, err)
	}
	group, err := store.CreateGroup(context.Background(), "工程档案", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddItemToGroup(context.Background(), matched[0].ID, group.ID); err != nil {
		t.Fatal(err)
	}
	groupMatches, _, err := store.Search(context.Background(), SearchOptions{Query: "工程档案", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(groupMatches) != 1 || groupMatches[0].ID != matched[0].ID {
		t.Fatalf("group search returned %+v, want item %d", groupMatches, matched[0].ID)
	}

	private, _, err := store.Search(context.Background(), SearchOptions{Query: "screenshot", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(private) != 0 {
		t.Fatalf("sensitive item leaked into default search: %+v", private)
	}

	quickResults, total, err := store.Search(context.Background(), SearchOptions{
		Query:     "release",
		Limit:     20,
		SkipTotal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(quickResults) != 1 || total != 0 {
		t.Fatalf("quick search got %d results and total %d, want one result with no total", len(quickResults), total)
	}
}

func TestItemGroupReferentialIntegrity(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "record", ByteLen: 6, CharLen: 6, SHA256: "record", MetaJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ExportAll(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("export items = %#v, err = %v", items, err)
	}
	group, err := store.CreateGroup(ctx, "group", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddItemToGroup(ctx, 999, group.ID); err == nil {
		t.Fatal("accepted an item-group link for a missing item")
	}
	if err := store.AddItemToGroup(ctx, items[0].ID, 999); err == nil {
		t.Fatal("accepted an item-group link for a missing group")
	}
	if err := store.AddItemToGroup(ctx, items[0].ID, group.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.AddItemToGroup(ctx, items[0].ID, group.ID); err != nil {
		t.Fatalf("duplicate item-group link should be idempotent: %v", err)
	}
	if err := store.Delete(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	export, err := store.ExportFullForJSON(ctx)
	if err != nil || len(export.ItemGroups) != 0 {
		t.Fatalf("delete left item-group links: %#v, err = %v", export.ItemGroups, err)
	}

	if err := store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "second", ByteLen: 6, CharLen: 6, SHA256: "second", MetaJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	items, err = store.ExportAll(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("export items = %#v, err = %v", items, err)
	}
	if err := store.AddItemToGroup(ctx, items[0].ID, group.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClearAll(ctx); err != nil {
		t.Fatal(err)
	}
	export, err = store.ExportFullForJSON(ctx)
	if err != nil || len(export.Items) != 0 || len(export.ItemGroups) != 0 {
		t.Fatalf("clear left exported data: %#v, err = %v", export, err)
	}
}
