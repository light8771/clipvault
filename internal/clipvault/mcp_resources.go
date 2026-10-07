package clipvault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP 资源 URI（RFC 6570 模板；只读，未授权敏感条目与不存在条目不可区分）。
const (
	mcpResourceItemPrefix         = "clipvault://items"
	mcpResourceItemsTemplate      = "clipvault://items/{id}"
	mcpResourceContentTemplate    = "clipvault://items/{id}/content"
	mcpResourceRecentURI          = "clipvault://recent"
	mcpResourceStarredURI         = "clipvault://starred"
	mcpResourceGroupsTemplate     = "clipvault://groups/{id}"
	mcpResourceGroupItemsTemplate = "clipvault://groups/{id}/items"
)

// mcpResourceCollectionLimit 是集合资源（recent/starred/group items）的固定条数。
const mcpResourceCollectionLimit = 50

// mcpResourceNotFound 返回资源不存在的 JSON-RPC 错误。
// 使用服务器自定义错误码 -32002（go-sdk 1.7.0 之前的 ResourceNotFoundError
// 默认码）：在 2026-07-28 协议下，-32602 会被 SEP-2575 映射为 HTTP 400，
// 而 go-sdk 客户端会把 4xx 视为终态错误并断开会话；-32002 以 HTTP 200
// 携带 JSON-RPC 错误体返回，客户端获得干净错误且会话保持可用。
// 未授权敏感条目与不存在条目返回完全相同的错误（不可区分，SEC-02）。
func mcpResourceNotFound(uri string) error {
	return &jsonrpc.Error{
		Code:    -32002,
		Message: "Resource not found",
		Data:    json.RawMessage(fmt.Sprintf(`{"uri":%q}`, uri)),
	}
}

// registerMCPResources 注册资源模板与静态集合资源。
func registerMCPResources(server *mcp.Server, tc mcpToolContext) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: mcpResourceItemsTemplate,
		Name:        "clipboard-item",
		Description: "A single clipboard record with full metadata.",
		MIMEType:    "application/json",
	}, tc.readItemResource)

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: mcpResourceContentTemplate,
		Name:        "clipboard-item-content",
		Description: "The content of a clipboard record (text or PNG image data).",
	}, tc.readItemContentResource)

	server.AddResource(&mcp.Resource{
		URI:         mcpResourceRecentURI,
		Name:        "clipboard-recent",
		Description: "The latest clipboard records.",
		MIMEType:    "application/json",
	}, tc.readRecentResource)

	server.AddResource(&mcp.Resource{
		URI:         mcpResourceStarredURI,
		Name:        "clipboard-starred",
		Description: "The latest starred clipboard records.",
		MIMEType:    "application/json",
	}, tc.readStarredResource)

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: mcpResourceGroupsTemplate,
		Name:        "clipboard-group",
		Description: "A clipboard group with its visible item count.",
		MIMEType:    "application/json",
	}, tc.readGroupResource)

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: mcpResourceGroupItemsTemplate,
		Name:        "clipboard-group-items",
		Description: "The latest clipboard records belonging to a group.",
		MIMEType:    "application/json",
	}, tc.readGroupItemsResource)
}

// emitResource 在资源读取结束时写审计（resource:<uri>）。
func (tc mcpToolContext) emitResource(uri string, started time.Time, statusCode int, errCode string, itemCount int, sensitive bool) {
	event := APIAuditEvent{
		Timestamp:         time.Now().UTC(),
		Channel:           TokenChannelMCP,
		TokenID:           tc.reqCtx.token.ID,
		TokenName:         truncateRunes(tc.reqCtx.token.Name, 60),
		RemoteIP:          tc.reqCtx.remoteIP,
		Method:            http.MethodPost,
		Endpoint:          mcpEndpointPath,
		Operation:         "resource:" + uri,
		ResourceURI:       uri,
		SensitiveAccessed: sensitive,
		ItemCount:         itemCount,
		DurationMs:        time.Since(started).Milliseconds(),
		StatusCode:        statusCode,
		ErrorCode:         errCode,
	}
	tc.svc.emitAudit(event)
}

// jsonResourceContents 把任意 JSON 值编码为资源内容。
func jsonResourceContents(uri string, value any) ([]*mcp.ResourceContents, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(data)}}, nil
}

