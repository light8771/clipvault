package clipvault

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestSourceInfoRoundtrip 验证来源字段经 Save → Search/GetByID 全链路不丢失。
func TestSourceInfoRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	item := ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: "source test", ByteLen: 11, CharLen: 11,
		SHA256: "src001", MetaJSON: "{}",
		SourceExeName:     "notepad.exe",
		SourceExePath:     `C:\Windows\System32\notepad.exe`,
		SourceWindowTitle: "无标题 - 记事本",
		SourceMethod:      "foreground",
	}
	if err := store.Save(ctx, item); err != nil {
		t.Fatalf("Save: %v", err)
	}

	items, total, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected 1 item, got total=%d len=%d", total, len(items))
	}
	got := items[0]
	if got.SourceExeName != item.SourceExeName || got.SourceExePath != item.SourceExePath ||
		got.SourceWindowTitle != item.SourceWindowTitle || got.SourceMethod != item.SourceMethod {
		t.Fatalf("Search source fields mismatch: %#v", got)
	}

	byID, err := store.GetByID(ctx, got.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID.SourceExeName != item.SourceExeName || byID.SourceMethod != item.SourceMethod {
		t.Fatalf("GetByID source fields mismatch: %#v", byID)
	}
}

// TestExportCarriesSourceFields 验证 JSON 导出携带来源字段（cvexport 是同一份 JSON 加密）。
func TestExportCarriesSourceFields(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Save(ctx, ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: "export source", ByteLen: 13, CharLen: 13,
		SHA256: "exp001", MetaJSON: "{}",
		SourceExeName: "msedge.exe", SourceMethod: "foreground",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	export, err := store.ExportFullForJSON(ctx)
	if err != nil {
		t.Fatalf("ExportFullForJSON: %v", err)
	}
	if len(export.Items) != 1 {
		t.Fatalf("expected 1 exported item, got %d", len(export.Items))
	}
	if export.Items[0].SourceExeName != "msedge.exe" || export.Items[0].SourceMethod != "foreground" {
		t.Fatalf("exported source fields mismatch: %#v", export.Items[0])
	}

	data, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	if !strings.Contains(string(data), `"sourceExeName":"msedge.exe"`) {
		t.Fatalf("export JSON missing sourceExeName: %s", data)
	}
}

// TestDefaultConfigRecordsSourceInfo 验证默认开启来源记录。
func TestDefaultConfigRecordsSourceInfo(t *testing.T) {
	if !DefaultConfig().RecordSourceInfo {
		t.Fatal("DefaultConfig().RecordSourceInfo should be true")
	}
}

// TestDefaultConfigSourceApp 验证默认开启来源应用展示，且别名/白名单为零值语义。
func TestDefaultConfigSourceApp(t *testing.T) {
	src := DefaultConfig().Source
	if !src.ShowExe {
		t.Fatal("DefaultConfig().Source.ShowExe should be true")
	}
	if src.ExeAliases == nil {
		t.Fatal("DefaultConfig().Source.ExeAliases should be non-nil empty map")
	}
	if src.ExeWhitelist != nil {
		t.Fatal("DefaultConfig().Source.ExeWhitelist should be nil (display all)")
	}
}

// TestSourceExeStats 验证按来源应用聚合计数，结果按 count 降序、名称升序，空来源被忽略。
func TestSourceExeStats(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "a", ByteLen: 1, CharLen: 1, SHA256: "s1", MetaJSON: "{}", SourceExeName: "notepad.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "b", ByteLen: 1, CharLen: 1, SHA256: "s2", MetaJSON: "{}", SourceExeName: "notepad.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "c", ByteLen: 1, CharLen: 1, SHA256: "s3", MetaJSON: "{}", SourceExeName: "msedge.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "d", ByteLen: 1, CharLen: 1, SHA256: "s4", MetaJSON: "{}", SourceExeName: ""}))

	stats, err := store.SourceExeStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 distinct non-empty exe names, got %d: %#v", len(stats), stats)
	}
	if stats[0].Name != "notepad.exe" || stats[0].Count != 2 {
		t.Fatalf("expected first = notepad.exe count 2, got %#v", stats[0])
	}
	if stats[1].Name != "msedge.exe" || stats[1].Count != 1 {
		t.Fatalf("expected second = msedge.exe count 1, got %#v", stats[1])
	}
}

