package clipvault

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"clipboard_ext/internal/clipvault/domain"
)

// bearerTransport 给 MCP 客户端请求附加 Bearer Token。
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// mcpTestFixture 是一个已启动的 MCP 服务、固定数据集与有效 MCP Token。
type mcpTestFixture struct {
	store        *Store
	svc          *MCPService
	base         string
	token        string
	grantedToken string
	normalID     int64
	sensitiveID  int64
	groupID      int64
}

func startMCPTestService(t *testing.T, items ...ClipboardItem) *mcpTestFixture {
	t.Helper()
	store := openDataAPITestStore(t)
	ctx := context.Background()
	items = append(items,
		ClipboardItem{RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "mcp normal", SourceExeName: "Code.exe"},
		ClipboardItem{RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "mcp secret"},
	)
	for _, item := range items {
		if err := store.Save(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	env, err := newEnvironment(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 托管图片记录：直接落盘一个伪 PNG + 插入 image 条目。
	imageDir := env.ImagesDirectory()
	if err := os.MkdirAll(imageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pngPath := filepath.Join(imageDir, "mcp-test.png")
	pngBytes := []byte("\x89PNG-test-image-bytes")
	if err := os.WriteFile(pngPath, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO clipboard_items
		(created_at, record_type, kind, format, text, byte_len, char_len, content_sha256, meta_json)
		VALUES (?, 'content', 'image', 'CLIPVAULT_IMAGE_FILE', 'images/mcp-test.png', ?, 0, ?, '{}')`,
		time.Now().UTC().UnixMilli(), len(pngBytes), hashBytes(pngBytes)); err != nil {
		t.Fatal(err)
	}
	// 最近 6 条按 id 升序：普通、敏感、图片。
	var normalID, sensitiveID int64
	rows, err := store.db.QueryContext(ctx, `SELECT id, text FROM clipboard_items ORDER BY id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			t.Fatal(err)
		}
		switch text {
		case "mcp normal":
			normalID = id
		case "mcp secret":
			sensitiveID = id
		}
	}
	rows.Close()
	if _, err := store.db.ExecContext(ctx,
		`UPDATE clipboard_items SET sensitive = true WHERE id = ?`, sensitiveID); err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup(ctx, "MCP 组", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddItemToGroup(ctx, normalID, group.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.AddItemToGroup(ctx, sensitiveID, group.ID); err != nil {
		t.Fatal(err)
	}

	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	svc := NewMCPService(nil, NewExternalReadService(store, env), store, logger, nil, env)
	svc.activatedFn = func() bool { return true }
	t.Cleanup(svc.Close)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	cfg := DefaultConfig()
	cfg.MCP.Enabled = true
	cfg.MCP.ListenAddr = "127.0.0.1:" + strconv.Itoa(port)
	if err := svc.ApplyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if status := svc.Status(); !status.Running {
		t.Fatalf("mcp service not running: %+v", status)
	}
	raw, hash, err := generateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIToken(ctx, "mcp-e2e", hash, 0, TokenChannelMCP, false); err != nil {
		t.Fatal(err)
	}
	grantedRaw, grantedHash, err := generateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIToken(ctx, "mcp-granted", grantedHash, 0, TokenChannelMCP, true); err != nil {
		t.Fatal(err)
	}
	return &mcpTestFixture{
		store:        store,
		svc:          svc,
		base:         "http://127.0.0.1:" + strconv.Itoa(port),
		token:        raw,
		grantedToken: grantedRaw,
		normalID:     normalID,
		sensitiveID:  sensitiveID,
		groupID:      group.ID,
	}
}

// connect 建立一个带 Bearer Token 的真实 MCP 客户端会话。
func (f *mcpTestFixture) connect(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:             f.base + mcpEndpointPath,
		HTTPClient:           &http.Client{Transport: &bearerTransport{token: token}, Timeout: 10 * time.Second},
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-test-client", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect mcp client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func (f *mcpTestFixture) callTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call tool %s: %v", name, err)
	}
	return result
}

// TestMCPServerDisabledByDefault 默认配置下 MCP 服务不监听。
func TestMCPServerDisabledByDefault(t *testing.T) {
	store := openDataAPITestStore(t)
	env, err := newEnvironment(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	svc := NewMCPService(nil, nil, store, logger, nil, env)
	t.Cleanup(svc.Close)
	if err := svc.ApplyConfig(DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if svc.Status().Running {
		t.Fatal("mcp must be disabled by default")
	}
}

func TestMCPStopClearsInactiveBindFailure(t *testing.T) {
	svc := &MCPService{running: false, listen: "127.0.0.1:1", bindError: "address in use"}
	svc.stopLocked()
	if svc.running || svc.listen != "" || svc.bindError != "" {
		t.Fatalf("stopped state retained runtime fields: running=%v listen=%q bindError=%q", svc.running, svc.listen, svc.bindError)
	}
}

// TestMCPTokenRequiresMCPChannel S7：HTTP 通道令牌不能调用 MCP。
func TestMCPTokenRequiresMCPChannel(t *testing.T) {
	fx := startMCPTestService(t)
	raw, hash, err := generateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.CreateAPIToken(context.Background(), "http-token", hash, 0, TokenChannelHTTP, false); err != nil {
		t.Fatal(err)
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:             fx.base + mcpEndpointPath,
		HTTPClient:           &http.Client{Transport: &bearerTransport{token: raw}, Timeout: 5 * time.Second},
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "http-token-client", Version: "1"}, nil)
	if _, err := client.Connect(context.Background(), transport, nil); err == nil {
		t.Fatal("http channel token must not authenticate MCP")
	}
	// 无 Token 同样拒绝。
	resp, err := http.Post(fx.base+mcpEndpointPath, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token must be 401, got %d", resp.StatusCode)
	}
}

// TestMCPToolDiscoveryExactCoreSet 工具发现恰好是 7 个核心只读工具。
func TestMCPToolDiscoveryExactCoreSet(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		got = append(got, tool.Name)
	}
	want := mcpCoreToolNames()
	sort.Strings(got)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tools=%v, want %v", got, want)
	}
	for _, name := range got {
		if strings.HasPrefix(name, "create_") || strings.HasPrefix(name, "delete_") || strings.HasPrefix(name, "update_") || strings.HasPrefix(name, "write_") {
			t.Fatalf("write tool %q must not exist", name)
		}
	}
}

// TestMCPResourceTemplateDiscovery 资源模板与静态资源可发现。
func TestMCPResourceTemplateDiscovery(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	templates, err := session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotTemplates := map[string]bool{}
	for _, template := range templates.ResourceTemplates {
		gotTemplates[template.URITemplate] = true
	}
	for _, want := range []string{mcpResourceItemsTemplate, mcpResourceContentTemplate, mcpResourceGroupsTemplate, mcpResourceGroupItemsTemplate} {
		if !gotTemplates[want] {
			t.Fatalf("missing resource template %q in %v", want, gotTemplates)
		}
	}
	resources, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotResources := map[string]bool{}
	for _, resource := range resources.Resources {
		gotResources[resource.URI] = true
	}
	for _, want := range []string{mcpResourceRecentURI, mcpResourceStarredURI} {
		if !gotResources[want] {
			t.Fatalf("missing static resource %q in %v", want, gotResources)
		}
	}
}

// decodeStructured 把工具结构化输出解码到目标类型。
func decodeStructured(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

// TestMCPSearchTool 搜索工具：默认 Token 看不到敏感行（S1）。
func TestMCPSearchTool(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	result := fx.callTool(t, session, mcpToolSearch, map[string]any{})
	var search SearchResult
	decodeStructured(t, result, &search)
	seenSensitive := false
	foundNormal := false
	for _, item := range search.Items {
		if item.Sensitive {
			seenSensitive = true
		}
		if item.Text == "mcp normal" {
			foundNormal = true
		}
	}
	if seenSensitive || !foundNormal {
		t.Fatalf("search items=%+v", search.Items)
	}
	// 授权 Token 可见敏感行。
	grantedSession := fx.connect(t, fx.grantedToken)
	grantedResult := fx.callTool(t, grantedSession, mcpToolSearch, map[string]any{})
	var grantedSearch SearchResult
	decodeStructured(t, grantedResult, &grantedSearch)
	foundSecret := false
	for _, item := range grantedSearch.Items {
		if item.Text == "mcp secret" {
			foundSecret = true
		}
	}
	if !foundSecret {
		t.Fatalf("granted search items=%+v", grantedSearch.Items)
	}
	// 未授权显式 sensitive=true → 空集。
	filtered := fx.callTool(t, session, mcpToolSearch, map[string]any{"sensitive": true})
	var sensitiveSearch SearchResult
	decodeStructured(t, filtered, &sensitiveSearch)
	if len(sensitiveSearch.Items) != 0 {
		t.Fatalf("sensitive=true without permission items=%+v", sensitiveSearch.Items)
	}
	// limit 分页：可见记录有 2 条（普通 + 图片），limit=1 探测 hasMore。
	paged := fx.callTool(t, session, mcpToolSearch, map[string]any{"limit": 1})
	var page SearchResult
	decodeStructured(t, paged, &page)
	if len(page.Items) != 1 || !page.HasMore || page.NextCursor == nil {
		t.Fatalf("paged search=%+v", page)
	}
	// 文本过滤。
	textQuery := fx.callTool(t, session, mcpToolSearch, map[string]any{"query": "normal"})
	var textResult SearchResult
	decodeStructured(t, textQuery, &textResult)
	if len(textResult.Items) != 1 || textResult.Items[0].Text != "mcp normal" {
		t.Fatalf("filtered search=%+v", textResult.Items)
	}
}

// TestMCPGetItemTool 单条工具：敏感 ID 对未授权表现为 not found（S2）。
func TestMCPGetItemTool(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	result := fx.callTool(t, session, mcpToolGetItem, map[string]any{"id": strconv.FormatInt(fx.normalID, 10)})
	var item ExternalItem
	decodeStructured(t, result, &item)
	if item.Text != "mcp normal" {
		t.Fatalf("item=%+v", item)
	}
	sensitiveResult := fx.callTool(t, session, mcpToolGetItem, map[string]any{"id": strconv.FormatInt(fx.sensitiveID, 10)})
	if !sensitiveResult.IsError {
		t.Fatalf("unauthorized sensitive get must be tool error: %+v", sensitiveResult.Content)
	}
	// 授权 Token 可获取敏感条目。
	grantedSession := fx.connect(t, fx.grantedToken)
	grantedResult := fx.callTool(t, grantedSession, mcpToolGetItem, map[string]any{"id": strconv.FormatInt(fx.sensitiveID, 10)})
	var secret ExternalItem
	decodeStructured(t, grantedResult, &secret)
	if !secret.Sensitive || secret.Text != "mcp secret" {
		t.Fatalf("granted item=%+v", secret)
	}
}

// TestMCPBatchTool 批量工具：静默省略敏感 ID（S3）。
func TestMCPBatchTool(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	ids := []string{strconv.FormatInt(fx.normalID, 10), "99999", strconv.FormatInt(fx.sensitiveID, 10)}
	result := fx.callTool(t, session, mcpToolGetItems, map[string]any{"ids": ids})
	var batch mcpItemsResult
	decodeStructured(t, result, &batch)
	if len(batch.Items) != 1 || batch.Items[0].Text != "mcp normal" {
		t.Fatalf("batch=%+v", batch.Items)
	}
}

// TestMCPContentTool 内容工具：文本返回文本块、图片返回图片块、敏感 not found。
func TestMCPContentTool(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	// 文本内容。
	result := fx.callTool(t, session, mcpToolGetContent, map[string]any{"id": strconv.FormatInt(fx.normalID, 10)})
	if result.IsError || len(result.Content) == 0 {
		t.Fatalf("text content result=%+v", result)
	}
	textBlock, ok := result.Content[0].(*mcp.TextContent)
	if !ok || textBlock.Text != "mcp normal" {
		t.Fatalf("text block=%+v", result.Content[0])
	}
	// 图片内容（通过存储 ID 查找 image 条目）。
	items, _, err := fx.store.Search(context.Background(), domain.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var imageID int64
	for _, item := range items {
		if item.Format == "CLIPVAULT_IMAGE_FILE" {
			imageID = item.ID
		}
	}
	if imageID == 0 {
		t.Fatal("no image item seeded")
	}
	imageResult := fx.callTool(t, session, mcpToolGetContent, map[string]any{"id": strconv.FormatInt(imageID, 10)})
	if imageResult.IsError || len(imageResult.Content) == 0 {
		t.Fatalf("image content result=%+v", imageResult)
	}
	if _, ok := imageResult.Content[0].(*mcp.ImageContent); !ok {
		t.Fatalf("image block=%T", imageResult.Content[0])
	}
	// 敏感条目内容 not found。
	sensitiveResult := fx.callTool(t, session, mcpToolGetContent, map[string]any{"id": strconv.FormatInt(fx.sensitiveID, 10)})
	if !sensitiveResult.IsError {
		t.Fatalf("unauthorized sensitive content must be tool error: %+v", sensitiveResult.Content)
	}
}

// TestMCPCountTool 计数工具排除敏感行（S4）。
func TestMCPCountTool(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	result := fx.callTool(t, session, mcpToolCount, map[string]any{})
	var count mcpCountResult
	decodeStructured(t, result, &count)
	// 可见：mcp normal + 图片（seed 两条 + 图片一条）。
	if count.Count != 2 {
		t.Fatalf("count=%d, want 2", count.Count)
	}
	grantedSession := fx.connect(t, fx.grantedToken)
	grantedResult := fx.callTool(t, grantedSession, mcpToolCount, map[string]any{})
	var grantedCount mcpCountResult
	decodeStructured(t, grantedResult, &grantedCount)
	if grantedCount.Count != 3 {
		t.Fatalf("granted count=%d, want 3", grantedCount.Count)
	}
}

// TestMCPStatsTool 统计工具排除敏感行（S5）。
func TestMCPStatsTool(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	result := fx.callTool(t, session, mcpToolStats, map[string]any{"bucket": "day"})
	var stats StatsResult
	decodeStructured(t, result, &stats)
	if stats.Total != 2 {
		t.Fatalf("stats total=%d, want 2", stats.Total)
	}
	if len(stats.ByKind) == 0 || len(stats.BySourceApp) == 0 || len(stats.Trend) == 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

// TestMCPGroupsTool 分组工具：可见计数排除敏感行（S6）。
func TestMCPGroupsTool(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	result := fx.callTool(t, session, mcpToolGroups, map[string]any{})
	var groups mcpGroupsResult
	decodeStructured(t, result, &groups)
	if len(groups.Groups) != 1 || groups.Groups[0].VisibleItemCount != 1 {
		t.Fatalf("groups=%+v", groups.Groups)
	}
	if groups.Groups[0].Name != "MCP 组" {
		t.Fatalf("group name=%q", groups.Groups[0].Name)
	}
}

// TestMCPResources 资源读取：item/content/recent/starred/group/group-items。
func TestMCPResources(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	read := func(uri string) *mcp.ReadResourceResult {
		result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("read resource %s: %v", uri, err)
		}
		return result
	}

	// item 资源。
	itemResult := read("clipvault://items/" + strconv.FormatInt(fx.normalID, 10))
	if len(itemResult.Contents) != 1 {
		t.Fatalf("item contents=%+v", itemResult.Contents)
	}
	var item ExternalItem
	if err := json.Unmarshal([]byte(itemResult.Contents[0].Text), &item); err != nil {
		t.Fatal(err)
	}
	if item.Text != "mcp normal" {
		t.Fatalf("item resource=%+v", item)
	}
	// 敏感条目资源与不存在条目同样表现为资源不存在。
	if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "clipvault://items/" + strconv.FormatInt(fx.sensitiveID, 10)}); err == nil {
		t.Fatal("unauthorized sensitive item resource must not be readable")
	}
	if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "clipvault://items/99999"}); err == nil {
		t.Fatal("missing item resource must not be readable")
	}
	// content 资源：文本。
	contentResult := read("clipvault://items/" + strconv.FormatInt(fx.normalID, 10) + "/content")
	if len(contentResult.Contents) != 1 || contentResult.Contents[0].Text != "mcp normal" {
		t.Fatalf("content resource=%+v", contentResult.Contents)
	}
	if !strings.Contains(contentResult.Contents[0].MIMEType, "text/plain") {
		t.Fatalf("content mime=%q", contentResult.Contents[0].MIMEType)
	}
	// recent 资源：可见的最新条目（不含敏感）。
	recentResult := read(mcpResourceRecentURI)
	var recentItems []ExternalItem
	if err := json.Unmarshal([]byte(recentResult.Contents[0].Text), &recentItems); err != nil {
		t.Fatal(err)
	}
	if len(recentItems) != 2 {
		t.Fatalf("recent items=%d, want 2", len(recentItems))
	}
	for _, item := range recentItems {
		if item.Sensitive {
			t.Fatalf("recent leaked sensitive item %+v", item)
		}
	}
	// starred 资源：给普通条目加星后可见。
	if _, err := fx.store.db.Exec(`UPDATE clipboard_items SET starred = true WHERE id = ?`, fx.normalID); err != nil {
		t.Fatal(err)
	}
	starredResult := read(mcpResourceStarredURI)
	var starredItems []ExternalItem
	if err := json.Unmarshal([]byte(starredResult.Contents[0].Text), &starredItems); err != nil {
		t.Fatal(err)
	}
	if len(starredItems) != 1 || starredItems[0].Text != "mcp normal" {
		t.Fatalf("starred=%+v", starredItems)
	}
	// group 资源与 group items 资源。
	groupResult := read("clipvault://groups/" + strconv.FormatInt(fx.groupID, 10))
	var group ExternalGroup
	if err := json.Unmarshal([]byte(groupResult.Contents[0].Text), &group); err != nil {
		t.Fatal(err)
	}
	if group.VisibleItemCount != 1 || group.Name != "MCP 组" {
		t.Fatalf("group resource=%+v", group)
	}
	groupItemsResult := read("clipvault://groups/" + strconv.FormatInt(fx.groupID, 10) + "/items")
	var groupItems []ExternalItem
	if err := json.Unmarshal([]byte(groupItemsResult.Contents[0].Text), &groupItems); err != nil {
		t.Fatal(err)
	}
	if len(groupItems) != 1 || groupItems[0].Sensitive {
		t.Fatalf("group items=%+v", groupItems)
	}
	// 不存在的分组。
	if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "clipvault://groups/99999"}); err == nil {
		t.Fatal("missing group resource must not be readable")
	}
}

