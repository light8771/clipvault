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

func TestStoreRejectsMixedHistoryRecordShapes(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	invalid := []ClipboardItem{
		{RecordType: domain.HistoryRecordContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "content", EventCode: domain.EventCaptureFailed},
		{RecordType: domain.HistoryRecordContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "content", CaptureStage: "decode"},
		{RecordType: domain.HistoryRecordEvent, EventCode: domain.EventClipboardBusy, Kind: domain.KindText},
		{RecordType: domain.HistoryRecordEvent, EventCode: domain.EventClipboardBusy, Starred: true},
		{RecordType: domain.HistoryRecordEvent, EventCode: domain.EventClipboardBusy, CaptureAttempts: -1},
		{RecordType: domain.HistoryRecordEvent, EventCode: domain.EventClipboardBusy, SourceAttribution: "guessed"},
	}
	for _, item := range invalid {
		if err := store.Save(ctx, item); err == nil {
			t.Fatalf("mixed record shape was accepted: %+v", item)
		}
	}
	items, total, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true, IncludeAllRecords: true})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("rejected records changed history: items=%d total=%d err=%v", len(items), total, err)
	}
}

func TestDatabaseRejectsMixedEventFormatShapes(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.db.ExecContext(ctx, `INSERT INTO clipboard_items
		(created_at, record_type, kind, format, text, byte_len, char_len, content_sha256, meta_json, event_formats)
		VALUES (?, 'content', 'text', 'CF_UNICODETEXT', 'x', 1, 1, ?, '{}', '[{"id":13,"name":"CF_UNICODETEXT"}]')`,
		time.Now().UnixMilli(), hashString("x"))
	if err == nil {
		t.Fatal("database accepted event formats on a content record")
	}

	_, err = store.db.ExecContext(ctx, `INSERT INTO clipboard_items
		(created_at, record_type, byte_len, char_len, meta_json, event_code, event_fingerprint, event_formats)
		VALUES (?, 'event', 0, 0, '{}', 'clipboard_empty', 'fingerprint', '{}')`, time.Now().UnixMilli())
	if err == nil {
		t.Fatal("database accepted a non-array event format value")
	}
}

func TestEventRecordsRejectContentOnlyMutations(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(ctx, ClipboardItem{
		RecordType: domain.HistoryRecordEvent, EventCode: domain.EventClipboardEmpty,
		SourceAttribution: "unknown",
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true, RecordType: domain.HistoryRecordEvent})
	if err != nil || len(items) != 1 {
		t.Fatalf("event search items=%d err=%v", len(items), err)
	}
	for _, mutate := range []func() error{
		func() error { return store.Star(ctx, items[0].ID, true) },
		func() error { return store.SetSensitive(ctx, items[0].ID, true) },
	} {
		if err := mutate(); !errors.Is(err, ErrItemNotFound) {
			t.Fatalf("event content-only mutation error=%v, want ErrItemNotFound", err)
		}
	}
}

func TestStoredItemJSONRejectsLegacyFields(t *testing.T) {
	for _, legacyField := range []string{"sha256", "contentAvailable", "unavailableReason", "isDuplicate"} {
		payload := `{"id":1,"createdAt":"2026-09-03T00:00:00Z","recordType":"event","text":null,"byteLen":0,"charLen":0,"contentSha256":null,"starred":false,"sensitive":false,"metaJson":"{}","event":{"code":"clipboard_empty","fingerprint":"empty","severity":"info","titleKey":"clipboard.event.clipboard_empty.title","descriptionKey":"clipboard.event.clipboard_empty.description","attempts":0,"occurrenceCount":1,"firstOccurredAt":"2026-09-03T00:00:00Z","lastOccurredAt":"2026-09-03T00:00:00Z"},"` + legacyField + `":true}`
		var item StoredItem
		if err := json.Unmarshal([]byte(payload), &item); err == nil {
			t.Fatalf("legacy field %q was accepted", legacyField)
		}
	}
}

