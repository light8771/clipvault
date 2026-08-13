package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestStreamExportMatchesFullExport 验证流式导出(full scope)的输出与
// ExportFullForJSON 序列化结果在解析后等价（忽略时间戳 ExportedAt）。
// 这是 StreamExport 正确性的核心保证：流式输出必须能被现有导入路径原样消费。
func TestStreamExportMatchesFullExport(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedStoreForExport(t, store)

	full, err := store.ExportFullForJSON(ctx)
	if err != nil {
		t.Fatalf("ExportFullForJSON: %v", err)
	}
	fullJSON, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal full: %v", err)
	}

	var buf bytes.Buffer
	if _, err := store.StreamExport(ctx, ExportScope{Mode: "full"}, &buf); err != nil {
		t.Fatalf("StreamExport: %v", err)
	}

	var fullData, streamData ExportData
	if err := json.Unmarshal(fullJSON, &fullData); err != nil {
		t.Fatalf("unmarshal full: %v", err)
	}
	if err := json.Unmarshal(buf.Bytes(), &streamData); err != nil {
		t.Fatalf("StreamExport output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	// ExportedAt 是 time.Now()，两次调用必然不同，清空后比较
	fullData.ExportedAt = ""
	streamData.ExportedAt = ""
	if !reflect.DeepEqual(fullData, streamData) {
		t.Fatalf("stream export differs from full export:\nfull =%#v\nstream=%#v", fullData, streamData)
	}
	t.Logf("[OK] stream export matches full export (%d items, %d groups, %d links)",
		len(streamData.Items), len(streamData.Groups), len(streamData.ItemGroups))
}

// TestStreamExportRangeScope 验证 range 模式只导出时间范围内的记录，
// 且 item_groups 也被同步过滤到该范围。
func TestStreamExportRangeScope(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	old := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	insertItem(t, store, old, "old record", false, false)
	insertItem(t, store, recent, "recent record", false, false)

	var buf bytes.Buffer
	scope := ExportScope{Mode: "range", From: "2026-06-01T00:00:00Z", To: "2026-08-01T00:00:00Z"}
	if _, err := store.StreamExport(ctx, scope, &buf); err != nil {
		t.Fatalf("StreamExport range: %v", err)
	}

	var data ExportData
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(data.Items) != 1 || data.Items[0].Text != "recent record" {
		t.Fatalf("range export should contain only 'recent record', got %#v", data.Items)
	}
	t.Logf("[OK] range export returned %d item(s)", len(data.Items))
}

// TestStreamExportIncrementalScope 验证 incremental 模式只导出 id > SinceID 的记录。
func TestStreamExportIncrementalScope(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	insertItem(t, store, now, "first", false, false)
	insertItem(t, store, now, "second", false, false)
	insertItem(t, store, now, "third", false, false)

	// 全量导出拿 id，取第二条作为增量水位线
	var fullBuf bytes.Buffer
	if _, err := store.StreamExport(ctx, ExportScope{Mode: "full"}, &fullBuf); err != nil {
		t.Fatalf("StreamExport full: %v", err)
	}
	var fullData ExportData
	if err := json.Unmarshal(fullBuf.Bytes(), &fullData); err != nil {
		t.Fatalf("unmarshal full: %v", err)
	}
	if len(fullData.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(fullData.Items))
	}
	cutoff := fullData.Items[1].ID

	// 增量导出 id > cutoff，应只返回 third
	var incBuf bytes.Buffer
	if _, err := store.StreamExport(ctx, ExportScope{Mode: "incremental", SinceID: cutoff}, &incBuf); err != nil {
		t.Fatalf("StreamExport incremental: %v", err)
	}
	var incData ExportData
	if err := json.Unmarshal(incBuf.Bytes(), &incData); err != nil {
		t.Fatalf("unmarshal incremental: %v", err)
	}
	if len(incData.Items) != 1 || incData.Items[0].Text != "third" {
		t.Fatalf("incremental export should return only 'third', got %#v", incData.Items)
	}
	t.Logf("[OK] incremental export since id=%d returned %d item(s)", cutoff, len(incData.Items))
}

