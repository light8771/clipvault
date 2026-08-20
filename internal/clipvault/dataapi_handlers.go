package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// ── 对外业务模型（与内部 DuckDB schema 解耦，DATA-03）──────

// dataAPIItem 是 clipboardItems 资源的公开表示（保持既有 HTTP 契约不变）。
// 敏感记录的可见性由 ExternalReadService 强制；托管图片的内部存储路径
// 永不外泄，只给 contentUrl。
type dataAPIItem struct {
	ID               string    `json:"id"`
	CreatedAt        time.Time `json:"createdAt"`
	Kind             string    `json:"kind"`
	Text             *string   `json:"text"`
	TextTruncated    bool      `json:"textTruncated,omitempty"`
	ByteLen          int       `json:"byteLen"`
	SHA256           string    `json:"sha256,omitempty"`
	SourceApp        *string   `json:"sourceApp"`
	SourceWindow     *string   `json:"sourceWindow"`
	Starred          bool      `json:"starred"`
	Sensitive        bool      `json:"sensitive"`
	GroupID          *string   `json:"groupId"`
	ContentAvailable bool      `json:"contentAvailable"`
	ContentURL       string    `json:"contentUrl,omitempty"`
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// toDataAPIItem 把存储模型映射为公开模型。preview=true 时截断超长文本，
// 完整文本经 /items/{id} 获取（限制单次响应资源消耗）。
func toDataAPIItem(item StoredItem, groupID int64, preview bool) dataAPIItem {
	out := dataAPIItem{
		ID:               fmt.Sprintf("%d", item.ID),
		CreatedAt:        item.CreatedAt,
		Kind:             item.Kind,
		ByteLen:          item.ByteLen,
		SourceApp:        strPtrOrNil(item.SourceExeName),
		SourceWindow:     strPtrOrNil(item.SourceWindowTitle),
		Starred:          item.Starred,
		Sensitive:        item.Sensitive,
		ContentAvailable: item.ContentAvailable,
	}
	if groupID > 0 {
		id := fmt.Sprintf("%d", groupID)
		out.GroupID = &id
	}
	if item.Format == "CLIPVAULT_IMAGE_FILE" {
		// 托管图片：不暴露内部路径，内容经 contentUrl 流式获取。
		if item.ContentAvailable {
			out.ContentURL = "/api/v1/items/" + out.ID + "/content"
		}
		return out
	}
	out.SHA256 = item.SHA256
	text := item.Text
	if preview {
		runes := []rune(text)
		if len(runes) > dataAPITextPreviewMax {
			text = string(runes[:dataAPITextPreviewMax])
			out.TextTruncated = true
		}
	}
	out.Text = &text
	return out
}

// ── 端点 ─────────────────────────────────────────────────

// handleHealth 无需 Token：只暴露服务与激活状态，不泄露业务数据。
func (svc *DataAPIService) handleHealth(w http.ResponseWriter, _ *http.Request) {
	activated := svc.activated()
	status := "ok"
	if !activated {
		status = "restricted"
	}
	svc.writeJSON(w, map[string]any{
		"status":     status,
		"apiVersion": "1",
		"apiEnabled": true,
		"activated":  activated,
	})
}

// handleMetadata 描述对外业务数据模型与资源限制（不是数据库 schema），
// 并报告当前 Token 的权限（只读 + 是否可见敏感数据）。
func (svc *DataAPIService) handleMetadata(w http.ResponseWriter, _ *http.Request, token *APIToken) {
	svc.writeJSON(w, map[string]any{
		"apiVersion": "1",
		"resources": map[string]any{
			"clipboardItems": map[string]any{
				"fields": map[string]string{
					"id": "string", "createdAt": "datetime", "kind": "string",
					"text": "string|null", "byteLen": "integer", "sha256": "string",
					"sourceApp": "string|null", "sourceWindow": "string|null",
					"starred": "boolean", "sensitive": "boolean", "groupId": "string|null",
					"contentAvailable": "boolean", "contentUrl": "string",
				},
				"filterable": []string{"id", "createdAt", "kind", "text", "sourceApp", "sourceWindow", "starred", "sensitive", "groupId"},
				"sortable":   []string{"createdAt", "id"},
				"groupable":  []string{"sourceApp", "kind", "starred", "sensitive"},
			},
		},
		"limits": map[string]any{
			"defaultPageSize": dataAPIDefaultLimit,
			"maxPageSize":     dataAPIMaxLimit,
			"queryTimeoutMs":  dataAPIQueryTimeout.Milliseconds(),
			"maxBodyBytes":    dataAPIMaxBodyBytes,
			"maxBatchIds":     dataAPIMaxBatchIDs,
			"textPreviewMax":  dataAPITextPreviewMax,
		},
		"permissions": map[string]any{
			"readOnly":       true,
			"allowSensitive": token.AllowSensitive,
		},
	})
}

// handleQuery 是 V1 核心接口：搜索/过滤/排序/分页/简单聚合。
// DSL 解析在协议层完成；数据读取与敏感可见性由 ExternalReadService 强制。
func (svc *DataAPIService) handleQuery(w http.ResponseWriter, r *http.Request, token *APIToken) {
	started := time.Now()
	audit := auditEventFor(r, token, "query_items", nil)
	defer func() {
		audit.DurationMs = time.Since(started).Milliseconds()
		svc.emitAudit(audit)
	}()
	r.Body = http.MaxBytesReader(w, r.Body, dataAPIMaxBodyBytes)
	body, readErr := io.ReadAll(r.Body)
	// 原始请求体无论解析成败都留档：解析失败时可在访问记录里看到到底发了什么。
	audit.RequestBody = capAuditBody(string(body))
	if readErr != nil {
		apiErr := invalidQuery("Malformed query body.")
		audit.StatusCode, audit.ErrorCode = apiErr.Status, apiErr.Code
		svc.writeError(w, apiErr)
		return
	}
	var req dataAPIQueryRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apiErr := invalidQuery("Malformed query body.")
		audit.StatusCode, audit.ErrorCode = apiErr.Status, apiErr.Code
		svc.writeError(w, apiErr)
		return
	}
	audit.RequestSummary = summarizeDataAPIQuery(&req)
	plan, apiErr := parseDataAPIQuery(&req)
	if apiErr != nil {
		audit.StatusCode, audit.ErrorCode = apiErr.Status, apiErr.Code
		svc.writeError(w, apiErr)
		return
	}
	if plan.aggregate {
		svc.runAggregateQuery(w, r, readAccessFor(token), plan, &audit)
		return
	}
	svc.runItemsQuery(w, r, readAccessFor(token), plan, &audit)
}

