package clipvault

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStoreSaveAndSearch(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "clipvault.db"))
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

	items, _, err := store.Search(context.Background(), SearchOptions{Query: "project", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Text != "alpha project note" {
		t.Fatalf("unexpected text %q", items[0].Text)
	}
}
