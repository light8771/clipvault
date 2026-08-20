package clipvault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP 核心工具（v1 恰好注册以下 7 个，不提供任何写/变更工具）。
// 工具 handler 通过闭包绑定 mcpRequestContext（令牌 + 读取权限 + 审计上下文），
// 可见性策略全部委托 ExternalReadService。
const (
	mcpToolSearch     = "search_clipboard"
	mcpToolGetItem    = "get_clipboard_item"
	mcpToolGetItems   = "get_clipboard_items"
	mcpToolGetContent = "get_clipboard_content"
	mcpToolCount      = "count_clipboard_items"
	mcpToolStats      = "get_clipboard_stats"
	mcpToolGroups     = "list_clipboard_groups"
)

// newSDKServer 构造绑定指定请求权限的 MCP Server（无状态模式每请求一个实例）。
// 工具与资源注册在实例上；同一 SchemaCache 复用推导结果降低每请求开销。
func (svc *MCPService) newSDKServer(reqCtx *mcpRequestContext) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    mcpServerName,
		Version: mcpServerVersionNum,
	}, &mcp.ServerOptions{SchemaCache: svc.schemaCache})
	tc := mcpToolContext{svc: svc, reqCtx: reqCtx}
	registerMCPTools(server, tc)
	registerMCPResources(server, tc)
	return server
}

// mcpToolContext 携带服务与已认证请求上下文。
type mcpToolContext struct {
	svc    *MCPService
	reqCtx *mcpRequestContext
}

// registerMCPTools 注册 7 个核心只读工具（输入 Schema 显式声明，含
// minimum/maximum/enum/pattern 约束与 additionalProperties=false）。
func registerMCPTools(server *mcp.Server, tc mcpToolContext) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolSearch,
		Description: "Search previously copied clipboard content (text, images, files) recorded by ClipVault. Use it to find prior copies; prefer narrow from/to date ranges and sourceApp filters when the user provides them. Results are read-only; whether sensitive entries appear depends on the access token authorization.",
		InputSchema: mcpSearchInputSchema(),
	}, tc.search)

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolGetItem,
		Description: "Get one clipboard record by numeric id, including full metadata and complete text. Unknown or unauthorized ids behave as not found.",
		InputSchema: mcpIDInputSchema(),
	}, tc.getItem)

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolGetItems,
		Description: "Get multiple clipboard records by numeric ids (at most 100). Output preserves the request order; unknown or unauthorized ids are silently omitted.",
		InputSchema: mcpIDsInputSchema(),
	}, tc.getItems)

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolGetContent,
		Description: "Get the content of one clipboard record: text content for text items, image data (PNG) for captured images. File-path records have no servable content. Unknown or unauthorized ids behave as not found.",
		InputSchema: mcpIDInputSchema(),
	}, tc.getContent)

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolCount,
		Description: "Count clipboard records matching the given filters. Counts obey the same visibility rules as search.",
		InputSchema: mcpCountInputSchema(),
	}, tc.count)

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolStats,
		Description: "Structured statistics over clipboard records (totals, bytes, by kind, by source app, time trend) for reasoning or charting. The server never renders charts; statistics obey visibility rules.",
		InputSchema: mcpStatsInputSchema(),
	}, tc.stats)

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolGroups,
		Description: "List clipboard groups with visible item counts. Counts exclude entries the token cannot see.",
		InputSchema: mcpGroupsInputSchema(),
	}, tc.groups)
}

// ── 工具输入结构（类型化解码；Schema 约束见上方构造函数）────

type mcpSearchInput struct {
	Query        string   `json:"query,omitempty"`
	Kind         []string `json:"kind,omitempty"`
	SourceApp    string   `json:"sourceApp,omitempty"`
	SourceWindow string   `json:"sourceWindow,omitempty"`
	Starred      *bool    `json:"starred,omitempty"`
	Sensitive    *bool    `json:"sensitive,omitempty"`
	GroupID      string   `json:"groupId,omitempty"`
	From         string   `json:"from,omitempty"`
	To           string   `json:"to,omitempty"`
	Limit        int      `json:"limit,omitempty"`
	Cursor       string   `json:"cursor,omitempty"`
}