// readItemResource 处理 clipvault://items/{id}。
func (tc mcpToolContext) readItemResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	started := time.Now()
	id, err := parseMCPItemURI(uri)
	if err != nil {
		tc.emitResource(uri, started, 400, "invalid_request", 0, false)
		return nil, mcpResourceNotFound(uri)
	}
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	item, err := tc.svc.readSvc.GetItem(ctx, tc.reqCtx.access, id)
	if err != nil {
		tc.emitResource(uri, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err), 0, false)
		return nil, mcpResourceNotFound(uri)
	}
	contents, err := jsonResourceContents(uri, item)
	if err != nil {
		tc.emitResource(uri, started, 500, "internal_error", 0, false)
		return nil, errors.New("internal error")
	}
	tc.emitResource(uri, started, 200, "", 1, item.Sensitive)
	return &mcp.ReadResourceResult{Contents: contents}, nil
}

// readItemContentResource 处理 clipvault://items/{id}/content。
func (tc mcpToolContext) readItemContentResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	started := time.Now()
	id, err := parseMCPItemContentURI(uri)
	if err != nil {
		tc.emitResource(uri, started, 400, "invalid_request", 0, false)
		return nil, mcpResourceNotFound(uri)
	}
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	content, err := tc.svc.readSvc.GetContent(ctx, tc.reqCtx.access, id)
	if err != nil {
		tc.emitResource(uri, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err), 0, false)
		return nil, mcpResourceNotFound(uri)
	}
	var contents []*mcp.ResourceContents
	switch content.Kind {
	case "image":
		data, readErr := os.ReadFile(content.ImagePath)
		if readErr != nil {
			tc.emitResource(uri, started, 500, "internal_error", 0, false)
			return nil, errors.New("internal error")
		}
		contents = []*mcp.ResourceContents{{URI: uri, MIMEType: content.MimeType, Blob: data}}
	case "text", "composite":
		contents = []*mcp.ResourceContents{{URI: uri, MIMEType: content.MimeType, Text: content.Text}}
	default:
		tc.emitResource(uri, started, 404, "not_found", 0, false)
		return nil, mcpResourceNotFound(uri)
	}
	tc.emitResource(uri, started, 200, "", 1, content.Sensitive)
	return &mcp.ReadResourceResult{Contents: contents}, nil
}

// readRecentResource 处理 clipvault://recent（最新 50 条可见记录）。
func (tc mcpToolContext) readRecentResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return tc.readCollectionResource(ctx, req, mcpResourceRecentURI, CountRequest{})
}

// readStarredResource 处理 clipvault://starred（最新 50 条可见加星记录）。
func (tc mcpToolContext) readStarredResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	starred := true
	return tc.readCollectionResource(ctx, req, mcpResourceStarredURI, CountRequest{Starred: &starred})
}

// readCollectionResource 是 recent/starred 集合资源的共享实现。
func (tc mcpToolContext) readCollectionResource(ctx context.Context, _ *mcp.ReadResourceRequest, uri string, filter CountRequest) (*mcp.ReadResourceResult, error) {
	started := time.Now()
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	result, err := tc.svc.readSvc.Search(ctx, tc.reqCtx.access, SearchRequest{CountRequest: filter, Limit: mcpResourceCollectionLimit})
	if err != nil {
		tc.emitResource(uri, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err), 0, false)
		return nil, errors.New("internal error")
	}
	sensitive := false
	for _, item := range result.Items {
		sensitive = sensitive || item.Sensitive
	}
	contents, err := jsonResourceContents(uri, result.Items)
	if err != nil {
		tc.emitResource(uri, started, 500, "internal_error", 0, false)
		return nil, errors.New("internal error")
	}
	tc.emitResource(uri, started, 200, "", len(result.Items), sensitive)
	return &mcp.ReadResourceResult{Contents: contents}, nil
}

