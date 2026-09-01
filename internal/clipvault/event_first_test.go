package clipvault

import (
	"context"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

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
		Kind: "text", Format: "CF_UNICODETEXT", Text: "hello", CapturedAtMS: eventMS,
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
	if !items[0].ContentAvailable || items[0].IsDuplicate || items[0].UnavailableReason != "" {
		t.Fatalf("unexpected event state: %+v", items[0])
	}
}

// TestEventStateRoundtripAndDisplayFilters 验证重复/无内容记录的落库与显示过滤。
func TestEventStateRoundtripAndDisplayFilters(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Now().UnixMilli()
	records := []ClipboardItem{
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "alpha", CapturedAtMS: base},
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "alpha", CapturedAtMS: base + 1, IsDuplicate: true},
		{Kind: domain.KindOther, Format: domain.FormatEventOnly, CapturedAtMS: base + 2,
			ContentAvailable: false, UnavailableReason: domain.ReasonUnsupportedFormat},
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
	if n := search(SearchOptions{}); n != 3 {
		t.Fatalf("unfiltered = %d, want 3", n)
	}
	if n := search(SearchOptions{HideDuplicates: true}); n != 2 {
		t.Fatalf("HideDuplicates = %d, want 2", n)
	}
	if n := search(SearchOptions{HideUnavailable: true}); n != 2 {
		t.Fatalf("HideUnavailable = %d, want 2", n)
	}
	if n := search(SearchOptions{HideDuplicates: true, HideUnavailable: true}); n != 1 {
		t.Fatalf("both filters = %d, want 1", n)
	}
	if n := search(SearchOptions{Kind: domain.KindOther}); n != 1 {
		t.Fatalf("event kind filter = %d, want 1", n)
	}
	// 事件状态字段随查询返回
	items, _, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	var eventOnly *StoredItem
	for i := range items {
		if items[i].Kind == domain.KindOther {
			eventOnly = &items[i]
		}
	}
	if eventOnly == nil || eventOnly.ContentAvailable || eventOnly.UnavailableReason != domain.ReasonUnsupportedFormat {
		t.Fatalf("event-only record state drift: %+v", eventOnly)
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
			Kind: "text", Format: "CF_UNICODETEXT", Text: string(rune('a' + i)),
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
	if err := store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "普通内容"}); err != nil {
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
	items, _, err = store.Search(ctx, SearchOptions{Limit: 10, SensitiveOnly: true})
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