func (in *mcpSearchInput) toCountRequest() (CountRequest, error) {
	req := CountRequest{
		Query:        in.Query,
		Kinds:        in.Kind,
		SourceApp:    in.SourceApp,
		SourceWindow: in.SourceWindow,
		Starred:      in.Starred,
		Sensitive:    in.Sensitive,
		GroupID:      in.GroupID,
	}
	var err error
	if req.From, err = parseOptionalRFC3339(in.From); err != nil {
		return CountRequest{}, err
	}
	if req.To, err = parseOptionalRFC3339(in.To); err != nil {
		return CountRequest{}, err
	}
	return req, nil
}

func parseOptionalRFC3339(text string) (*time.Time, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return nil, fmt.Errorf("invalid datetime %q: expected RFC3339", text)
	}
	return &parsed, nil
}

type mcpIDInput struct {
	ID string `json:"id"`
}

type mcpIDsInput struct {
	IDs []string `json:"ids"`
}

type mcpStatsInput struct {
	From          string `json:"from,omitempty"`
	To            string `json:"to,omitempty"`
	Bucket        string `json:"bucket,omitempty"`
	TopSourceApps int    `json:"topSourceApps,omitempty"`
}

type mcpGroupsInput struct {
	IncludeCounts bool `json:"includeCounts,omitempty"`
}

// ── 显式输入 Schema（与规范 JSON Schema 对齐）──────────────

// falseSchema 渲染为布尔 schema false（additionalProperties:false）。
func falseSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Not: &jsonschema.Schema{}}
}

func numPtr(v float64) *float64 { return &v }
func intPtr(v int) *int         { return &v }

// mcpFilterProperties 是 search/count 共用的过滤参数 Schema。
func mcpFilterProperties() map[string]*jsonschema.Schema {
	return map[string]*jsonschema.Schema{
		"query":        {Type: "string", Description: "Substring to match against item text", Default: json.RawMessage(`""`)},
		"kind":         {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Content kinds to include (text, image, video, archive, office, files, other)"},
		"sourceApp":    {Type: "string", Description: "Exact source application name (case-insensitive), e.g. chrome.exe", Default: json.RawMessage(`""`)},
		"sourceWindow": {Type: "string", Description: "Substring to match against the source window title", Default: json.RawMessage(`""`)},
		"starred":      {Types: []string{"boolean", "null"}, Description: "Filter by starred flag"},
		"sensitive":    {Types: []string{"boolean", "null"}, Description: "Filter by sensitive flag; sensitive=true requires token authorization"},
		"groupId":      {Type: "string", Description: "Only items belonging to this group id", Default: json.RawMessage(`""`)},
		"from":         {Type: "string", Format: "date-time", Description: "RFC3339 lower bound on createdAt"},
		"to":           {Type: "string", Format: "date-time", Description: "RFC3339 upper bound on createdAt"},
	}
}

func mcpSearchInputSchema() *jsonschema.Schema {
	properties := mcpFilterProperties()
	properties["limit"] = &jsonschema.Schema{Type: "integer", Minimum: numPtr(1), Maximum: numPtr(500), Default: json.RawMessage(`50`)}
	properties["cursor"] = &jsonschema.Schema{Type: "string", Description: "Opaque pagination cursor from a previous response", Default: json.RawMessage(`""`)}
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		AdditionalProperties: falseSchema(),
	}
}

func mcpCountInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           mcpFilterProperties(),
		AdditionalProperties: falseSchema(),
	}
}

func mcpIDInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"id": {Type: "string", Pattern: `^[1-9][0-9]*$`},
		},
		Required:             []string{"id"},
		AdditionalProperties: falseSchema(),
	}
}

func mcpIDsInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"ids": {
				Type:     "array",
				Items:    &jsonschema.Schema{Type: "string", Pattern: `^[1-9][0-9]*$`},
				MinItems: intPtr(1),
				MaxItems: intPtr(mcpMaxBatchIDs),
			},
		},
		Required:             []string{"ids"},
		AdditionalProperties: falseSchema(),
	}
}

func mcpStatsInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"from":          {Type: "string", Format: "date-time"},
			"to":            {Type: "string", Format: "date-time"},
			"bucket":        {Type: "string", Enum: []any{"hour", "day"}, Default: json.RawMessage(`"day"`)},
			"topSourceApps": {Type: "integer", Minimum: numPtr(1), Maximum: numPtr(50), Default: json.RawMessage(`20`)},
		},
		AdditionalProperties: falseSchema(),
	}
}

func mcpGroupsInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"includeCounts": {Type: "boolean", Default: json.RawMessage(`true`)},
		},
		AdditionalProperties: falseSchema(),
	}
}

// mcpCountResult 是 count 工具的输出。
type mcpCountResult struct {
	Count int64 `json:"count"`
}

// mcpItemsResult 是批量工具的输出。
type mcpItemsResult struct {
	Items []ExternalItem `json:"items"`
}

// mcpGroupsResult 是分组列表工具的输出。
type mcpGroupsResult struct {
	Groups []ExternalGroup `json:"groups"`
}

// ── 工具实现 ──────────────────────────────────────────────

// mcpToolAudit 汇聚一次工具调用的审计信息。
type mcpToolAudit struct {
	Operation   string
	ResourceURI string
	ItemID      int64
	ItemCount   int
	Sensitive   bool
}

// emit 在工具结束时写审计（tool:<name>，含资源 URI 与敏感访问标记）。
func (tc mcpToolContext) emit(audit mcpToolAudit, started time.Time, statusCode int, errCode string) {
	event := APIAuditEvent{
		Timestamp:         time.Now().UTC(),
		Channel:           TokenChannelMCP,
		TokenID:           tc.reqCtx.token.ID,
		TokenName:         truncateRunes(tc.reqCtx.token.Name, 60),
		RemoteIP:          tc.reqCtx.remoteIP,
		Method:            http.MethodPost,
		Endpoint:          mcpEndpointPath,
		Operation:         audit.Operation,
		ResourceURI:       audit.ResourceURI,
		SensitiveAccessed: audit.Sensitive,
		ItemID:            audit.ItemID,
		ItemCount:         audit.ItemCount,
		DurationMs:        time.Since(started).Milliseconds(),
		StatusCode:        statusCode,
		ErrorCode:         errCode,
	}
	tc.svc.emitAudit(event)
}

// toolError 把共享服务错误映射为受控的工具错误（不泄露 DB 细节或敏感存在性）。
func toolError(err error) error {
	switch {
	case errors.Is(err, ErrExternalNotFound):
		return errors.New("not found")
	case errors.Is(err, ErrExternalInvalid):
		return fmt.Errorf("invalid request: %s", sanitizeToolError(err))
	case errors.Is(err, ErrExternalTooLarge):
		return errors.New("the content exceeds the size limit")
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("the query timed out")
	default:
		return errors.New("internal error")
	}
}

// sanitizeToolError 剥离哨兵错误前缀，只保留受控的说明文本。
func sanitizeToolError(err error) string {
	text := err.Error()
	if idx := strings.Index(text, "external: "); idx >= 0 {
		text = text[idx+len("external: "):]
	}
	return text
}

// toolTimeout 给 handler 补充查询超时（即使协议层上下文未传播也生效）。
func toolTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, mcpQueryTimeout)
}

