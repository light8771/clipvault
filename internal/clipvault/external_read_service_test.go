package clipvault

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

// externalReadFixture 准备一个共享读取服务与固定数据集：
// 两条普通记录 + 两条敏感记录 + 一个分组（含一条敏感成员）。
type externalReadFixture struct {
	store        *Store
	svc          *ExternalReadService
	normalIDs    []int64
	sensitiveIDs []int64
	groupID      int64
}

func newExternalReadFixture(t *testing.T) *externalReadFixture {
	t.Helper()
	store := openDataAPITestStore(t)
	env, err := newEnvironment(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fx := &externalReadFixture{store: store, svc: NewExternalReadService(store, env)}
	seeds := []ClipboardItem{
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "normal one", SourceExeName: "Code.exe"},
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "normal two", SourceExeName: "chrome.exe"},
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "secret one", SourceExeName: "Code.exe"},
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "secret two", SourceExeName: "chrome.exe"},
	}
	for _, item := range seeds {
		if err := store.Save(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	// 按 ID 升序取回（与保存顺序一致）：前两条普通、后两条敏感。
	for i := range seeds {
		var id int64
		if err := store.db.QueryRowContext(ctx,
			`SELECT id FROM clipboard_items ORDER BY id ASC LIMIT 1 OFFSET ?`, i).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			fx.normalIDs = append(fx.normalIDs, id)
		} else {
			fx.sensitiveIDs = append(fx.sensitiveIDs, id)
		}
	}
	if _, err := store.db.ExecContext(ctx,
		`UPDATE clipboard_items SET sensitive = true WHERE text LIKE 'secret%'`); err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup(ctx, "工作", 0)
	if err != nil {
		t.Fatal(err)
	}
	fx.groupID = group.ID
	// 分组成员：一条普通 + 一条敏感。
	if err := store.AddItemToGroup(ctx, fx.normalIDs[0], fx.groupID); err != nil {
		t.Fatal(err)
	}
	if err := store.AddItemToGroup(ctx, fx.sensitiveIDs[0], fx.groupID); err != nil {
		t.Fatal(err)
	}
	return fx
}

var (
	deniedAccess  = ReadAccess{AllowSensitive: false}
	grantedAccess = ReadAccess{AllowSensitive: true}
)

func formatGroupID(id int64) string { return strconv.FormatInt(id, 10) }