func TestStoredItemJSONEnforcesDiscriminatedRecordShape(t *testing.T) {
	const createdAt = `"2026-09-03T00:00:00Z"`
	tests := []string{
		`{"id":1,"createdAt":` + createdAt + `,"recordType":"content","text":null,"byteLen":0,"charLen":0,"contentSha256":null,"starred":false,"sensitive":false}`,
		`{"id":1,"createdAt":` + createdAt + `,"recordType":"content","text":"","byteLen":0,"charLen":0,"contentSha256":null,"starred":false,"sensitive":false,"event":{}}`,
		`{"id":1,"createdAt":` + createdAt + `,"recordType":"event","text":"","byteLen":0,"charLen":0,"contentSha256":null,"starred":false,"sensitive":false,"event":{"code":"clipboard_empty","fingerprint":"empty"}}`,
		`{"id":1,"createdAt":` + createdAt + `,"recordType":"event","text":null,"byteLen":0,"charLen":0,"contentSha256":null,"starred":false,"sensitive":false}`,
		`{"id":1,"createdAt":` + createdAt + `,"recordType":"other","text":null,"byteLen":0,"charLen":0,"contentSha256":null,"starred":false,"sensitive":false}`,
	}
	for _, payload := range tests {
		var item StoredItem
		if err := json.Unmarshal([]byte(payload), &item); err == nil {
			t.Fatalf("mixed record shape was accepted: %s", payload)
		}
	}
}