// runItemsQuery 执行 keyset 分页条目查询（limit+1 探测 hasMore）。
func (svc *DataAPIService) runItemsQuery(w http.ResponseWriter, r *http.Request, access ReadAccess, plan *dataAPIQueryPlan, audit *APIAuditEvent) {
	items, hasMore, err := svc.readSvc.QueryItems(r.Context(), access, plan)
	if err != nil {
		svc.failQuery(w, r, audit, err)
		return
	}
	ids := make([]int64, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	groups, err := svc.store.ItemGroupIDs(r.Context(), ids)
	if err != nil {
		svc.failQuery(w, r, audit, err)
		return
	}
	out := make([]dataAPIItem, len(items))
	for i, item := range items {
		out[i] = toDataAPIItem(item, groups[item.ID], true)
		audit.SensitiveAccessed = audit.SensitiveAccessed || item.Sensitive
	}
	var nextCursor *string
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		cursor := plan.encodeCursor(last.CreatedAt.UnixMilli(), last.ID)
		nextCursor = &cursor
	}
	audit.StatusCode = http.StatusOK
	audit.ItemCount = len(out)
	svc.writeJSON(w, map[string]any{"items": out, "nextCursor": nextCursor, "hasMore": hasMore})
}

// runAggregateQuery 执行 count 聚合（无游标，limit 封顶）。
func (svc *DataAPIService) runAggregateQuery(w http.ResponseWriter, r *http.Request, access ReadAccess, plan *dataAPIQueryPlan, audit *APIAuditEvent) {
	resultRows, err := svc.readSvc.QueryAggregate(r.Context(), access, plan)
	if err != nil {
		svc.failQuery(w, r, audit, err)
		return
	}
	audit.StatusCode = http.StatusOK
	audit.ItemCount = len(resultRows)
	svc.writeJSON(w, map[string]any{"rows": resultRows})
}

// failQuery 把存储层错误映射为稳定契约（超时→504 语义的 query_timeout，其余 500）。
func (svc *DataAPIService) failQuery(w http.ResponseWriter, r *http.Request, audit *APIAuditEvent, err error) {
	var apiErr *dataAPIError
	switch {
	case errors.Is(err, ErrExternalNotFound):
		apiErr = newDataAPIError(http.StatusNotFound, "not_found", "Item not found.")
	case errors.Is(err, ErrExternalInvalid):
		apiErr = invalidQuery("Invalid request.")
	case errors.Is(err, ErrExternalTooLarge):
		apiErr = newDataAPIError(http.StatusRequestEntityTooLarge, "response_too_large", "The content exceeds the size limit.")
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(r.Context().Err(), context.DeadlineExceeded):
		apiErr = newDataAPIError(http.StatusGatewayTimeout, "query_timeout", "The query timed out.")
	case errors.Is(r.Context().Err(), context.Canceled):
		return // 客户端已断开，无需响应
	default:
		svc.logger.Warn("data_api_query_failed", map[string]any{"error": err.Error()})
		apiErr = newDataAPIError(http.StatusInternalServerError, "internal_error", "Internal server error.")
	}
	audit.StatusCode, audit.ErrorCode = apiErr.Status, apiErr.Code
	svc.writeError(w, apiErr)
}

