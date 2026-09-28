package clipvault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

// ── 共享读取权限（协议无关）─────────────────────────────────

// ReadAccess 是外部读取服务调用方的可见性权限。敏感数据只有当
// AllowSensitive=true（Token 显式授权）时才可见；否则敏感记录在所有读取
// 路径（列表/单条/批量/内容/计数/统计/分组）上与"不存在"完全等价，
// 不通过行、计数、聚合或错误差异泄露其存在（SEC-01..06）。
type ReadAccess struct {
	AllowSensitive bool
}

// 外部读取的共享治理参数（与数据接口 HTTP 治理口径一致）。
const (
	externalDefaultLimit   = dataAPIDefaultLimit
	externalMaxLimit       = dataAPIMaxLimit
	externalMaxBatchIDs    = dataAPIMaxBatchIDs
	externalMaxContentSize = dataAPIMaxContentSize
	externalTextPreviewMax = dataAPITextPreviewMax
)

// 协议中立错误：协议层（HTTP/MCP）把它们映射为各自契约，绝不透出 DB 错误
// 细节或敏感存在性。
var (
	ErrExternalNotFound = errors.New("external: not found")
	ErrExternalInvalid  = errors.New("external: invalid request")
	ErrExternalTooLarge = errors.New("external: response too large")
)

// ExternalReadService 是 HTTP 数据接口与 MCP 共享的只读业务服务。
// 协议层只做协议解析与响应映射；记录可见性策略唯一地实现在这里，
// 并尽可能下推到 SQL 过滤。
type ExternalReadService struct {
	store *Store
	env   Environment
}

// NewExternalReadService 创建共享读取服务。env 用于定位托管图片目录
// （内容读取的路径防穿越校验也在本服务完成）。
func NewExternalReadService(store *Store, env Environment) *ExternalReadService {
	return &ExternalReadService{store: store, env: env}
}

// ── 查询实现 ──────────────────────────────────────────────

