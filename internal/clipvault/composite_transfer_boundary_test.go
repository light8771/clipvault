package clipvault

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCompositeLargeTransferAndKindOperators(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := DefaultConfig()
	cfg.MaxTextChars = 0
	value := strings.Repeat("表格😀value", 6000)
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"HTML Format": []byte("<table><tr><td>" + value + "</td></tr></table>")}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		op, raw string
		count   int
	}{
		{"eq", `"table"`, 1}, {"eq", `"composite"`, 1}, {"eq", `"image"`, 0},
		{"neq", `"table"`, 0}, {"neq", `"image"`, 1},
		{"in", `["text","table"]`, 1}, {"contains", `"abl"`, 1},
	} {
		condition, args, apiErr := dataAPIFilterFields["kind"].compileOp("kind", tc.op, json.RawMessage(tc.raw))
		if apiErr != nil {
			t.Fatal(apiErr)
		}
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM clipboard_items WHERE "+condition, args...).Scan(&count); err != nil || count != tc.count {
			t.Fatalf("%s %s: count=%d err=%v", tc.op, tc.raw, count, err)
		}
	}
	original := exportAllForTest(t, store)
	jsonData, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	xlsx, err := store.ExportXLSX(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, importData := range map[string]func(*Store) error{
		"JSON": func(s *Store) error { return s.ImportFullJSON(ctx, jsonData) },
		"XLSX": func(s *Store) error { return s.ImportXLSX(ctx, xlsx) },
	} {
		t.Run(name, func(t *testing.T) {
			target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if err := importData(target); err != nil {
				t.Fatal(err)
			}
			items, err := exportAll(ctx, target.db)
			if err != nil || len(items) != 1 {
				t.Fatalf("roundtrip count=%d err=%v", len(items), err)
			}
			if items[0].Text != value || compositeJSONString(items[0].Content) != compositeJSONString(snapshot.Content) {
				t.Fatal("large Unicode package changed in transfer")
			}
		})
	}
}
