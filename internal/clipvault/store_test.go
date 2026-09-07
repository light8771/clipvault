package clipvault

import (
	"context"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestStoreSaveAndSearch(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(context.Background(), ClipboardItem{
		RecordType: RecordTypeContent,
		Kind:       "text",
		Format:     "CF_UNICODETEXT",
		Text:       "alpha project note",
		MetaJSON:   "{}",
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

func TestStoreRejectsInvalidClipboardStates(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	invalid := []ClipboardItem{
		{Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "missing discriminator"},
		{RecordType: RecordTypeContent, Format: domain.FormatUnicodeText, Text: "missing kind"},
		{RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, EventCode: domain.EventCaptureFailed},
		{RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "visible\x00hidden"},
		{RecordType: RecordTypeContent, Kind: domain.KindFiles, Format: domain.FormatFileDrop, Text: "C:\\visible.txt\x00D:\\hidden.txt"},
		{RecordType: RecordTypeContent, Kind: domain.KindImage, Format: domain.FormatImageFile, Text: "images/missing-length.png", ContentSHA256: hashString("png")},
		{RecordType: RecordTypeContent, Kind: domain.KindImage, Format: domain.FormatImageFile, Text: "images/char-length.png", ByteLen: 3, CharLen: 1, ContentSHA256: hashString("png")},
		{RecordType: RecordTypeContent, Kind: domain.KindImage, Format: domain.FormatImageFile, Text: "images/../outside.png", ByteLen: 3, ContentSHA256: hashString("png")},
		{RecordType: RecordTypeEvent},
		{RecordType: RecordTypeEvent, EventCode: domain.EventCaptureFailed, Text: "forbidden"},
	}
	for _, item := range invalid {
		if err := store.Save(context.Background(), item); err == nil {
			t.Fatalf("invalid clipboard state was accepted: %+v", item)
		}
	}
}

func TestStoreMarksAndFiltersConsecutiveContentDuplicates(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, item := range []ClipboardItem{
		{RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "same"},
		{RecordType: RecordTypeEvent, EventCode: domain.EventClipboardEmpty},
		{RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "same"},
		{RecordType: RecordTypeContent, Kind: domain.KindFiles, Format: domain.FormatFileDrop, Text: "same"},
		{RecordType: RecordTypeContent, Kind: domain.KindFiles, Format: domain.FormatFileDrop, Text: "same"},
	} {
		if err := store.Save(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	items, total, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(items) != 4 {
		t.Fatalf("content results = %d/%d, want 4/4", len(items), total)
	}
	want := []bool{true, false, true, false}
	for index := range want {
		if items[index].ConsecutiveDuplicate != want[index] {
			t.Fatalf("item %d consecutiveDuplicate = %v, want %v", items[index].ID, items[index].ConsecutiveDuplicate, want[index])
		}
	}

	filtered, filteredTotal, err := store.Search(ctx, SearchOptions{
		Limit: 10, IncludeSensitive: true, HideConsecutiveDuplicates: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if filteredTotal != 2 || len(filtered) != 2 {
		t.Fatalf("filtered content results = %d/%d, want 2/2", len(filtered), filteredTotal)
	}
}

func TestDeleteRecomputesNewContentBoundary(t *testing.T) {
	for _, test := range []struct {
		name          string
		texts         []string
		deleteNewestN int
		wantNewestDup bool
	}{
		{name: "duplicate becomes unique", texts: []string{"A", "B", "B"}, deleteNewestN: 1, wantNewestDup: false},
		{name: "unique becomes duplicate", texts: []string{"A", "B", "A"}, deleteNewestN: 1, wantNewestDup: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			for _, text := range test.texts {
				if err := store.Save(ctx, ClipboardItem{
					RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: text,
				}); err != nil {
					t.Fatal(err)
				}
			}
			items, _, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Delete(ctx, items[test.deleteNewestN].ID); err != nil {
				t.Fatal(err)
			}
			items, _, err = store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 2 || items[0].ConsecutiveDuplicate != test.wantNewestDup {
				t.Fatalf("items after delete = %+v; newest duplicate want %v", items, test.wantNewestDup)
			}
		})
	}
}

func TestCleanupRecomputesContentBoundaries(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for index, text := range []string{"A", "B", "A"} {
		if err := store.Save(ctx, ClipboardItem{
			RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText,
			Text: text, CapturedAtMS: int64(index+1) * 1000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := store.Cleanup(ctx, CleanupOptions{Mode: "range", FromMS: 2000, ToExclusiveMS: 3000})
	if err != nil || deleted != 1 {
		t.Fatalf("Cleanup deleted=%d err=%v, want 1", deleted, err)
	}
	items, _, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !items[0].ConsecutiveDuplicate {
		t.Fatalf("cleanup did not derive the new A/A boundary: %+v", items)
	}
}

func TestCurrentSchemaUsesUnixMillisecondsAndIdentity(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, column := range []struct {
		table string
		name  string
	}{
		{table: "clipboard_items", name: "created_at"},
		{table: "groups", name: "created_at"},
		{table: "backup_history", name: "created_at"},
	} {
		var dataType string
		if err := store.db.QueryRow(`SELECT data_type FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`, column.table, column.name).Scan(&dataType); err != nil {
			t.Fatalf("query %s.%s type: %v", column.table, column.name, err)
		}
		if dataType != "BIGINT" {
			t.Errorf("%s.%s type=%q", column.table, column.name, dataType)
		}
	}
	for _, column := range []string{"sha256", "content_available", "is_duplicate", "unavailable_reason", "source_method"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'clipboard_items' AND column_name = ?`, column).Scan(&count); err != nil {
			t.Fatalf("query clipboard_items.%s: %v", column, err)
		}
		if count != 0 {
			t.Errorf("legacy column clipboard_items.%s still exists", column)
		}
	}
	var identity string
	if err := store.db.QueryRow(`SELECT json_extract_string(value, '$') FROM settings WHERE key = 'schema.identity'`).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if identity != storeSchemaIdentity {
		t.Fatalf("schema identity=%q, want %q", identity, storeSchemaIdentity)
	}
}

func TestSchemaBoundsCaptureSequenceToWin32Range(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeEvent, EventCode: domain.EventClipboardEmpty}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE clipboard_items SET capture_sequence = 4294967296 WHERE record_type = 'event'`); err == nil {
		t.Fatal("schema accepted a capture sequence that cannot fit the Win32 uint32 source")
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
			RecordType: RecordTypeContent,
			Kind:       "files",
			Format:     "CF_HDROP",
			Text:       "C:\\work\\release\\notes.txt",
			MetaJSON:   `{"source":"Explorer","label":"Release notes"}`,
		},
		{
			RecordType: RecordTypeContent,
			Kind:       "image",
			Format:     "CF_HDROP",
			Text:       "",
			MetaJSON:   `{"application":"Screenshot Tool"}`,
			Sensitive:  true,
		},
		{
			RecordType: RecordTypeContent,
			Kind:       "text",
			Format:     "CF_UNICODETEXT",
			Text:       "发布计划 🚀",
			MetaJSON:   "{}",
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
		{query: hashString(items[0].Text)[:8], want: "files"},
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

// 托管图片（CLIPVAULT_IMAGE_FILE）的 text/format/meta_json 均为内部存储细节，
// 关键词搜索不得因此命中图片记录；图片仍可按类型（kind）与哈希检索。
func TestStoreSearchDoesNotMatchManagedImageInternals(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	imageText := "images/2026-08-19_abc123.png"
	imageHash := hashString("original PNG bytes")
	if err := store.Save(context.Background(), ClipboardItem{
		RecordType:    RecordTypeContent,
		Kind:          "image",
		Format:        "CLIPVAULT_IMAGE_FILE",
		Text:          imageText,
		ByteLen:       len("original PNG bytes"),
		ContentSHA256: imageHash,
		MetaJSON:      `{"imagePath":"C:\\Users\\me\\AppData\\Roaming\\ClipVault\\images\\2026-08-19_abc123.png","width":800,"height":600}`,
	}); err != nil {
		t.Fatal(err)
	}
	stored, _, err := store.Search(context.Background(), SearchOptions{Kind: domain.KindImage, Limit: 1, IncludeSensitive: true})
	if err != nil || len(stored) != 1 || stored[0].ByteLen != len("original PNG bytes") || stored[0].CharLen != 0 {
		t.Fatalf("managed image lengths were not preserved: items=%+v err=%v", stored, err)
	}

	// 关键词命中伪格式名 / 内部路径 / 文件名时不得返回图片
	for _, query := range []string{"clipvault", "imagePath", "abc123.png"} {
		items, total, err := store.Search(context.Background(), SearchOptions{Query: query, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 || total != 0 {
			t.Fatalf("query %q matched managed image internals: items=%+v total=%d", query, items, total)
		}
	}

	// 按类型与哈希仍可检索到图片
	byKind, _, err := store.Search(context.Background(), SearchOptions{Query: "image", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(byKind) != 1 {
		t.Fatalf("kind search should still find the image: %+v", byKind)
	}
	byHash, _, err := store.Search(context.Background(), SearchOptions{Query: imageHash[:12], Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(byHash) != 1 {
		t.Fatalf("hash search should still find the image: %+v", byHash)
	}
}

func TestItemGroupReferentialIntegrity(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "record", MetaJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	items, err := exportAll(ctx, store.db)
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
	export := exportAllForTest(t, store)
	if len(export.ItemGroups) != 0 {
		t.Fatalf("delete left item-group links: %#v", export.ItemGroups)
	}

	if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "second", MetaJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	items, err = exportAll(ctx, store.db)
	if err != nil || len(items) != 1 {
		t.Fatalf("export items = %#v, err = %v", items, err)
	}
	if err := store.AddItemToGroup(ctx, items[0].ID, group.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cleanup(ctx, CleanupOptions{Mode: "all"}); err != nil {
		t.Fatal(err)
	}
	export = exportAllForTest(t, store)
	if len(export.Items) != 0 || len(export.ItemGroups) != 0 {
		t.Fatalf("clear left exported data: %#v", export)
	}
}