func TestStoredItemJSONRequiresSourceAttribution(t *testing.T) {
	base := `{"id":1,"createdAt":"2026-09-03T00:00:00Z","recordType":"content","kind":"text","format":"CF_UNICODETEXT","text":"x","byteLen":1,"charLen":1,"contentSha256":"unused","metaJson":"{}","starred":false,"sensitive":false}`
	for name, payload := range map[string]string{
		"missing source":      base,
		"missing attribution": strings.TrimSuffix(base, "}") + `,"source":{"exeName":"editor.exe"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var item StoredItem
			if err := json.Unmarshal([]byte(payload), &item); err == nil {
				t.Fatalf("source-less record was accepted: %s", payload)
			}
		})
	}
}

func TestImportRejectsForgedEventFingerprint(t *testing.T) {
	formats := ClipboardFormatSet{IDs: []uint32{50001}, Names: []string{"PrivateFormat"}}
	item := StoredItem{
		ID: 1, CreatedAt: time.Now().UTC(), RecordType: RecordTypeEvent, MetaJSON: "{}",
		EventCode: domain.EventUnsupportedFormat, EventFingerprint: "forged",
		CaptureStage: "classify", CaptureAttempts: 1, EventFormats: formats.Info(),
		SourceExeName: "remote.exe", SourceAttribution: "foreground",
	}
	if err := validateStoredHistoryItem(item); err == nil {
		t.Fatal("import accepted a forged event fingerprint")
	}
	item.EventFingerprint = buildEventFingerprintFromInfo(item.EventCode, item.EventFormats,
		item.CaptureStage, item.ErrorCategory, item.SourceExeName)
	if err := validateStoredHistoryItem(item); err != nil {
		t.Fatalf("canonical event fingerprint rejected: %v", err)
	}
}

func TestImportRejectsEmbeddedNULContent(t *testing.T) {
	text := "visible\x00hidden"
	hash := hashString(text)
	item := StoredItem{
		ID: 1, CreatedAt: time.Now().UTC(), RecordType: RecordTypeContent,
		Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: text,
		ByteLen: len(text), CharLen: textRuneLen(text), ContentSHA256: &hash,
		MetaJSON: "{}", SourceAttribution: "unknown",
	}
	if err := validateStoredHistoryItem(item); err == nil {
		t.Fatal("import accepted content that Windows would truncate at an embedded NUL")
	}
}

// TestStoreSaveUsesEventCaptureTime 验证 created_at 使用事件发生时间
// （CapturedAtMS），而不是数据库写入时间。
func TestStoreSaveUsesEventCaptureTime(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	eventMS := time.Date(2026, 1, 15, 8, 0, 0, 123000000, time.UTC).UnixMilli()
	if err := store.Save(ctx, ClipboardItem{
		RecordType: domain.HistoryRecordContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "hello", CapturedAtMS: eventMS,
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("search items=%d err=%v", len(items), err)
	}
	if got := items[0].CreatedAt.UnixMilli(); got != eventMS {
		t.Fatalf("created_at = %d ms, want event time %d ms", got, eventMS)
	}
	if items[0].RecordType != domain.HistoryRecordContent || items[0].ContentSHA256 == nil {
		t.Fatalf("unexpected event state: %+v", items[0])
	}
}

// TestEventStateRoundtripAndDisplayFilters verifies that content is the default
// timeline and capture events require an explicit record-type filter.
func TestEventStateRoundtripAndDisplayFilters(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Now().UnixMilli()
	records := []ClipboardItem{
		{RecordType: domain.HistoryRecordContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "alpha", CapturedAtMS: base},
		{RecordType: domain.HistoryRecordContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "alpha", CapturedAtMS: base + 1},
		{RecordType: domain.HistoryRecordEvent, EventCode: domain.EventUnsupportedFormat,
			CaptureStage: "classify", CaptureAttempts: 1, CapturedAtMS: base + 2},
	}
	for _, record := range records {
		if err := store.Save(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	search := func(opts SearchOptions) int {
		opts.Limit = 10
		opts.IncludeSensitive = true
		items, _, err := store.Search(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		return len(items)
	}
	if n := search(SearchOptions{}); n != 2 {
		t.Fatalf("default content timeline = %d, want 2", n)
	}
	if n := search(SearchOptions{RecordType: domain.HistoryRecordEvent}); n != 1 {
		t.Fatalf("event record-type filter = %d, want 1", n)
	}
	items, _, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true, RecordType: domain.HistoryRecordEvent})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ContentSHA256 != nil || items[0].EventCode != domain.EventUnsupportedFormat {
		t.Fatalf("event record state drift: %+v", items)
	}
}

// TestCleanupPreviewMatchesDeleteAndPreservesStarred 验证 preview 与 delete
// 命中同一集合，PreserveStarred 一定保留收藏，范围边界为 [from, toExclusive)。
func TestCleanupPreviewMatchesDeleteAndPreservesStarred(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	ids := make([]int64, 0, 4)
	for i := 0; i < 4; i++ {
		if err := store.Save(ctx, ClipboardItem{
			RecordType: domain.HistoryRecordContent, Kind: "text", Format: "CF_UNICODETEXT", Text: string(rune('a' + i)),
			CapturedAtMS: day + int64(i)*1000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := store.Search(ctx, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	// 收藏范围内的一条
	if err := store.Star(ctx, ids[1], true); err != nil {
		t.Fatal(err)
	}

	opts := CleanupOptions{Mode: "range", FromMS: day + 1000, ToExclusiveMS: day + 3000, PreserveStarred: true}
	preview, err := store.PreviewCleanup(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if preview != 1 {
		t.Fatalf("preview = %d, want 1 (starred preserved)", preview)
	}
	deleted, err := store.Cleanup(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != preview {
		t.Fatalf("deleted = %d, preview = %d: preview/delete 条件不一致", deleted, preview)
	}
	remaining, _, err := store.Search(ctx, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 3 {
		t.Fatalf("remaining = %d, want 3", len(remaining))
	}
	for _, it := range remaining {
		if it.CreatedAt.UnixMilli() >= day+1000 && it.CreatedAt.UnixMilli() < day+3000 && !it.Starred {
			t.Fatalf("non-starred in-range item survived cleanup: %+v", it)
		}
	}

	// 秒级边界：结束秒 +1000ms 为开区间上界
	boundaryOpts := CleanupOptions{Mode: "range", FromMS: day, ToExclusiveMS: day + 1000}
	preview, err = store.PreviewCleanup(ctx, boundaryOpts)
	if err != nil {
		t.Fatal(err)
	}
	if preview != 1 {
		t.Fatalf("boundary preview = %d, want 1 (only first second)", preview)
	}

	// all + preserveStarred
	allOpts := CleanupOptions{Mode: "all", PreserveStarred: true}
	preview, err = store.PreviewCleanup(ctx, allOpts)
	if err != nil {
		t.Fatal(err)
	}
	if preview != 2 {
		t.Fatalf("all preview = %d, want 2", preview)
	}
	if _, err := store.Cleanup(ctx, allOpts); err != nil {
		t.Fatal(err)
	}
	remaining, _, err = store.Search(ctx, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || !remaining[0].Starred {
		t.Fatalf("after cleanup-all only the starred item may survive: %+v", remaining)
	}
}

// TestCleanupValidation 校验清理条件的合法性判定。
func TestCleanupValidation(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	invalid := []CleanupOptions{
		{Mode: "range", FromMS: 0, ToExclusiveMS: 1000},
		{Mode: "range", FromMS: 2000, ToExclusiveMS: 1000},
		{Mode: "bogus"},
		{Mode: "all", FromMS: 1000},
	}
	for _, opts := range invalid {
		if _, err := store.PreviewCleanup(ctx, opts); err == nil {
			t.Fatalf("invalid cleanup options accepted: %+v", opts)
		}
		if _, err := store.Cleanup(ctx, opts); err == nil {
			t.Fatalf("invalid cleanup options executed: %+v", opts)
		}
	}
}

// TestSetSensitiveManualMark 验证手动标记/取消敏感。
func TestSetSensitiveManualMark(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(ctx, ClipboardItem{RecordType: domain.HistoryRecordContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "普通内容"}); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.Search(ctx, SearchOptions{Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	id := items[0].ID
	if items[0].Sensitive {
		t.Fatal("new item should not be sensitive")
	}
	if err := store.SetSensitive(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	// 标记后默认搜索（不含敏感）不可见
	items, _, err = store.Search(ctx, SearchOptions{Limit: 10})
	if err != nil || len(items) != 0 {
		t.Fatalf("after mark, default search items=%d err=%v", len(items), err)
	}
	// 敏感过滤可见
	items, _, err = store.Search(ctx, SearchOptions{Limit: 10, SensitiveOnly: true, IncludeSensitive: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("sensitive search items=%d err=%v", len(items), err)
	}
	if err := store.SetSensitive(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	items, _, err = store.Search(ctx, SearchOptions{Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("after unmark, default search items=%d err=%v", len(items), err)
	}
	if err := store.SetSensitive(ctx, 99999, true); err == nil {
		t.Fatal("marking a missing item should fail")
	}
}

// TestParsePopupKeyBinding 验证弹窗动作快捷键解析与冲突校验。
func TestParsePopupKeyBinding(t *testing.T) {
	valid := map[string]int{
		"Enter":                0,
		"Ctrl+Enter":           popupModCtrl,
		"Shift+Enter":          popupModShift,
		"Alt+Enter":            popupModAlt,
		"Ctrl+Shift+Enter":     popupModCtrl | popupModShift,
		"ctrl+alt+enter":       popupModCtrl | popupModAlt,
		"Shift+Ctrl+Enter":     popupModShift | popupModCtrl,
		"Ctrl+Shift+Alt+Enter": popupModCtrl | popupModShift | popupModAlt,
	}
	for input, want := range valid {
		mods, ok := parsePopupKeyBinding(input)
		if !ok || mods != want {
			t.Errorf("parsePopupKeyBinding(%q) = %d,%v; want %d,true", input, mods, ok, want)
		}
	}
	for _, input := range []string{"", "Enter+Enter", "Ctrl+Ctrl+Enter", "A", "Ctrl+A", "Ctrl+", "Tab"} {
		if _, ok := parsePopupKeyBinding(input); ok {
			t.Errorf("parsePopupKeyBinding(%q) should fail", input)
		}
	}
	cfg := DefaultConfig().Popup
	if err := validatePopupKeys(cfg); err != nil {
		t.Fatalf("default popup keys invalid: %v", err)
	}
	cfg.KeyOpen = "Ctrl+Enter"
	cfg.KeyPaste = "Ctrl+Enter"
	if err := validatePopupKeys(cfg); err == nil {
		t.Fatal("conflicting popup keys accepted")
	}
}