// TestSearchSourceExeFilter 验证下拉多选精确过滤（原始名 IN）。
func TestSearchSourceExeFilter(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "n1", ByteLen: 2, CharLen: 2, SHA256: "n1", MetaJSON: "{}", SourceExeName: "notepad.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "n2", ByteLen: 2, CharLen: 2, SHA256: "n2", MetaJSON: "{}", SourceExeName: "notepad.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "e1", ByteLen: 2, CharLen: 2, SHA256: "e1", MetaJSON: "{}", SourceExeName: "msedge.exe"}))

	only, _, err := store.Search(ctx, SearchOptions{Limit: 50, SourceExeNames: []string{"notepad.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 2 {
		t.Fatalf("expected 2 notepad items, got %d", len(only))
	}
	for _, it := range only {
		if it.SourceExeName != "notepad.exe" {
			t.Fatalf("unexpected source %q", it.SourceExeName)
		}
	}

	both, _, err := store.Search(ctx, SearchOptions{Limit: 50, SourceExeNames: []string{"notepad.exe", "msedge.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 3 {
		t.Fatalf("expected 3 items for multi-select, got %d", len(both))
	}
}

// TestSearchAliasMatch 验证主搜索框按别名子串匹配展开为原始名精确匹配，且原始名仍可搜到。
func TestSearchAliasMatch(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "edge content", ByteLen: 12, CharLen: 12, SHA256: "m1", MetaJSON: "{}", SourceExeName: "msedge.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "notepad content", ByteLen: 15, CharLen: 15, SHA256: "n1", MetaJSON: "{}", SourceExeName: "notepad.exe"}))

	aliases := map[string]string{"msedge.exe": "Edge浏览器"}

	// 按别名 "Edge" 搜索应返回 msedge 来源记录
	byAlias, _, err := store.Search(ctx, SearchOptions{Limit: 50, Query: "Edge", ExeAliases: aliases})
	if err != nil {
		t.Fatal(err)
	}
	if len(byAlias) != 1 || byAlias[0].SourceExeName != "msedge.exe" {
		t.Fatalf("alias search expected 1 msedge item, got %#v", byAlias)
	}

	// 主搜索关键词不再模糊匹配来源列（#4）：自由文本仅匹配文本/格式等字段，
	// "msedge" 不在文本 "edge content" 中，故按原始名作主搜索关键词应返回 0 条；
	// 若要按来源筛选，须使用显式 source_exe 过滤参数（而非关键词）。
	byRaw, _, err := store.Search(ctx, SearchOptions{Limit: 50, Query: "msedge", ExeAliases: aliases})
	if err != nil {
		t.Fatal(err)
	}
	if len(byRaw) != 0 {
		t.Fatalf("raw-name free-text must NOT match source_exe_name after #4, got %#v", byRaw)
	}
}

// TestSourceExeStatsEmpty 验证无来源记录（或来源全为空）时返回非 nil 空切片，不报错。
func TestSourceExeStatsEmpty(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	// 1) 根本没有记录
	stats, err := store.SourceExeStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats == nil {
		t.Fatal("SourceExeStats returned nil; expected non-nil empty slice")
	}
	if len(stats) != 0 {
		t.Fatalf("expected 0 stats for empty store, got %d: %#v", len(stats), stats)
	}

	// 2) 有记录但来源全为空串，仍应排除，返回非 nil 空切片
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "x", ByteLen: 1, CharLen: 1, SHA256: "z1", MetaJSON: "{}", SourceExeName: ""}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "y", ByteLen: 1, CharLen: 1, SHA256: "z2", MetaJSON: "{}", SourceExeName: ""}))
	stats2, err := store.SourceExeStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats2 == nil || len(stats2) != 0 {
		t.Fatalf("expected non-nil empty stats when all sources empty, got %#v", stats2)
	}
}