// readGroupResource 处理 clipvault://groups/{id}。
func (tc mcpToolContext) readGroupResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	started := time.Now()
	groupID, err := parseMCPGroupURI(uri)
	if err != nil {
		tc.emitResource(uri, started, 400, "invalid_request", 0, false)
		return nil, mcpResourceNotFound(uri)
	}
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	group, err := tc.svc.readSvc.GetGroup(ctx, tc.reqCtx.access, groupID)
	if err != nil {
		tc.emitResource(uri, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err), 0, false)
		return nil, mcpResourceNotFound(uri)
	}
	contents, err := jsonResourceContents(uri, group)
	if err != nil {
		tc.emitResource(uri, started, 500, "internal_error", 0, false)
		return nil, errors.New("internal error")
	}
	tc.emitResource(uri, started, 200, "", 1, false)
	return &mcp.ReadResourceResult{Contents: contents}, nil
}

// readGroupItemsResource 处理 clipvault://groups/{id}/items（最新 50 条可见分组条目）。
func (tc mcpToolContext) readGroupItemsResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	started := time.Now()
	groupID, err := parseMCPGroupItemsURI(uri)
	if err != nil {
		tc.emitResource(uri, started, 400, "invalid_request", 0, false)
		return nil, mcpResourceNotFound(uri)
	}
	ctx, cancel := toolTimeout(ctx)
	defer cancel()
	result, err := tc.svc.readSvc.ListGroupItems(ctx, tc.reqCtx.access, groupID, mcpResourceCollectionLimit, "")
	if err != nil {
		tc.emitResource(uri, started, mcpErrorStatusFor(err), mcpErrorCodeFor(err), 0, false)
		return nil, mcpResourceNotFound(uri)
	}
	sensitive := false
	for _, item := range result.Items {
		sensitive = sensitive || item.Sensitive
	}
	contents, err := jsonResourceContents(uri, result.Items)
	if err != nil {
		tc.emitResource(uri, started, 500, "internal_error", 0, false)
		return nil, errors.New("internal error")
	}
	tc.emitResource(uri, started, 200, "", len(result.Items), sensitive)
	return &mcp.ReadResourceResult{Contents: contents}, nil
}

// ── clipvault:// URI 解析 ─────────────────────────────────

// parseMCPURI 解析 clipvault:// URI，返回 host 与 path 段（已去空）。
func parseMCPURI(uri string) (string, []string, error) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "clipvault" || parsed.Host == "" {
		return "", nil, fmt.Errorf("%w: invalid clipvault URI %q", ErrExternalInvalid, uri)
	}
	segments := []string{}
	for _, segment := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return parsed.Host, segments, nil
}

// parseMCPItemURI 解析 clipvault://items/{id}。
func parseMCPItemURI(uri string) (int64, error) {
	host, segments, err := parseMCPURI(uri)
	if err != nil {
		return 0, err
	}
	if host != "items" || len(segments) != 1 {
		return 0, fmt.Errorf("%w: expected clipvault://items/{id}", ErrExternalInvalid)
	}
	id, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: invalid item id %q", ErrExternalInvalid, segments[0])
	}
	return id, nil
}

// parseMCPItemContentURI 解析 clipvault://items/{id}/content。
func parseMCPItemContentURI(uri string) (int64, error) {
	host, segments, err := parseMCPURI(uri)
	if err != nil {
		return 0, err
	}
	if host != "items" || len(segments) != 2 || segments[1] != "content" {
		return 0, fmt.Errorf("%w: expected clipvault://items/{id}/content", ErrExternalInvalid)
	}
	id, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: invalid item id %q", ErrExternalInvalid, segments[0])
	}
	return id, nil
}

// parseMCPGroupURI 解析 clipvault://groups/{id}。
func parseMCPGroupURI(uri string) (string, error) {
	host, segments, err := parseMCPURI(uri)
	if err != nil {
		return "", err
	}
	if host != "groups" || len(segments) != 1 {
		return "", fmt.Errorf("%w: expected clipvault://groups/{id}", ErrExternalInvalid)
	}
	return segments[0], nil
}

// parseMCPGroupItemsURI 解析 clipvault://groups/{id}/items。
func parseMCPGroupItemsURI(uri string) (string, error) {
	host, segments, err := parseMCPURI(uri)
	if err != nil {
		return "", err
	}
	if host != "groups" || len(segments) != 2 || segments[1] != "items" {
		return "", fmt.Errorf("%w: expected clipvault://groups/{id}/items", ErrExternalInvalid)
	}
	return segments[0], nil
}