// search 实现 search_clipboard。
func (tc mcpToolContext) search(ctx context.Context, _ *mcp.CallToolRequest, in mcpSearchInput) (*mcp.CallToolResult, SearchResult, error) {
	started := time.Now()
	audit := mcpToolAudit{Operation: "tool:" + mcpToolSearch}
	countReq, err := in.toCountRequest()
	if err != nil {
		tc.emit(audit, started, 400, "invalid_request")
		return nil, SearchResult{}, fmt.Errorf("invalid request: %w", err)
	}
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	result, err := tc.svc.readSvc.Search(ctx, tc.reqCtx.access, SearchRequest{CountRequest: countReq, Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		tc.emit(audit, started, 500, "internal_error")
		return nil, SearchResult{}, toolError(err)
	}
	audit.ItemCount = len(result.Items)
	for _, item := range result.Items {
		audit.Sensitive = audit.Sensitive || item.Sensitive
	}
	tc.emit(audit, started, 200, "")
	return nil, result, nil
}

// getItem 实现 get_clipboard_item。
func (tc mcpToolContext) getItem(ctx context.Context, _ *mcp.CallToolRequest, in mcpIDInput) (*mcp.CallToolResult, ExternalItem, error) {
	started := time.Now()
	id, err := strconv.ParseInt(strings.TrimSpace(in.ID), 10, 64)
	audit := mcpToolAudit{Operation: "tool:" + mcpToolGetItem}
	if err != nil || id <= 0 {
		tc.emit(audit, started, 400, "invalid_request")
		return nil, ExternalItem{}, errors.New("invalid request: id must be a positive integer")
	}
	audit.ItemID = id
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	item, err := tc.svc.readSvc.GetItem(ctx, tc.reqCtx.access, id)
	if err != nil {
		tc.emit(audit, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err))
		return nil, ExternalItem{}, toolError(err)
	}
	audit.ItemCount = 1
	audit.Sensitive = item.Sensitive
	tc.emit(audit, started, 200, "")
	return nil, item, nil
}

// getItems 实现 get_clipboard_items。
func (tc mcpToolContext) getItems(ctx context.Context, _ *mcp.CallToolRequest, in mcpIDsInput) (*mcp.CallToolResult, mcpItemsResult, error) {
	started := time.Now()
	audit := mcpToolAudit{Operation: "tool:" + mcpToolGetItems}
	if len(in.IDs) == 0 || len(in.IDs) > mcpMaxBatchIDs {
		tc.emit(audit, started, 400, "invalid_request")
		return nil, mcpItemsResult{}, fmt.Errorf("invalid request: ids must contain 1-%d entries", mcpMaxBatchIDs)
	}
	ids := make([]int64, 0, len(in.IDs))
	for _, text := range in.IDs {
		id, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil || id <= 0 {
			tc.emit(audit, started, 400, "invalid_request")
			return nil, mcpItemsResult{}, errors.New("invalid request: every id must be a positive integer")
		}
		ids = append(ids, id)
	}
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	items, err := tc.svc.readSvc.GetItems(ctx, tc.reqCtx.access, ids)
	if err != nil {
		tc.emit(audit, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err))
		return nil, mcpItemsResult{}, toolError(err)
	}
	audit.ItemCount = len(items)
	for _, item := range items {
		audit.Sensitive = audit.Sensitive || item.Sensitive
	}
	tc.emit(audit, started, 200, "")
	return nil, mcpItemsResult{Items: items}, nil
}

// getContent 实现 get_clipboard_content：文本返回 text 内容块，
// 托管图片返回 image 内容块（PNG）；其余 not found。
func (tc mcpToolContext) getContent(ctx context.Context, _ *mcp.CallToolRequest, in mcpIDInput) (*mcp.CallToolResult, any, error) {
	started := time.Now()
	id, err := strconv.ParseInt(strings.TrimSpace(in.ID), 10, 64)
	audit := mcpToolAudit{Operation: "tool:" + mcpToolGetContent}
	if err != nil || id <= 0 {
		tc.emit(audit, started, 400, "invalid_request")
		return nil, nil, errors.New("invalid request: id must be a positive integer")
	}
	audit.ItemID = id
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	content, err := tc.svc.readSvc.GetContent(ctx, tc.reqCtx.access, id)
	if err != nil {
		tc.emit(audit, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err))
		return nil, nil, toolError(err)
	}
	audit.ItemCount = 1
	audit.Sensitive = content.Sensitive
	result := &mcp.CallToolResult{Content: []mcp.Content{}}
	switch content.Kind {
	case "image":
		data, readErr := os.ReadFile(content.ImagePath)
		if readErr != nil {
			tc.emit(audit, started, 500, "internal_error")
			return nil, nil, errors.New("internal error")
		}
		result.Content = append(result.Content, &mcp.ImageContent{Data: data, MIMEType: content.MimeType})
	case "text":
		result.Content = append(result.Content, &mcp.TextContent{Text: content.Text})
	default:
		tc.emit(audit, started, 404, "not_found")
		return nil, nil, errors.New("not found")
	}
	tc.emit(audit, started, 200, "")
	return result, nil, nil
}