// TestMCPAuditRecordsOperations MCP 审计记录 tool:/resource: 操作。
func TestMCPAuditRecordsOperations(t *testing.T) {
	fx := startMCPTestService(t)
	session := fx.connect(t, fx.token)
	fx.callTool(t, session, mcpToolSearch, map[string]any{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "clipvault://recent"}); err != nil {
		t.Fatal(err)
	}
	fx.svc.Close() // 优雅关闭审计 writer，缓冲落盘
	events, total, err := fx.store.ListAPIAuditEvents(ctx, APIAuditFilter{Channel: TokenChannelMCP, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total < 2 {
		t.Fatalf("mcp audit total=%d events=%+v", total, events)
	}
	toolOps := map[string]bool{}
	resourceURIs := map[string]bool{}
	for _, event := range events {
		if event.Channel != TokenChannelMCP {
			t.Fatalf("audit channel=%q", event.Channel)
		}
		if strings.HasPrefix(event.Operation, "tool:") {
			toolOps[event.Operation] = true
		}
		if strings.HasPrefix(event.Operation, "resource:") {
			resourceURIs[event.ResourceURI] = true
		}
		// 审计绝不包含令牌原文（S10）。
		if strings.Contains(event.RequestBody, fx.token) || strings.Contains(event.RequestSummary, fx.token) {
			t.Fatalf("audit leaked raw token: %+v", event)
		}
	}
	if !toolOps["tool:"+mcpToolSearch] {
		t.Fatalf("missing tool:search audit in %v", toolOps)
	}
	if !resourceURIs[mcpResourceRecentURI] {
		t.Fatalf("missing recent resource audit in %v", resourceURIs)
	}
}

// TestMCPBusyGovernor 并发槽占满时 HTTP 层 429（带认证后命中并发闸门）。
func TestMCPBusyGovernor(t *testing.T) {
	fx := startMCPTestService(t)
	for i := 0; i < mcpMaxConcurrent; i++ {
		fx.svc.gov <- struct{}{}
	}
	defer func() {
		for i := 0; i < mcpMaxConcurrent; i++ {
			<-fx.svc.gov
		}
	}()
	req, _ := http.NewRequest(http.MethodPost, fx.base+mcpEndpointPath,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("busy must be 429, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("busy response must carry Retry-After")
	}
}

// TestMCPPanicRecovery panic 被恢复为 500，服务不退出。
func TestMCPPanicRecovery(t *testing.T) {
	fx := startMCPTestService(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	handler := fx.svc.recoverMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("panic must recover to 500, got %d", recorder.Code)
	}
	// 服务仍可用。
	session := fx.connect(t, fx.token)
	fx.callTool(t, session, mcpToolCount, map[string]any{})
}

// TestMCPNoWriteOperations 外部 MCP 请求不能改变任何数据（S9 冒烟）。
func TestMCPNoWriteOperations(t *testing.T) {
	fx := startMCPTestService(t)
	before, _, err := fx.store.Search(context.Background(), domain.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	session := fx.connect(t, fx.token)
	// 调用全部工具（含非法参数）不会修改数据。
	for name, args := range map[string]map[string]any{
		mcpToolSearch:     {},
		mcpToolGetItem:    {"id": "1"},
		mcpToolGetItems:   {"ids": []string{"1"}},
		mcpToolGetContent: {"id": "1"},
		mcpToolCount:      {},
		mcpToolStats:      {},
		mcpToolGroups:     {},
	} {
		result := fx.callTool(t, session, name, args)
		_ = result
	}
	after, _, err := fx.store.Search(context.Background(), domain.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("item count changed: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i].Starred != after[i].Starred || before[i].Sensitive != after[i].Sensitive {
			t.Fatalf("item state changed: %+v -> %+v", before[i], after[i])
		}
	}
}

// ── 小工具 ────────────────────────────────────────────────