// TestStreamExportRoundtripImport 验证 StreamExport 输出能被 ImportFullJSON 导入，
// 即流式导出 -> 导入的端到端链路。
func TestStreamExportRoundtripImport(t *testing.T) {
	ctx := context.Background()
	src, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	seedStoreForExport(t, src)

	var buf bytes.Buffer
	if _, err := src.StreamExport(ctx, ExportScope{Mode: "full"}, &buf); err != nil {
		t.Fatalf("StreamExport: %v", err)
	}

	dst, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.ImportFullJSON(ctx, buf.Bytes()); err != nil {
		t.Fatalf("ImportFullJSON of stream export: %v", err)
	}

	items, total, err := dst.Search(ctx, SearchOptions{Limit: 100, IncludeSensitive: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 3 {
		t.Fatalf("imported %d items, want 3", total)
	}
	// 验证 starred/sensitive 标记保留
	for _, it := range items {
		if it.Text == "gamma" && !it.Starred {
			t.Fatalf("starred flag lost on 'gamma'")
		}
		if it.Text == "beta" && !it.Sensitive {
			t.Fatalf("sensitive flag lost on 'beta'")
		}
	}
	t.Logf("[OK] stream export -> import roundtrip (%d items, flags preserved)", total)
}

// TestStreamExportEmptyStore 验证空库导出产生合法的空数组 JSON（边界情况）。
func TestStreamExportEmptyStore(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveConfig(DefaultConfig()); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := store.StreamExport(context.Background(), ExportScope{Mode: "full"}, &buf); err != nil {
		t.Fatalf("StreamExport empty: %v", err)
	}
	var data ExportData
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		t.Fatalf("empty stream export invalid JSON: %v\n%s", err, buf.String())
	}
	if len(data.Items) != 0 || len(data.Groups) != 0 || len(data.ItemGroups) != 0 {
		t.Fatalf("empty store should export empty arrays, got items=%d groups=%d links=%d",
			len(data.Items), len(data.Groups), len(data.ItemGroups))
	}
	// 必须输出 [] 而非 null，与 ExportFullForJSON 一致
	if !bytes.Contains(buf.Bytes(), []byte(`"items":[]`)) {
		t.Fatalf("empty export should contain \"items\":[], got: %s", buf.String())
	}
	t.Logf("[OK] empty store exports valid empty arrays")
}

// seedStoreForExport 填充测试数据：3 条 item（含 starred/sensitive）+ 2 个嵌套 group + 2 条 link + 敏感规则。
func seedStoreForExport(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	insertItem(t, store, now, "alpha", false, false)
	insertItem(t, store, now, "beta", false, true)
	insertItem(t, store, now, "gamma", true, false)

	cfg := DefaultConfig()
	cfg.Sensitive.CustomPatterns = []string{"(?i)secret"}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	g1, err := store.CreateGroup(ctx, "Parent", 0)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	g2, err := store.CreateGroup(ctx, "Child", g1.ID)
	if err != nil {
		t.Fatalf("CreateGroup nested: %v", err)
	}
	all, _, err := store.Search(ctx, SearchOptions{Limit: 100, IncludeSensitive: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if err := store.AddItemToGroup(ctx, all[0].ID, g2.ID); err != nil {
		t.Fatalf("AddItemToGroup: %v", err)
	}
	if err := store.AddItemToGroup(ctx, all[1].ID, g1.ID); err != nil {
		t.Fatalf("AddItemToGroup: %v", err)
	}
}

// insertItem 直接插入一条完整记录（绕过 Save，用于精确控制 created_at/starred/sensitive）。
func insertItem(t *testing.T, store *Store, ts time.Time, text string, starred, sensitive bool) {
	t.Helper()
	_, err := store.db.ExecContext(context.Background(), `INSERT INTO clipboard_items
		(created_at, kind, format, text, byte_len, char_len, sha256, meta_json, starred, sensitive)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts.UTC().Format(time.RFC3339Nano),
		"text", "CF_UNICODETEXT", text, len([]byte(text)), textRuneLen(text),
		hashString(text), "{}", starred, sensitive,
	)
	if err != nil {
		t.Fatalf("insert %q: %v", text, err)
	}
}

// TestImportMergesGroupsIdempotently 验证 groups 按 (name, parent_id) 幂等合并：
// 同一导出重复导入（等价于邮件分片依次还原，每片都携带全量 groups），
// 分组不重复创建；items 仍按声明的追加模式翻倍；links 不撞主键。
func TestImportMergesGroupsIdempotently(t *testing.T) {
	ctx := context.Background()
	src, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	seedStoreForExport(t, src)

	var buf bytes.Buffer
	if _, err := src.StreamExport(ctx, ExportScope{Mode: "full"}, &buf); err != nil {
		t.Fatalf("StreamExport: %v", err)
	}

	dst, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.ImportFullJSON(ctx, buf.Bytes()); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if err := dst.ImportFullJSON(ctx, buf.Bytes()); err != nil {
		t.Fatalf("second import (multi-part restore scenario): %v", err)
	}

	groups, err := dst.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups should be merged idempotently (want 2), got %d: %#v", len(groups), groups)
	}
	var itemCount, linkCount int
	if err := dst.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clipboard_items`).Scan(&itemCount); err != nil {
		t.Fatal(err)
	}
	if err := dst.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_groups`).Scan(&linkCount); err != nil {
		t.Fatal(err)
	}
	if itemCount != 6 {
		t.Fatalf("items are appended per import (want 6), got %d", itemCount)
	}
	if linkCount != 4 {
		t.Fatalf("links should remap onto merged groups without PK collision (want 4), got %d", linkCount)
	}
	t.Logf("[OK] groups merged idempotently across repeated imports (%d groups, %d items, %d links)", len(groups), itemCount, linkCount)
}

// TestImportDeduplicatesLinksAfterGroupMerge 验证导出文件内同名同 parent 的多个
// 分组被合并成一个后，remap 撞到同一 (item_id, group_id) 主键的 links 被去重，
// 导入正常完成而非主键冲突报错。
func TestImportDeduplicatesLinksAfterGroupMerge(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	export := ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		ExportedAt:    now.Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Items: []StoredItem{{
			ID: 1, CreatedAt: now, Kind: "text", Format: "CF_UNICODETEXT",
			Text: "hello", ByteLen: 5, CharLen: 5, SHA256: hashString("hello"), MetaJSON: "{}",
		}},
		Groups: []StoredGroup{
			{ID: 1, Name: "Dup", ParentID: 0, CreatedAt: now},
			{ID: 2, Name: "Dup", ParentID: 0, CreatedAt: now},
		},
		ItemGroups: []ItemGroupLink{{ItemID: 1, GroupID: 1}, {ItemID: 1, GroupID: 2}},
		Settings:   ExportSettings{Sensitive: SensitiveConfig{CustomPatterns: []string{}}},
	}
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}

	dst, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.ImportFullJSON(ctx, data); err != nil {
		t.Fatalf("import with merge-collapsed links: %v", err)
	}

	groups, err := dst.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("duplicate source groups should merge into 1, got %d", len(groups))
	}
	var linkCount int
	if err := dst.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_groups`).Scan(&linkCount); err != nil {
		t.Fatal(err)
	}
	if linkCount != 1 {
		t.Fatalf("merged links should deduplicate to 1, got %d", linkCount)
	}
	t.Logf("[OK] group merge + link dedup: %d group, %d link", len(groups), linkCount)
}