// count 实现 count_clipboard_items。
func (tc mcpToolContext) count(ctx context.Context, _ *mcp.CallToolRequest, in mcpSearchInput) (*mcp.CallToolResult, mcpCountResult, error) {
	started := time.Now()
	audit := mcpToolAudit{Operation: "tool:" + mcpToolCount}
	countReq, err := in.toCountRequest()
	if err != nil {
		tc.emit(audit, started, 400, "invalid_request")
		return nil, mcpCountResult{}, fmt.Errorf("invalid request: %w", err)
	}
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	count, err := tc.svc.readSvc.Count(ctx, tc.reqCtx.access, countReq)
	if err != nil {
		tc.emit(audit, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err))
		return nil, mcpCountResult{}, toolError(err)
	}
	tc.emit(audit, started, 200, "")
	return nil, mcpCountResult{Count: count}, nil
}

// stats 实现 get_clipboard_stats。
func (tc mcpToolContext) stats(ctx context.Context, _ *mcp.CallToolRequest, in mcpStatsInput) (*mcp.CallToolResult, StatsResult, error) {
	started := time.Now()
	audit := mcpToolAudit{Operation: "tool:" + mcpToolStats}
	req := StatsRequest{Bucket: in.Bucket, TopSourceApps: in.TopSourceApps}
	var err error
	if req.From, err = parseOptionalRFC3339(in.From); err != nil {
		tc.emit(audit, started, 400, "invalid_request")
		return nil, StatsResult{}, fmt.Errorf("invalid request: %w", err)
	}
	if req.To, err = parseOptionalRFC3339(in.To); err != nil {
		tc.emit(audit, started, 400, "invalid_request")
		return nil, StatsResult{}, fmt.Errorf("invalid request: %w", err)
	}
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	result, err := tc.svc.readSvc.Stats(ctx, tc.reqCtx.access, req)
	if err != nil {
		tc.emit(audit, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err))
		return nil, StatsResult{}, toolError(err)
	}
	tc.emit(audit, started, 200, "")
	return nil, result, nil
}

// groups 实现 list_clipboard_groups。
func (tc mcpToolContext) groups(ctx context.Context, _ *mcp.CallToolRequest, in mcpGroupsInput) (*mcp.CallToolResult, mcpGroupsResult, error) {
	started := time.Now()
	audit := mcpToolAudit{Operation: "tool:" + mcpToolGroups}
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	groups, err := tc.svc.readSvc.ListGroups(ctx, tc.reqCtx.access, in.IncludeCounts)
	if err != nil {
		tc.emit(audit, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err))
		return nil, mcpGroupsResult{}, toolError(err)
	}
	tc.emit(audit, started, 200, "")
	return nil, mcpGroupsResult{Groups: groups}, nil
}

// mcpErrorStatusFor 把共享服务错误映射为审计状态码。
func mcpErrorStatusFor(err error) int {
	switch {
	case errors.Is(err, ErrExternalNotFound):
		return 404
	case errors.Is(err, ErrExternalInvalid):
		return 400
	case errors.Is(err, ErrExternalTooLarge):
		return 413
	case errors.Is(err, context.DeadlineExceeded):
		return 504
	default:
		return 500
	}
}

// mcpErrorCodeFor 把共享服务错误映射为审计错误码。
func mcpErrorCodeFor(err error) string {
	switch {
	case errors.Is(err, ErrExternalNotFound):
		return "not_found"
	case errors.Is(err, ErrExternalInvalid):
		return "invalid_request"
	case errors.Is(err, ErrExternalTooLarge):
		return "response_too_large"
	case errors.Is(err, context.DeadlineExceeded):
		return "query_timeout"
	default:
		return "internal_error"
	}
}
