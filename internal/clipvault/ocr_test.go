package clipvault

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

func TestOCRSearchEditsCacheAndTransfers(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := compositeFixture(t, `C:\temp\ocr.png`)
	if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content, MetaJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	items, err := exportAll(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	id := items[0].ID
	edit := "修订发票甲"
	record := domain.ImageOCR{ImageID: "asset:test", ImageHash: strings.Repeat("a", 64), CacheKey: strings.Repeat("b", 64), Original: "原始发票乙", Edited: &edit, Model: "test-model", RecognizedAt: time.Now().UnixMilli()}
	if err := store.updateImageOCR(ctx, id, func([]domain.ImageOCR) ([]domain.ImageOCR, error) { return []domain.ImageOCR{record}, nil }); err != nil {
		t.Fatal(err)
	}
	for term, want := range map[string]int{"修订发票甲": 1, "原始发票乙": 0} {
		found, total, err := store.Search(ctx, SearchOptions{Query: term, Limit: 10})
		if err != nil || total != want {
			t.Fatalf("search %s: %d %v", term, total, err)
		}
		if len(found) > 0 && len(found[0].OCR) > 0 {
			t.Fatal("list exposed OCR body")
		}
	}
	cached, hit, err := store.cachedImageOCR(ctx, record.CacheKey)
	if err != nil || !hit || cached.Edited != nil || cached.Original != record.Original {
		t.Fatalf("cache: %+v %v %v", cached, hit, err)
	}
	export := exportAllForTest(t, store)
	raw, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	xlsx, err := store.ExportXLSX(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func(*Store) error{"json": func(s *Store) error { return s.ImportFullJSON(ctx, raw) }, "xlsx": func(s *Store) error { return s.ImportXLSX(ctx, xlsx) }, "materialized": func(s *Store) error { return s.importExportData(ctx, *export) }} {
		t.Run(name, func(t *testing.T) {
			target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if err := run(target); err != nil {
				t.Fatal(err)
			}
			items, err := exportAll(ctx, target.db)
			if err != nil || len(items) != 1 || len(items[0].OCR) != 1 {
				t.Fatalf("roundtrip: %v", err)
			}
			if items[0].OCR[0].Text() != edit || items[0].OCR[0].Original != record.Original {
				t.Fatal("lost OCR edits")
			}
		})
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM clipboard_items WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	err = store.updateImageOCR(ctx, id, func(records []domain.ImageOCR) ([]domain.ImageOCR, error) { return append(records, record), nil })
	if !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("deleted record resurrected: %v", err)
	}
}