// handleGetItem 返回单条完整记录（文本不截断）。
func (svc *DataAPIService) handleGetItem(w http.ResponseWriter, r *http.Request, token *APIToken) {
	started := time.Now()
	audit := auditEventFor(r, token, "get_item", nil)
	defer func() {
		audit.DurationMs = time.Since(started).Milliseconds()
		svc.emitAudit(audit)
	}()
	id, apiErr := parseItemID(r)
	if apiErr != nil {
		audit.StatusCode, audit.ErrorCode = apiErr.Status, apiErr.Code
		svc.writeError(w, apiErr)
		return
	}
	item, err := svc.readSvc.GetStoredItem(r.Context(), readAccessFor(token), id)
	if err != nil {
		svc.failQuery(w, r, &audit, err)
		return
	}
	audit.ItemID = item.ID
	groups, err := svc.store.ItemGroupIDs(r.Context(), []int64{item.ID})
	if err != nil {
		svc.failQuery(w, r, &audit, err)
		return
	}
	audit.StatusCode = http.StatusOK
	audit.ItemCount = 1
	audit.SensitiveAccessed = item.Sensitive
	svc.writeJSON(w, toDataAPIItem(item, groups[item.ID], false))
}

// handleBatchGet 批量按 ID 取完整记录（上限 100，保持响应有界）。
func (svc *DataAPIService) handleBatchGet(w http.ResponseWriter, r *http.Request, token *APIToken) {
	started := time.Now()
	audit := auditEventFor(r, token, "batch_get", nil)
	defer func() {
		audit.DurationMs = time.Since(started).Milliseconds()
		svc.emitAudit(audit)
	}()
	r.Body = http.MaxBytesReader(w, r.Body, dataAPIMaxBodyBytes)
	body, readErr := io.ReadAll(r.Body)
	audit.RequestBody = capAuditBody(string(body))
	if readErr != nil {
		apiErr := invalidQuery("Malformed request body.")
		audit.StatusCode, audit.ErrorCode = apiErr.Status, apiErr.Code
		svc.writeError(w, apiErr)
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apiErr := invalidQuery("Malformed request body.")
		audit.StatusCode, audit.ErrorCode = apiErr.Status, apiErr.Code
		svc.writeError(w, apiErr)
		return
	}
	audit.RequestSummary = summarizeDataAPIBatchIDs(req.IDs)
	if len(req.IDs) == 0 || len(req.IDs) > dataAPIMaxBatchIDs {
		apiErr := invalidQuery("ids must contain 1-%d entries.", dataAPIMaxBatchIDs)
		audit.StatusCode, audit.ErrorCode = apiErr.Status, apiErr.Code
		svc.writeError(w, apiErr)
		return
	}
	ids := make([]int64, 0, len(req.IDs))
	for _, text := range req.IDs {
		id, err := strconv.ParseInt(text, 10, 64)
		if err != nil || id <= 0 {
			apiErr := invalidQuery("Invalid item id %q.", text)
			audit.StatusCode, audit.ErrorCode = apiErr.Status, apiErr.Code
			svc.writeError(w, apiErr)
			return
		}
		ids = append(ids, id)
	}
	items, err := svc.readSvc.GetStoredItems(r.Context(), readAccessFor(token), ids)
	if err != nil {
		svc.failQuery(w, r, &audit, err)
		return
	}
	itemIDs := make([]int64, len(items))
	for i, item := range items {
		itemIDs[i] = item.ID
	}
	groups, err := svc.store.ItemGroupIDs(r.Context(), itemIDs)
	if err != nil {
		svc.failQuery(w, r, &audit, err)
		return
	}
	// 保持请求 ID 顺序输出；不存在或未授权敏感的 ID 静默跳过，结果集可小于请求集。
	out := make([]dataAPIItem, 0, len(items))
	for _, item := range items {
		out = append(out, toDataAPIItem(item, groups[item.ID], false))
		audit.SensitiveAccessed = audit.SensitiveAccessed || item.Sensitive
	}
	audit.StatusCode = http.StatusOK
	audit.ItemCount = len(out)
	svc.writeJSON(w, map[string]any{"items": out})
}

// handleItemContent 流式返回二进制内容：托管图片（PNG）或纯文本。
// 大小受限、绑定请求 context、路径防穿越（在共享读取服务内完成）；
// 文件路径记录不提供内容（文件系统边界）。
func (svc *DataAPIService) handleItemContent(w http.ResponseWriter, r *http.Request, token *APIToken) {
	started := time.Now()
	audit := auditEventFor(r, token, "get_content", nil)
	defer func() {
		audit.DurationMs = time.Since(started).Milliseconds()
		svc.emitAudit(audit)
	}()
	id, apiErr := parseItemID(r)
	if apiErr != nil {
		audit.StatusCode, audit.ErrorCode = apiErr.Status, apiErr.Code
		svc.writeError(w, apiErr)
		return
	}
	content, err := svc.readSvc.GetContent(r.Context(), readAccessFor(token), id)
	if err != nil {
		svc.failQuery(w, r, &audit, err)
		return
	}
	audit.ItemID = id
	audit.StatusCode = http.StatusOK
	audit.ItemCount = 1
	if content.Kind == "image" {
		http.ServeFile(w, r, content.ImagePath)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(content.Text))
}

// parseItemID 解析路径参数中的条目 ID。
func parseItemID(r *http.Request) (int64, *dataAPIError) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalidQuery("Invalid item id.")
	}
	return id, nil
}