// TestSearchSourceExeWithOtherFilters 验证 SourceExeNames 与 Kind/StarredOnly 的 AND 叠加。
func TestSearchSourceExeWithOtherFilters(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "A", ByteLen: 1, CharLen: 1, SHA256: "x1", MetaJSON: "{}", SourceExeName: "msedge.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "B", ByteLen: 1, CharLen: 1, SHA256: "x2", MetaJSON: "{}", SourceExeName: "msedge.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "C", ByteLen: 1, CharLen: 1, SHA256: "x3", MetaJSON: "{}", SourceExeName: "notepad.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "image", Format: "CF_BITMAP", Text: "D", ByteLen: 1, CharLen: 1, SHA256: "x4", MetaJSON: "{}", SourceExeName: "msedge.exe"}))

	// 回填 starred：A、C、D 为收藏
	all, _, err := store.Search(ctx, SearchOptions{Limit: 100, IncludeSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range all {
		switch it.Text {
		case "A", "C", "D":
			must(store.Star(ctx, it.ID, true))
		}
	}

	// 1) SourceExeNames + StarredOnly → msedge 且 starred = A, D
	r1, _, err := store.Search(ctx, SearchOptions{Limit: 50, SourceExeNames: []string{"msedge.exe"}, StarredOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r1) != 2 {
		t.Fatalf("expected 2 (A,D), got %d: %#v", len(r1), r1)
	}
	for _, it := range r1 {
		if it.SourceExeName != "msedge.exe" || !it.Starred {
			t.Fatalf("unexpected item %#v", it)
		}
	}

	// 2) SourceExeNames + Kind=text → msedge 且 text 类 = A, B
	r2, _, err := store.Search(ctx, SearchOptions{Limit: 50, SourceExeNames: []string{"msedge.exe"}, Kind: "text"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r2) != 2 {
		t.Fatalf("expected 2 (A,B), got %d: %#v", len(r2), r2)
	}

	// 3) SourceExeNames + StarredOnly + Kind=text → 仅 A
	r3, _, err := store.Search(ctx, SearchOptions{Limit: 50, SourceExeNames: []string{"msedge.exe"}, StarredOnly: true, Kind: "text"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r3) != 1 || r3[0].Text != "A" {
		t.Fatalf("expected 1 (A), got %#v", r3)
	}
}

// TestSearchAliasCaseInsensitive 验证别名匹配大小写：别名值大小写不敏感、键大小写敏感。
// 注意：主搜索关键词不再模糊匹配来源列（#4 调整后，source_exe_name 仅由显式 source_exe
// 过滤或别名精确展开命中），因此本测试用不在真实名/文本中出现的别名词（"浏览器"/"MyEdge"）
// 来隔离别名展开路径，确保走的是别名精确匹配而非来源列 ILIKE。
func TestSearchAliasCaseInsensitive(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "hello world", ByteLen: 11, CharLen: 11, SHA256: "m1", MetaJSON: "{}", SourceExeName: "msedge.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "hello world", ByteLen: 11, CharLen: 11, SHA256: "n1", MetaJSON: "{}", SourceExeName: "notepad.exe"}))

	// 1) 自定义别名子串（不在真实名/文本中）应命中：搜索 "浏览器" → msedge
	r1, _, err := store.Search(ctx, SearchOptions{Limit: 50, Query: "浏览器", ExeAliases: map[string]string{"msedge.exe": "Edge浏览器"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r1) != 1 || r1[0].SourceExeName != "msedge.exe" {
		t.Fatalf("alias substring search expected 1 msedge, got %#v", r1)
	}

	// 2) 别名值大小写不敏感：大写 "EDGE浏览器" 仍应命中
	r2, _, err := store.Search(ctx, SearchOptions{Limit: 50, Query: "EDGE浏览器", ExeAliases: map[string]string{"msedge.exe": "Edge浏览器"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r2) != 1 || r2[0].SourceExeName != "msedge.exe" {
		t.Fatalf("uppercase alias search expected 1 msedge, got %#v", r2)
	}

	// 3) 别名键大小写敏感："MsEdge.exe" 键不应匹配真实来源 "msedge.exe"。
	//    查询 "MyEdge" 命中别名值（触发展开），但展开用的键是 "MsEdge.exe"，
	//    与真实来源 "msedge.exe" 大小写不同，精确等号不匹配 → 0 条。
	r3, _, err := store.Search(ctx, SearchOptions{Limit: 50, Query: "MyEdge", ExeAliases: map[string]string{"MsEdge.exe": "MyEdge浏览器"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r3) != 0 {
		t.Fatalf("case-mismatched alias key should NOT match, got %#v", r3)
	}
}

// TestSearchMultiTermAnd 验证多词查询为 AND 语义（每个词各自匹配，结果需同时满足）。
func TestSearchMultiTermAnd(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "Edge 笔记 meeting", ByteLen: 16, CharLen: 16, SHA256: "a1", MetaJSON: "{}", SourceExeName: "msedge.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "笔记本 notes", ByteLen: 12, CharLen: 12, SHA256: "b1", MetaJSON: "{}", SourceExeName: "notepad.exe"}))
	must(store.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "Edge browser", ByteLen: 12, CharLen: 12, SHA256: "c1", MetaJSON: "{}", SourceExeName: "msedge.exe"}))

	aliases := map[string]string{"msedge.exe": "Edge浏览器"}
	// "Edge 笔记" 两词 AND：需同时满足 → 仅 item A
	r, _, err := store.Search(ctx, SearchOptions{Limit: 50, Query: "Edge 笔记", ExeAliases: aliases})
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 1 || r[0].Text != "Edge 笔记 meeting" {
		t.Fatalf("multi-term AND expected 1 (item A), got %#v", r)
	}
}

// TestConfigNormalize 验证 SourceAppConfig.normalize() 的零值收敛与 ShowExe 保留。
func TestConfigNormalize(t *testing.T) {
	// nil ExeAliases → 空 map；空 ExeWhitelist → nil
	src := SourceAppConfig{ExeWhitelist: []string{}, ExeAliases: nil}
	src.normalize()
	if src.ExeWhitelist != nil {
		t.Fatalf("expected nil whitelist, got %#v", src.ExeWhitelist)
	}
	if src.ExeAliases == nil {
		t.Fatal("expected non-nil empty alias map")
	}
	if len(src.ExeAliases) != 0 {
		t.Fatalf("expected empty alias map, got %#v", src.ExeAliases)
	}

	// ShowExe=false、非空白名单与非空别名应原样保留
	src2 := SourceAppConfig{ShowExe: false, ExeWhitelist: []string{"a.exe"}, ExeAliases: map[string]string{"a.exe": "A"}}
	src2.normalize()
	if src2.ShowExe != false {
		t.Fatal("ShowExe=false must be preserved")
	}
	if src2.ExeWhitelist == nil || len(src2.ExeWhitelist) != 1 {
		t.Fatalf("non-empty whitelist must be preserved, got %#v", src2.ExeWhitelist)
	}
	if src2.ExeAliases == nil || src2.ExeAliases["a.exe"] != "A" {
		t.Fatalf("non-nil alias map must be preserved, got %#v", src2.ExeAliases)
	}

	// 默认配置 ShowExe=true
	if !DefaultConfig().Source.ShowExe {
		t.Fatal("DefaultConfig().Source.ShowExe should be true")
	}
}