// queryExternalItems 执行条目查询（固定 created_at DESC, id DESC 排序），
// limit+1 探测 hasMore；返回可见条目与下一页游标。
func (s *ExternalReadService) queryExternalItems(ctx context.Context, access ReadAccess, where string, args []any, limit int) ([]StoredItem, bool, error) {
	fetchArgs := append(append([]any{}, args...), limit+1)
	rows, err := s.store.db.QueryContext(ctx,
		"SELECT "+clipboardItemReadColumns(!access.AllowSensitive)+" FROM clipboard_items"+where+
			" ORDER BY created_at DESC, id DESC LIMIT ?", fetchArgs...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := []StoredItem{}
	for rows.Next() {
		var item StoredItem
		if err := scanStoredItem(rows, &item); err != nil {
			return nil, false, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}

// externalSearchResult 把存储条目组装为 SearchResult（含分组与截断）。
func (s *ExternalReadService) externalSearchResult(ctx context.Context, items []StoredItem, hasMore bool) (SearchResult, error) {
	ids := make([]int64, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	groups, err := s.store.ItemAllGroupIDs(ctx, ids)
	if err != nil {
		return SearchResult{}, err
	}
	out := make([]ExternalItem, len(items))
	for i, item := range items {
		out[i] = toExternalItem(item, groups[item.ID], true)
	}
	result := SearchResult{Items: out, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		cursor := encodeExternalCursor(last.CreatedAt.UnixMilli(), last.ID)
		result.NextCursor = &cursor
	}
	return result, nil
}

// Search 按结构化过滤搜索可见剪贴板记录（MCP search_clipboard 工具与
// 集合资源共用）。
func (s *ExternalReadService) Search(ctx context.Context, access ReadAccess, req SearchRequest) (SearchResult, error) {
	where, args, err := buildExternalFilter(req.CountRequest, access)
	if err != nil {
		return SearchResult{}, err
	}
	limit := clampExternalLimit(req.Limit)
	if req.Cursor != "" {
		cursorMS, cursorID, err := decodeExternalCursor(req.Cursor)
		if err != nil {
			return SearchResult{}, fmt.Errorf("%w: invalid cursor", ErrExternalInvalid)
		}
		if where == "" {
			where = " WHERE (created_at < ? OR (created_at = ? AND id < ?))"
		} else {
			where += " AND (created_at < ? OR (created_at = ? AND id < ?))"
		}
		args = append(args, cursorMS, cursorMS, cursorID)
	}
	items, hasMore, err := s.queryExternalItems(ctx, access, where, args, limit)
	if err != nil {
		return SearchResult{}, err
	}
	return s.externalSearchResult(ctx, items, hasMore)
}

// GetStoredItem 返回单条存储记录（HTTP 层复用其完整字段；敏感记录对
// 未授权调用方与不存在完全等价）。
func (s *ExternalReadService) GetStoredItem(ctx context.Context, access ReadAccess, id int64) (StoredItem, error) {
	if id <= 0 {
		return StoredItem{}, fmt.Errorf("%w: invalid id", ErrExternalInvalid)
	}
	var item StoredItem
	row := s.store.db.QueryRowContext(ctx,
		"SELECT "+clipboardItemReadColumns(!access.AllowSensitive)+" FROM clipboard_items WHERE id = ?"+externalItemScopeAnd(access), id)
	if err := scanStoredItem(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredItem{}, ErrExternalNotFound
		}
		return StoredItem{}, err
	}
	return item, nil
}

// GetItem 返回单条协议中立条目（完整文本，不截断）。
func (s *ExternalReadService) GetItem(ctx context.Context, access ReadAccess, id int64) (ExternalItem, error) {
	item, err := s.GetStoredItem(ctx, access, id)
	if err != nil {
		return ExternalItem{}, err
	}
	groupIDs, err := s.store.GetItemGroups(ctx, item.ID)
	if err != nil {
		return ExternalItem{}, err
	}
	return toExternalItem(item, groupIDs, false), nil
}

// GetItems 批量返回条目（保持请求顺序；不存在或未授权敏感的 ID 静默省略）。
func (s *ExternalReadService) GetItems(ctx context.Context, access ReadAccess, ids []int64) ([]ExternalItem, error) {
	items, err := s.GetStoredItems(ctx, access, ids)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return []ExternalItem{}, nil
	}
	itemIDs := make([]int64, len(items))
	for i, item := range items {
		itemIDs[i] = item.ID
	}
	groups, err := s.store.ItemAllGroupIDs(ctx, itemIDs)
	if err != nil {
		return nil, err
	}
	out := make([]ExternalItem, len(items))
	for i, item := range items {
		out[i] = toExternalItem(item, groups[item.ID], false)
	}
	return out, nil
}

// GetStoredItems 批量返回存储记录（保持请求顺序；不可见 ID 静默省略）。
// HTTP batch-get 与 MCP get_clipboard_items 共用。
func (s *ExternalReadService) GetStoredItems(ctx context.Context, access ReadAccess, ids []int64) ([]StoredItem, error) {
	if len(ids) == 0 {
		return []StoredItem{}, nil
	}
	if len(ids) > externalMaxBatchIDs {
		return nil, fmt.Errorf("%w: at most %d ids per batch", ErrExternalInvalid, externalMaxBatchIDs)
	}
	deduped := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("%w: invalid id %d", ErrExternalInvalid, id)
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			deduped = append(deduped, id)
		}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(deduped)), ",")
	args := make([]any, len(deduped))
	for i, id := range deduped {
		args[i] = id
	}
	rows, err := s.store.db.QueryContext(ctx,
		"SELECT "+clipboardItemReadColumns(!access.AllowSensitive)+" FROM clipboard_items WHERE id IN ("+placeholders+")"+externalItemScopeAnd(access), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[int64]StoredItem{}
	for rows.Next() {
		var item StoredItem
		if err := scanStoredItem(rows, &item); err != nil {
			return nil, err
		}
		found[item.ID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]StoredItem, 0, len(deduped))
	for _, id := range deduped {
		if item, ok := found[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

// GetContent 返回条目正文：文本条目返回文本；托管图片返回图片路径
// （由协议层流式输出）；文件路径记录不读取文件系统（只读文件系统边界）。
// 敏感记录对未授权调用方与不存在完全等价。
func (s *ExternalReadService) GetContent(ctx context.Context, access ReadAccess, id int64) (ExternalContent, error) {
	item, err := s.GetStoredItem(ctx, access, id)
	if err != nil {
		return ExternalContent{}, err
	}
	switch {
	case item.RecordType == domain.HistoryRecordContent && item.Format == domain.FormatImageFile:
		full, err := resolveManagedImagePath(s.env, item.Text)
		if err != nil {
			return ExternalContent{}, fmt.Errorf("%w: invalid image path", ErrExternalInvalid)
		}
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			return ExternalContent{}, ErrExternalNotFound
		}
		if info.Size() > externalMaxContentSize {
			return ExternalContent{}, ErrExternalTooLarge
		}
		return ExternalContent{Kind: "image", MimeType: "image/png", ImagePath: full, Sensitive: item.Sensitive}, nil
	case item.Kind == domain.KindText && item.Format == "CF_UNICODETEXT":
		return ExternalContent{Kind: "text", Text: item.Text, MimeType: "text/plain; charset=utf-8", Sensitive: item.Sensitive}, nil
	default:
		return ExternalContent{}, ErrExternalNotFound
	}
}

// Count 返回可见记录数（敏感行按可见性排除）。
func (s *ExternalReadService) Count(ctx context.Context, access ReadAccess, req CountRequest) (int64, error) {
	where, args, err := buildExternalFilter(req, access)
	if err != nil {
		return 0, err
	}
	var count int64
	if err := s.store.db.QueryRowContext(ctx,
		"SELECT count(*) FROM clipboard_items"+where, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// Stats 返回可见记录的结构化统计（供 AI 推理/绘图；服务端不渲染图表）。
func (s *ExternalReadService) Stats(ctx context.Context, access ReadAccess, req StatsRequest) (StatsResult, error) {
	bucketMS := int64(86400000)
	switch req.Bucket {
	case "", "day":
		bucketMS = 86400000
	case "hour":
		bucketMS = 3600000
	default:
		return StatsResult{}, fmt.Errorf("%w: unsupported bucket %q", ErrExternalInvalid, req.Bucket)
	}
	topApps := req.TopSourceApps
	if topApps <= 0 {
		topApps = 20
	}
	if topApps > 50 {
		topApps = 50
	}
	var conds []string
	var args []any
	if req.From != nil {
		conds = append(conds, "created_at >= ?")
		args = append(args, req.From.UnixMilli())
	}
	if req.To != nil {
		conds = append(conds, "created_at <= ?")
		args = append(args, req.To.UnixMilli())
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	where = applyExternalItemScope(where, access)

	result := StatsResult{
		Range:       statsRange{From: req.From, To: req.To},
		ByKind:      []StatsBucket{},
		BySourceApp: []StatsBucket{},
		Trend:       []StatsTrendPoint{},
	}
	if err := s.store.db.QueryRowContext(ctx,
		"SELECT count(*), COALESCE(sum(byte_len), 0) FROM clipboard_items"+where, args...).
		Scan(&result.Total, &result.TotalBytes); err != nil {
		return StatsResult{}, err
	}
	kindRows, err := s.store.db.QueryContext(ctx,
		"SELECT kind, count(*), COALESCE(sum(byte_len), 0) FROM clipboard_items"+where+
			" GROUP BY kind ORDER BY count(*) DESC, kind ASC", args...)
	if err != nil {
		return StatsResult{}, err
	}
	for kindRows.Next() {
		var bucket StatsBucket
		if err := kindRows.Scan(&bucket.Key, &bucket.Count, &bucket.Bytes); err != nil {
			kindRows.Close()
			return StatsResult{}, err
		}
		result.ByKind = append(result.ByKind, bucket)
	}
	kindRows.Close()
	if err := kindRows.Err(); err != nil {
		return StatsResult{}, err
	}
	appArgs := append(append([]any{}, args...), topApps)
	appRows, err := s.store.db.QueryContext(ctx,
		"SELECT source_exe_name, count(*), COALESCE(sum(byte_len), 0) FROM clipboard_items"+where+
			" GROUP BY source_exe_name ORDER BY count(*) DESC, source_exe_name ASC LIMIT ?", appArgs...)
	if err != nil {
		return StatsResult{}, err
	}
	for appRows.Next() {
		var bucket StatsBucket
		if err := appRows.Scan(&bucket.Key, &bucket.Count, &bucket.Bytes); err != nil {
			appRows.Close()
			return StatsResult{}, err
		}
		result.BySourceApp = append(result.BySourceApp, bucket)
	}
	appRows.Close()
	if err := appRows.Err(); err != nil {
		return StatsResult{}, err
	}
	bucketLiteral := strconv.FormatInt(bucketMS, 10)
	trendRows, err := s.store.db.QueryContext(ctx,
		"SELECT created_at - (created_at % "+bucketLiteral+"), count(*), COALESCE(sum(byte_len), 0) FROM clipboard_items"+where+
			" GROUP BY 1 ORDER BY 1 ASC", args...)
	if err != nil {
		return StatsResult{}, err
	}
	for trendRows.Next() {
		var point StatsTrendPoint
		var bucketMSValue int64
		if err := trendRows.Scan(&bucketMSValue, &point.Count, &point.Bytes); err != nil {
			trendRows.Close()
			return StatsResult{}, err
		}
		point.Bucket = time.UnixMilli(bucketMSValue).UTC()
		result.Trend = append(result.Trend, point)
	}
	trendRows.Close()
	if err := trendRows.Err(); err != nil {
		return StatsResult{}, err
	}
	return result, nil
}

// ── 分组读取 ──────────────────────────────────────────────

// ListGroups 返回全部分组（可选附带可见条目计数）。
func (s *ExternalReadService) ListGroups(ctx context.Context, access ReadAccess, includeCounts bool) ([]ExternalGroup, error) {
	countExpr := "0"
	if includeCounts {
		countExpr = "(SELECT count(*) FROM item_groups ig JOIN clipboard_items i ON i.id = ig.item_id WHERE ig.group_id = g.id" + externalGroupVisibleCond(access) + ")"
	}
	rows, err := s.store.db.QueryContext(ctx,
		"SELECT g.id, g.name, g.parent_id, "+countExpr+" FROM groups g ORDER BY g.id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []ExternalGroup{}
	for rows.Next() {
		var id, parentID, count int64
		var name string
		if err := rows.Scan(&id, &name, &parentID, &count); err != nil {
			return nil, err
		}
		group := ExternalGroup{ID: strconv.FormatInt(id, 10), Name: name, VisibleItemCount: count}
		if parentID > 0 {
			parent := strconv.FormatInt(parentID, 10)
			group.ParentID = &parent
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

// GetGroup 返回单个分组元数据与可见条目计数。
func (s *ExternalReadService) GetGroup(ctx context.Context, access ReadAccess, id string) (ExternalGroup, error) {
	gid, err := parseExternalGroupID(id)
	if err != nil {
		return ExternalGroup{}, err
	}
	countExpr := "(SELECT count(*) FROM item_groups ig JOIN clipboard_items i ON i.id = ig.item_id WHERE ig.group_id = g.id" + externalGroupVisibleCond(access) + ")"
	var parentID, count int64
	var name string
	err = s.store.db.QueryRowContext(ctx,
		"SELECT g.id, g.name, g.parent_id, "+countExpr+" FROM groups g WHERE g.id = ?", gid).
		Scan(&gid, &name, &parentID, &count)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalGroup{}, ErrExternalNotFound
	}
	if err != nil {
		return ExternalGroup{}, err
	}
	group := ExternalGroup{ID: strconv.FormatInt(gid, 10), Name: name, VisibleItemCount: count}
	if parentID > 0 {
		parent := strconv.FormatInt(parentID, 10)
		group.ParentID = &parent
	}
	return group, nil
}

// ListGroupItems 返回分组内最新可见条目（keyset 分页）。
func (s *ExternalReadService) ListGroupItems(ctx context.Context, access ReadAccess, id string, limit int, cursor string) (SearchResult, error) {
	gid, err := parseExternalGroupID(id)
	if err != nil {
		return SearchResult{}, err
	}
	var exists bool
	if err := s.store.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM groups WHERE id = ?)`, gid).Scan(&exists); err != nil {
		return SearchResult{}, err
	}
	if !exists {
		return SearchResult{}, ErrExternalNotFound
	}
	where := " WHERE id IN (SELECT item_id FROM item_groups WHERE group_id = ?)"
	args := []any{gid}
	limit = clampExternalLimit(limit)
	if cursor != "" {
		cursorMS, cursorID, err := decodeExternalCursor(cursor)
		if err != nil {
			return SearchResult{}, fmt.Errorf("%w: invalid cursor", ErrExternalInvalid)
		}
		where += " AND (created_at < ? OR (created_at = ? AND id < ?))"
		args = append(args, cursorMS, cursorMS, cursorID)
	}
	where = applyExternalItemScope(where, access)
	items, hasMore, err := s.queryExternalItems(ctx, access, where, args, limit)
	if err != nil {
		return SearchResult{}, err
	}
	return s.externalSearchResult(ctx, items, hasMore)
}

// ── HTTP Query DSL 执行（协议解析在 dataapi_query.go）──────

// QueryItems 执行 HTTP Query DSL 的条目查询。协议层只负责解析 DSL；
// 可见性策略（隐式敏感过滤）在此强制。
func (s *ExternalReadService) QueryItems(ctx context.Context, access ReadAccess, plan *dataAPIQueryPlan) ([]StoredItem, bool, error) {
	where := applyExternalItemScope(plan.where, access)
	args := append(append([]any{}, plan.args...), plan.cursorArgs()...)
	fetchArgs := append(append([]any{}, args...), plan.limit+1)
	rows, err := s.store.db.QueryContext(ctx,
		"SELECT "+clipboardItemReadColumns(!access.AllowSensitive)+" FROM clipboard_items"+where+
			" ORDER BY "+plan.orderSQL+" LIMIT ?", fetchArgs...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := []StoredItem{}
	for rows.Next() {
		var item StoredItem
		if err := scanStoredItem(rows, &item); err != nil {
			return nil, false, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(items) > plan.limit
	if hasMore {
		items = items[:plan.limit]
	}
	return items, hasMore, nil
}

// QueryAggregate 执行 HTTP Query DSL 的 count 聚合。未授权时敏感行被
// 隐式过滤，聚合结果只反映可见行。
func (s *ExternalReadService) QueryAggregate(ctx context.Context, access ReadAccess, plan *dataAPIQueryPlan) ([]map[string]any, error) {
	where := applyExternalItemScope(plan.where, access)
	args := append([]any{}, plan.args...)
	fetchArgs := append(append([]any{}, args...), plan.limit)
	rows, err := s.store.db.QueryContext(ctx,
		"SELECT "+plan.groupCol+", COUNT(*) AS c FROM clipboard_items"+where+
			" GROUP BY "+plan.groupCol+" ORDER BY "+plan.orderSQL+" LIMIT ?", fetchArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resultRows := []map[string]any{}
	for rows.Next() {
		var value any
		var c int64
		var boolValue bool
		var textValue string
		if plan.groupCol == "starred" || plan.groupCol == "sensitive" {
			err = rows.Scan(&boolValue, &c)
			value = boolValue
		} else {
			err = rows.Scan(&textValue, &c)
			value = textValue
		}
		if err != nil {
			return nil, err
		}
		resultRows = append(resultRows, map[string]any{plan.groupField: value, "count": c})
	}
	return resultRows, rows.Err()
}
