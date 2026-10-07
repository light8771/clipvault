//go:build windows

package clipvault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestCompositeRestoreTextLimits(t *testing.T) {
	for _, format := range []string{compositeText, compositeHTML} {
		t.Run(format, func(t *testing.T) {
			value := strings.Repeat("a", maxClipboardTextPayloadBytes/2)
			content := &domain.CompositeContent{Representations: []domain.ContentRepresentation{{Format: format, Data: []byte(value)}}}
			text, kinds, err := compositeTextAndKinds(content)
			if err != nil {
				t.Fatal(err)
			}
			content.Kinds = kinds
			hash := compositeFingerprint(content)
			item := StoredItem{RecordType: RecordTypeContent, Kind: domain.KindComposite, Format: domain.FormatComposite, Text: text, Content: content, ContentSHA256: &hash}
			_, err = prepareClipboardRestoreRepresentations(item, t.TempDir())
			var limit *clipboardTextPayloadLimitError
			if !errors.As(err, &limit) {
				t.Fatalf("oversized Unicode accepted: %v", err)
			}
		})
	}
	if _, err := prepareClipboardRestoreRepresentations(StoredItem{RecordType: domain.HistoryRecordEvent, Format: domain.FormatComposite}, t.TempDir()); !errors.Is(err, errItemContentUnavailable) {
		t.Fatalf("event reached composite restore: %v", err)
	}
}

func TestCompositeSharedRecoveryCache(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := compositeFixture(t, `C:\source.png`)
	for range 2 {
		if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := exportAll(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareClipboardRestoreRepresentations(items[0], dir); err != nil {
		t.Fatal(err)
	}
	asset := snapshot.Content.Assets[0]
	path := filepath.Join(dir, "restore-assets", asset.ID+".png")
	for _, corrupted := range [][]byte{[]byte("wrong size"), make([]byte, len(asset.Data))} {
		if err := os.WriteFile(path, corrupted, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareClipboardRestoreRepresentations(items[0], dir); err != nil {
			t.Fatal(err)
		}
		if !compositeCacheMatches(path, asset.ID, len(asset.Data)) {
			t.Fatal("corrupted cache not repaired")
		}
	}
	if compositeCacheMatches(dir, asset.ID, len(asset.Data)) {
		t.Fatal("directory accepted as cache")
	}
	if err := store.Delete(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if !compositeCacheMatches(path, asset.ID, len(asset.Data)) {
		t.Fatal("shared image removed before final reference")
	}
	if err := store.Delete(ctx, items[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("orphan cache remains: %v", err)
	}
}
