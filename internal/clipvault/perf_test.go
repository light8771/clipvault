package clipvault

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"
)

// TestImportBatchedRoundtrip 验证批量导入（>importBatchSize 触发多批）的正确性：
// 导出 600 条记录后导入目标库，数量和内容应完整保留。
func TestImportBatchedRoundtrip(t *testing.T) {
	ctx := context.Background()
	src, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.SaveConfig(DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 600; i++ {
		insertItem(t, src, now, fmt.Sprintf("item-%03d", i), i%50 == 0, i%100 == 0)
	}

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
		t.Fatalf("ImportFullJSON batched: %v", err)
	}

	// total 是 COUNT(*)，不受 Search 上限影响；但 Search 结果行数被内部
	// 截断到 500（store.go: Limit > 500 → 500），所以 flags 统计改用 SQL 聚合。
	_, total, err := dst.Search(ctx, SearchOptions{Limit: 100, IncludeSensitive: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 600 {
		t.Fatalf("imported %d items, want 600", total)
	}
	var starred, sensitive int
	if err := dst.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN starred THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN sensitive THEN 1 ELSE 0 END), 0)
		FROM clipboard_items`).Scan(&starred, &sensitive); err != nil {
		t.Fatalf("count flags: %v", err)
	}
	if starred != 12 || sensitive != 6 {
		t.Fatalf("flags mismatch: starred=%d (want 12), sensitive=%d (want 6)", starred, sensitive)
	}
	t.Logf("[OK] batched import of 600 items (starred=%d, sensitive=%d)", starred, sensitive)
}

// TestBackupWatermarkReadWrite 验证备份水位线的持久化与读取。
func TestBackupWatermarkReadWrite(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 初始无水位线，应返回零值
	wm, err := store.GetBackupWatermark(ctx, "email")
	if err != nil {
		t.Fatalf("GetBackupWatermark initial: %v", err)
	}
	if wm.LastItemID != 0 {
		t.Fatalf("initial watermark should be zero, got LastItemID=%d", wm.LastItemID)
	}

	// 写入水位线
	if err := store.SetBackupWatermark(ctx, "email", BackupWatermark{LastItemID: 42, LastSentAt: "2026-07-29T10:00:00Z", IncrementalCount: 3}); err != nil {
		t.Fatalf("SetBackupWatermark: %v", err)
	}

	// 读回验证
	wm, err = store.GetBackupWatermark(ctx, "email")
	if err != nil {
		t.Fatalf("GetBackupWatermark after set: %v", err)
	}
	if wm.LastItemID != 42 || wm.IncrementalCount != 3 || wm.LastSentAt != "2026-07-29T10:00:00Z" {
		t.Fatalf("watermark not persisted correctly: %#v", wm)
	}

	// dir 与 email 水位线独立
	store.SetBackupWatermark(ctx, "dir", BackupWatermark{LastItemID: 99, IncrementalCount: 1})
	wmDir, _ := store.GetBackupWatermark(ctx, "dir")
	wmEmail, _ := store.GetBackupWatermark(ctx, "email")
	if wmDir.LastItemID != 99 || wmEmail.LastItemID != 42 {
		t.Fatalf("dir/email watermarks should be independent: dir=%#v email=%#v", wmDir, wmEmail)
	}
	t.Logf("[OK] backup watermark persisted (email id=%d, dir id=%d)", wmEmail.LastItemID, wmDir.LastItemID)
}

// TestResolveBackupScopeFallback 验证 incremental 模式的降级与透明标记。
func TestResolveBackupScopeFallback(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 无水位线：incremental 应降级 full（autoFallback=true）
	scope, autoFB, err := resolveBackupScope(store, "email", ExportScope{Mode: "incremental"}, 7, false)
	if err != nil || scope.Mode != "full" || !autoFB {
		t.Fatalf("incremental without watermark should fall back to full (autoFallback=true), got mode=%q autoFB=%v err=%v", scope.Mode, autoFB, err)
	}

	// 设水位线后：incremental 应填入 SinceID（autoFallback=false）
	store.SetBackupWatermark(ctx, "email", BackupWatermark{LastItemID: 100, IncrementalCount: 1})
	scope, autoFB, err = resolveBackupScope(store, "email", ExportScope{Mode: "incremental"}, 7, false)
	if err != nil || scope.Mode != "incremental" || scope.SinceID != 100 || autoFB {
		t.Fatalf("incremental with watermark should fill SinceID (autoFallback=false), got %#v autoFB=%v err=%v", scope, autoFB, err)
	}

	// 增量链过长（>=fullInterval）：降级 full（autoFallback=true）
	store.SetBackupWatermark(ctx, "email", BackupWatermark{LastItemID: 100, IncrementalCount: 7})
	scope, autoFB, err = resolveBackupScope(store, "email", ExportScope{Mode: "incremental"}, 7, false)
	if err != nil || scope.Mode != "full" || !autoFB {
		t.Fatalf("incremental chain too long should fall back to full (autoFallback=true), got mode=%q autoFB=%v err=%v", scope.Mode, autoFB, err)
	}

	// forceFull=true：强制 full（autoFallback=false，手动不算自动兜底）
	store.SetBackupWatermark(ctx, "email", BackupWatermark{LastItemID: 100, IncrementalCount: 1})
	scope, autoFB, err = resolveBackupScope(store, "email", ExportScope{Mode: "incremental"}, 7, true)
	if err != nil || scope.Mode != "full" || autoFB {
		t.Fatalf("forceFull should run full (autoFallback=false), got mode=%q autoFB=%v err=%v", scope.Mode, autoFB, err)
	}

	// fullInterval=0：不自动兜底，链过长也不降级
	store.SetBackupWatermark(ctx, "email", BackupWatermark{LastItemID: 100, IncrementalCount: 99})
	scope, autoFB, err = resolveBackupScope(store, "email", ExportScope{Mode: "incremental"}, 0, false)
	if err != nil || scope.Mode != "incremental" || autoFB {
		t.Fatalf("fullInterval=0 should not auto-fallback, got mode=%q autoFB=%v err=%v", scope.Mode, autoFB, err)
	}
	t.Logf("[OK] resolveBackupScope transparency logic works")
}

// TestResolveBackupScopeRangeValidation 验证 range 模式的 From/To 校验。
func TestResolveBackupScopeRangeValidation(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 缺 From/To：报错
	if _, _, err := resolveBackupScope(store, "dir", ExportScope{Mode: "range", From: "", To: "2026-08-01T00:00:00Z"}, 7, false); err == nil {
		t.Fatal("range without from should fail")
	}
	if _, _, err := resolveBackupScope(store, "dir", ExportScope{Mode: "range", From: "2026-07-01T00:00:00Z", To: ""}, 7, false); err == nil {
		t.Fatal("range without to should fail")
	}
	if _, _, err := resolveBackupScope(store, "dir", ExportScope{Mode: "range", From: "2026-08-01T00:00:00Z", To: "2026-07-01T00:00:00Z"}, 7, false); err == nil {
		t.Fatal("range with from >= to should fail")
	}
	if _, _, err := resolveBackupScope(store, "dir", ExportScope{Mode: "range", From: "2026-07-01T00:00:00Z", To: "2026-07-01T00:00:00Z"}, 7, false); err == nil {
		t.Fatal("empty half-open range should fail")
	}
	// 合法 range：原样通过（不看水位线）
	scope, autoFB, err := resolveBackupScope(store, "dir", ExportScope{Mode: "range", From: "2026-07-01T00:00:00Z", To: "2026-08-01T00:00:00Z"}, 7, false)
	if err != nil || scope.Mode != "range" || scope.From != "2026-07-01T00:00:00Z" || scope.To != "2026-08-01T00:00:00Z" || autoFB {
		t.Fatalf("valid range should pass through, got %#v autoFB=%v err=%v", scope, autoFB, err)
	}
	// forceFull 跳过 range 校验（强制全量优先）
	scope, autoFB, err = resolveBackupScope(store, "dir", ExportScope{Mode: "range"}, 7, true)
	if err != nil || scope.Mode != "full" || autoFB {
		t.Fatalf("forceFull should bypass range validation, got %#v autoFB=%v err=%v", scope, autoFB, err)
	}
	t.Logf("[OK] range scope validation works")
}