// TestSearchExcludesSensitiveWithoutPermission S1：未授权搜索不返回敏感行。
func TestSearchExcludesSensitiveWithoutPermission(t *testing.T) {
	fx := newExternalReadFixture(t)
	result, err := fx.svc.Search(context.Background(), deniedAccess, SearchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != len(fx.normalIDs) {
		t.Fatalf("items=%d, want %d", len(result.Items), len(fx.normalIDs))
	}
	for _, item := range result.Items {
		if item.Sensitive {
			t.Fatalf("unauthorized search returned sensitive item %+v", item)
		}
	}
}

// TestSearchIncludesSensitiveWithPermission S1：授权搜索返回敏感行。
func TestSearchIncludesSensitiveWithPermission(t *testing.T) {
	fx := newExternalReadFixture(t)
	result, err := fx.svc.Search(context.Background(), grantedAccess, SearchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != len(fx.normalIDs)+len(fx.sensitiveIDs) {
		t.Fatalf("items=%d, want %d", len(result.Items), len(fx.normalIDs)+len(fx.sensitiveIDs))
	}
}

// TestRequestedSensitiveTrueWithoutPermissionReturnsEmpty：
// 未授权调用方显式请求 sensitive=true 得到空集而非绕过。
func TestRequestedSensitiveTrueWithoutPermissionReturnsEmpty(t *testing.T) {
	fx := newExternalReadFixture(t)
	sensitive := true
	result, err := fx.svc.Search(context.Background(), deniedAccess, SearchRequest{CountRequest: CountRequest{Sensitive: &sensitive}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("sensitive=true without permission must return empty, got %d", len(result.Items))
	}
	// sensitive=false 正常工作。
	notSensitive := false
	result, err = fx.svc.Search(context.Background(), deniedAccess, SearchRequest{CountRequest: CountRequest{Sensitive: &notSensitive}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != len(fx.normalIDs) {
		t.Fatalf("sensitive=false items=%d, want %d", len(result.Items), len(fx.normalIDs))
	}
}

// TestGetSensitiveWithoutPermissionIsNotFound S2：已知敏感 ID 的单条获取是 not_found。
func TestGetSensitiveWithoutPermissionIsNotFound(t *testing.T) {
	fx := newExternalReadFixture(t)
	if _, err := fx.svc.GetItem(context.Background(), deniedAccess, fx.sensitiveIDs[0]); !errors.Is(err, ErrExternalNotFound) {
		t.Fatalf("unauthorized sensitive get err=%v, want not found", err)
	}
	if _, err := fx.svc.GetStoredItem(context.Background(), deniedAccess, fx.sensitiveIDs[0]); !errors.Is(err, ErrExternalNotFound) {
		t.Fatalf("unauthorized sensitive stored get err=%v, want not found", err)
	}
	item, err := fx.svc.GetItem(context.Background(), grantedAccess, fx.sensitiveIDs[0])
	if err != nil || !item.Sensitive {
		t.Fatalf("granted sensitive get err=%v item=%+v", err, item)
	}
}

// TestBatchOmitsSensitiveWithoutPermission S3：批量结果静默省略敏感 ID。
func TestBatchOmitsSensitiveWithoutPermission(t *testing.T) {
	fx := newExternalReadFixture(t)
	ids := append([]int64{fx.normalIDs[0]}, fx.sensitiveIDs[0], fx.normalIDs[1])
	items, err := fx.svc.GetItems(context.Background(), deniedAccess, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(fx.normalIDs) {
		t.Fatalf("batch items=%d, want %d", len(items), len(fx.normalIDs))
	}
	for _, item := range items {
		if item.Sensitive {
			t.Fatalf("batch leaked sensitive item %+v", item)
		}
	}
	// 授权后批量可见全部。
	granted, err := fx.svc.GetItems(context.Background(), grantedAccess, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(granted) != 3 {
		t.Fatalf("granted batch items=%d, want 3", len(granted))
	}
}

// TestContentSensitiveWithoutPermissionIsNotFound S2：未授权敏感内容是 not_found。
func TestContentSensitiveWithoutPermissionIsNotFound(t *testing.T) {
	fx := newExternalReadFixture(t)
	if _, err := fx.svc.GetContent(context.Background(), deniedAccess, fx.sensitiveIDs[0]); !errors.Is(err, ErrExternalNotFound) {
		t.Fatalf("unauthorized sensitive content err=%v, want not found", err)
	}
	content, err := fx.svc.GetContent(context.Background(), grantedAccess, fx.sensitiveIDs[0])
	if err != nil || content.Kind != "text" || content.Text != "secret one" || !content.Sensitive {
		t.Fatalf("granted content err=%v content=%+v", err, content)
	}
}

// TestCountExcludesSensitive S4：计数只统计可见行。
func TestCountExcludesSensitive(t *testing.T) {
	fx := newExternalReadFixture(t)
	deniedCount, err := fx.svc.Count(context.Background(), deniedAccess, CountRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if deniedCount != int64(len(fx.normalIDs)) {
		t.Fatalf("denied count=%d, want %d", deniedCount, len(fx.normalIDs))
	}
	grantedCount, err := fx.svc.Count(context.Background(), grantedAccess, CountRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if grantedCount != int64(len(fx.normalIDs)+len(fx.sensitiveIDs)) {
		t.Fatalf("granted count=%d, want %d", grantedCount, len(fx.normalIDs)+len(fx.sensitiveIDs))
	}
}

// TestStatsExcludesSensitive S5：统计的总量/字节/分布/趋势排除敏感行。
func TestStatsExcludesSensitive(t *testing.T) {
	fx := newExternalReadFixture(t)
	denied, err := fx.svc.Stats(context.Background(), deniedAccess, StatsRequest{Bucket: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Total != int64(len(fx.normalIDs)) {
		t.Fatalf("denied stats total=%d, want %d", denied.Total, len(fx.normalIDs))
	}
	var deniedBytes int64
	for _, item := range append(fx.normalIDs[:0:0], fx.normalIDs...) {
		var text string
		if err := fx.store.db.QueryRow(`SELECT text FROM clipboard_items WHERE id = ?`, item).Scan(&text); err != nil {
			t.Fatal(err)
		}
		deniedBytes += int64(len(text))
	}
	if denied.TotalBytes != deniedBytes {
		t.Fatalf("denied stats bytes=%d, want %d", denied.TotalBytes, deniedBytes)
	}
	granted, err := fx.svc.Stats(context.Background(), grantedAccess, StatsRequest{Bucket: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if granted.Total != int64(len(fx.normalIDs)+len(fx.sensitiveIDs)) {
		t.Fatalf("granted stats total=%d, want %d", granted.Total, len(fx.normalIDs)+len(fx.sensitiveIDs))
	}
	// 无效 bucket 被拒绝。
	if _, err := fx.svc.Stats(context.Background(), deniedAccess, StatsRequest{Bucket: "week"}); !errors.Is(err, ErrExternalInvalid) {
		t.Fatalf("invalid bucket err=%v, want invalid request", err)
	}
}

// TestGroupCountsExcludeSensitive S6：分组可见计数与集合排除敏感行。
func TestGroupCountsExcludeSensitive(t *testing.T) {
	fx := newExternalReadFixture(t)
	deniedGroups, err := fx.svc.ListGroups(context.Background(), deniedAccess, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(deniedGroups) != 1 || deniedGroups[0].VisibleItemCount != 1 {
		t.Fatalf("denied group counts=%+v, want visible count 1", deniedGroups)
	}
	groupItems, err := fx.svc.ListGroupItems(context.Background(), deniedAccess, deniedGroups[0].ID, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(groupItems.Items) != 1 || groupItems.Items[0].Sensitive {
		t.Fatalf("denied group items=%+v, want 1 visible item", groupItems.Items)
	}
	grantedGroups, err := fx.svc.ListGroups(context.Background(), grantedAccess, true)
	if err != nil {
		t.Fatal(err)
	}
	if grantedGroups[0].VisibleItemCount != 2 {
		t.Fatalf("granted group count=%d, want 2", grantedGroups[0].VisibleItemCount)
	}
	// GetGroup 同样遵守可见性。
	group, err := fx.svc.GetGroup(context.Background(), deniedAccess, deniedGroups[0].ID)
	if err != nil || group.VisibleItemCount != 1 {
		t.Fatalf("denied get group=%+v err=%v", group, err)
	}
}

// TestExternalSearchFilterAndPagination 覆盖结构化过滤与游标分页。
func TestExternalSearchFilterAndPagination(t *testing.T) {
	fx := newExternalReadFixture(t)
	ctx := context.Background()
	// 文本包含 + 来源过滤。
	result, err := fx.svc.Search(ctx, deniedAccess, SearchRequest{CountRequest: CountRequest{Query: "normal", SourceApp: "code.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Text != "normal one" {
		t.Fatalf("filtered search=%+v", result.Items)
	}
	// kind 白名单拒绝非法值。
	if _, err := fx.svc.Search(ctx, deniedAccess, SearchRequest{CountRequest: CountRequest{Kinds: []string{"dropzone"}}}); !errors.Is(err, ErrExternalInvalid) {
		t.Fatalf("invalid kind err=%v, want invalid request", err)
	}
	// 分组过滤：可见范围内分组只含 1 条普通记录。
	groupResult, err := fx.svc.Search(ctx, deniedAccess, SearchRequest{CountRequest: CountRequest{GroupID: formatGroupID(fx.groupID)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(groupResult.Items) != 1 {
		t.Fatalf("group filtered search items=%d, want 1", len(groupResult.Items))
	}
	// limit=1 翻页：两页拿到两条可见记录，游标前进。
	page1, err := fx.svc.Search(ctx, deniedAccess, SearchRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != 1 || !page1.HasMore || page1.NextCursor == nil {
		t.Fatalf("page1=%+v", page1)
	}
	page2, err := fx.svc.Search(ctx, deniedAccess, SearchRequest{Limit: 1, Cursor: *page1.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 1 || page2.HasMore {
		t.Fatalf("page2=%+v", page2)
	}
	if page1.Items[0].ID == page2.Items[0].ID {
		t.Fatal("pages must not overlap")
	}
	// 篡改游标被拒绝。
	if _, err := fx.svc.Search(ctx, deniedAccess, SearchRequest{Cursor: "bm90LWEtY3Vyc29y"}); !errors.Is(err, ErrExternalInvalid) {
		t.Fatalf("tampered cursor err=%v, want invalid request", err)
	}
}
