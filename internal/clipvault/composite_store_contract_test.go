package clipvault

import (
	"context"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestCompositeSaveRejectsIncorrectSuppliedFingerprint(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := compositeFixture(t, `C:\source.png`)
	item := ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content, ContentSHA256: strings.Repeat("0", 64)}
	if err := store.Save(ctx, item); err == nil {
		t.Fatal("incorrect supplied fingerprint was silently replaced")
	}
	items, err := exportAll(ctx, store.db)
	if err != nil || len(items) != 0 {
		t.Fatalf("invalid capture was stored: %d %v", len(items), err)
	}
	item.ContentSHA256 = snapshot.SHA256
	if err := store.Save(ctx, item); err != nil {
		t.Fatal(err)
	}
	item.ContentSHA256 = ""
	if err := store.Save(ctx, item); err != nil {
		t.Fatal(err)
	}
	items, err = exportAll(ctx, store.db)
	if err != nil || len(items) != 2 {
		t.Fatalf("valid captures: %d %v", len(items), err)
	}
	for _, got := range items {
		if got.ContentSHA256 == nil || *got.ContentSHA256 != snapshot.SHA256 {
			t.Fatal("fingerprint differs from capture")
		}
	}
}

func TestCompositeFingerprintNormalizesEmptyResources(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	content := &domain.CompositeContent{Representations: []domain.ContentRepresentation{{Format: compositeText, Data: []byte("same")}}, Kinds: []string{"text"}}
	first := compositeFingerprint(content)
	if content.Assets != nil {
		t.Fatal("hashing mutated the package")
	}
	for _, resources := range [][]domain.ContentAsset{nil, {}} {
		content.Assets = resources
		if compositeFingerprint(content) != first {
			t.Fatal("empty resource representation changed identity")
		}
		if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: domain.KindComposite, Format: domain.FormatComposite, Text: "same", Content: content, ContentSHA256: first}); err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := store.Search(ctx, SearchOptions{Limit: 10})
	if err != nil || len(items) != 2 || !items[0].ConsecutiveDuplicate {
		t.Fatalf("equivalent packages did not deduplicate: %+v %v", items, err)
	}
}
