package clipvault

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

func openDataAPITestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestAPITokenLifecycle(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()

	raw, hash, err := generateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "cv_") || len(raw) < 40 {
		t.Fatalf("unexpected token shape %q", raw)
	}
	expiresAtMS, err := apiTokenExpiryMS("30d", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.CreateAPIToken(ctx, "Ollama", hash, expiresAtMS)
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.AuthenticateAPIToken(ctx, raw)
	if err != nil {
		t.Fatalf("authenticate valid token: %v", err)
	}
	if token.ID != id || token.Name != "Ollama" || token.ExpiresAt == nil {
		t.Fatalf("unexpected token %+v", token)
	}
	if _, err := store.AuthenticateAPIToken(ctx, "cv_wrong"); err != errAPITokenNotFound {
		t.Fatalf("wrong token err=%v, want not found", err)
	}
	// 数据库中找不到 Token 原文。
	var count int
	if err := store.db.QueryRowContext(ctx,
		`SELECT count(*) FROM api_tokens WHERE token_hash = ? OR name LIKE '%' || ? || '%'`, raw, raw).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("raw token must never be persisted")
	}
	// 过期 Token 被拒绝。
	expiredHash := hashAPIToken("cv_expired")
	if _, err := store.CreateAPIToken(ctx, "expired", expiredHash, time.Now().Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateAPIToken(ctx, "cv_expired"); err != errAPITokenExpired {
		t.Fatalf("expired token err=%v, want expired", err)
	}
	// 撤销后立即被拒绝。
	if err := store.RevokeAPIToken(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateAPIToken(ctx, raw); err != errAPITokenRevoked {
		t.Fatalf("revoked token err=%v, want revoked", err)
	}
	// 最后访问时间单调更新。
	if err := store.TouchAPITokenLastUsed(ctx, id, 1000); err != nil {
		t.Fatal(err)
	}
	if err := store.TouchAPITokenLastUsed(ctx, id, 500); err != nil {
		t.Fatal(err)
	}
	tokens, err := store.ListAPITokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 || tokens[0].Name != "expired" {
		t.Fatalf("unexpected token list %+v", tokens)
	}
	for _, token := range tokens {
		if token.Name == "Ollama" {
			if token.LastUsedAt == nil || token.LastUsedAt.UnixMilli() != 1000 {
				t.Fatalf("lastUsedAt=%v, want 1000ms", token.LastUsedAt)
			}
			if token.RevokedAt == nil {
				t.Fatal("revokedAt must be set")
			}
		}
	}
}

func TestDataAPIQueryValidation(t *testing.T) {
	parse := func(body string) (*dataAPIQueryPlan, *dataAPIError) {
		var req dataAPIQueryRequest
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("test body must be valid JSON: %v", err)
		}
		return parseDataAPIQuery(&req)
	}
	// 合法查询：时间范围 + kind in + 文本 contains + 排序分页。
	plan, apiErr := parse(`{
		"resource": "clipboardItems",
		"filter": {
			"createdAt": {"gte": "2026-08-18T00:00:00+08:00", "lt": "2026-08-19T00:00:00+08:00"},
			"kind": {"in": ["text"]},
			"sourceApp": {"in": ["Code.exe"]},
			"text": {"contains": "docker"},
			"sensitive": {"eq": true}
		},
		"orderBy": [{"field": "createdAt", "direction": "desc"}],
		"limit": 50
	}`)
	if apiErr != nil {
		t.Fatalf("valid query rejected: %v", apiErr)
	}
	if !strings.Contains(plan.where, "created_at >=") || !strings.Contains(plan.where, "kind IN") ||
		!strings.Contains(plan.where, "ILIKE") || !strings.Contains(plan.where, "sensitive =") {
		t.Fatalf("unexpected where %s", plan.where)
	}
	if plan.orderSQL != "created_at DESC, id DESC" {
		t.Fatalf("unexpected order %q", plan.orderSQL)
	}
	// 非白名单字段与操作符被拒绝。
	for _, body := range []string{
		`{"resource": "clipboardItems", "filter": {"metaJson": {"eq": "x"}}}`,
		`{"resource": "clipboardItems", "filter": {"text": {"regex": ".*"}}}`,
		`{"resource": "clipboardItems", "filter": {"starred": {"contains": "x"}}}`,
		`{"resource": "clipboardItems", "filter": {"createdAt": {"eq": "2026-08-18T00:00:00Z"}}}`,
		`{"resource": "tables"}`,
		`{"resource": "clipboardItems", "orderBy": [{"field": "text", "direction": "desc"}]}`,
	} {
		if _, apiErr := parse(body); apiErr == nil {
			t.Fatalf("invalid query accepted: %s", body)
		}
	}
	// limit 封顶。
	plan, _ = parse(`{"resource": "clipboardItems", "limit": 1000000}`)
	if plan.limit != dataAPIMaxLimit {
		t.Fatalf("limit=%d, want capped %d", plan.limit, dataAPIMaxLimit)
	}
}

func TestDataAPIQueryKeysetPagination(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		if err := store.Save(ctx, ClipboardItem{
			CapturedAtMS: 1700000000000 + int64(i)*1000,
			Kind:         "text",
			Format:       "CF_UNICODETEXT",
			Text:         "entry",
		}); err != nil {
			t.Fatal(err)
		}
	}
	var req dataAPIQueryRequest
	if err := json.Unmarshal([]byte(`{"resource": "clipboardItems", "limit": 3}`), &req); err != nil {
		t.Fatal(err)
	}
	plan, apiErr := parseDataAPIQuery(&req)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	// 第一页。
	args := append(append([]any{}, plan.args...), plan.limit+1)
	rows, err := store.db.QueryContext(ctx,
		"SELECT id, created_at FROM clipboard_items"+plan.where+" ORDER BY "+plan.orderSQL+" LIMIT ?", args...)
	if err != nil {
		t.Fatal(err)
	}
	var page []struct{ id, ms int64 }
	for rows.Next() {
		var id, ms int64
		if err := rows.Scan(&id, &ms); err != nil {
			t.Fatal(err)
		}
		page = append(page, struct{ id, ms int64 }{id, ms})
	}
	rows.Close()
	if len(page) != 4 {
		t.Fatalf("first page probe rows=%d, want 4", len(page))
	}
	// 游标翻页后不得与第一页重叠，且保持倒序。
	cursor := plan.encodeCursor(page[2].ms, page[2].id)
	req.Cursor = cursor
	plan2, apiErr := parseDataAPIQuery(&req)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	args2 := append(append(append([]any{}, plan2.args...), plan2.cursorArgs()...), plan2.limit+1)
	rows2, err := store.db.QueryContext(ctx,
		"SELECT id FROM clipboard_items"+plan2.where+" ORDER BY "+plan2.orderSQL+" LIMIT ?", args2...)
	if err != nil {
		t.Fatal(err)
	}
	var page2 []int64
	for rows2.Next() {
		var id int64
		if err := rows2.Scan(&id); err != nil {
			t.Fatal(err)
		}
		page2 = append(page2, id)
	}
	rows2.Close()
	if len(page2) != 4 {
		t.Fatalf("second page rows=%d, want 4", len(page2))
	}
	firstPageIDs := map[int64]bool{}
	for _, row := range page[:3] {
		firstPageIDs[row.id] = true
	}
	for _, id := range page2 {
		if firstPageIDs[id] {
			t.Fatalf("page overlap on id %d", id)
		}
	}
	// 篡改游标被拒绝。
	req.Cursor = cursor[:len(cursor)-2] + "xx"
	if _, apiErr := parseDataAPIQuery(&req); apiErr == nil || apiErr.Code != "invalid_cursor" {
		t.Fatalf("tampered cursor err=%v, want invalid_cursor", apiErr)
	}
}

func TestDataAPIAggregateQuery(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()
	for _, exe := range []string{"Code.exe", "Code.exe", "chrome.exe"} {
		if err := store.Save(ctx, ClipboardItem{
			Kind: "text", Format: "CF_UNICODETEXT", Text: "x", SourceExeName: exe,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var req dataAPIQueryRequest
	body := `{"resource": "clipboardItems", "groupBy": ["sourceApp"], "aggregate": {"count": "*"},
		"orderBy": [{"field": "count", "direction": "desc"}], "limit": 20}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	plan, apiErr := parseDataAPIQuery(&req)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if !plan.aggregate || plan.orderSQL != "c DESC" {
		t.Fatalf("unexpected aggregate plan %+v", plan)
	}
	rows, err := store.db.QueryContext(ctx,
		"SELECT "+plan.groupCol+", COUNT(*) AS c FROM clipboard_items"+plan.where+
			" GROUP BY "+plan.groupCol+" ORDER BY "+plan.orderSQL+" LIMIT ?", plan.limit)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	total := 0
	first := true
	for rows.Next() {
		var name string
		var c int64
		if err := rows.Scan(&name, &c); err != nil {
			t.Fatal(err)
		}
		if first && (name != "Code.exe" || c != 2) {
			t.Fatalf("first row=%s/%d, want Code.exe/2", name, c)
		}
		first = false
		total++
	}
	if total != 2 {
		t.Fatalf("aggregate rows=%d, want 2", total)
	}
	// 任意聚合表达式被拒绝。
	req.Aggregate = map[string]json.RawMessage{"sum": json.RawMessage(`"bytes"`)}
	if _, apiErr := parseDataAPIQuery(&req); apiErr == nil {
		t.Fatal("non-count aggregate must be rejected")
	}
}

// dataAPITestFixture 是一个已启动的数据接口服务与有效 Token。
type dataAPITestFixture struct {
	store *Store
	svc   *DataAPIService
	base  string
	token string
}

func (f *dataAPITestFixture) authed(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, f.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readResponseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}

// startDataAPITestService 起服务 + 激活通过 + 一个有效 Token。
func startDataAPITestService(t *testing.T, items ...ClipboardItem) *dataAPITestFixture {
	t.Helper()
	store := openDataAPITestStore(t)
	ctx := context.Background()
	for _, item := range items {
		if err := store.Save(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	env, err := newEnvironment(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	svc := NewDataAPIService(nil, store, logger, nil, env)
	svc.activatedFn = func() bool { return true }
	t.Cleanup(svc.Close)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	cfg := DefaultConfig()
	cfg.DataAPI.Enabled = true
	cfg.DataAPI.ListenAddr = "127.0.0.1:" + strconv.Itoa(port)
	if err := svc.ApplyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if status := svc.Status(); !status.Running {
		t.Fatalf("service not running: %+v", status)
	}
	raw, hash, err := generateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIToken(ctx, "e2e", hash, 0); err != nil {
		t.Fatal(err)
	}
	return &dataAPITestFixture{
		store: store, svc: svc,
		base: "http://127.0.0.1:" + strconv.Itoa(port), token: raw,
	}
}

func TestDataAPIServiceEndToEnd(t *testing.T) {
	fx := startDataAPITestService(t, ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: "hello from agent", SourceExeName: "Code.exe",
	})
	ctx := context.Background()

	// health 无需 Token。
	resp, err := http.Get(fx.base + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(readResponseBody(t, resp), `"apiEnabled":true`) {
		t.Fatal("health check failed")
	}
	// 未带 Token：401 missing_token。
	resp, err = http.Get(fx.base + "/api/v1/metadata")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(readResponseBody(t, resp), `"error":"missing_token"`) {
		t.Fatal("missing token must be 401 missing_token")
	}
	// 错误 Token：401 invalid_token，且不泄露任何数据。
	req, _ := http.NewRequest("POST", fx.base+"/api/v1/query", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer cv_bad")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(readResponseBody(t, resp), `"error":"invalid_token"`) {
		t.Fatal("bad token must be 401 invalid_token")
	}
	// 合法查询。
	resp = fx.authed(t, "POST", "/api/v1/query", `{"resource": "clipboardItems", "filter": {"text": {"contains": "agent"}}, "limit": 10}`)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "hello from agent") || !strings.Contains(body, `"hasMore":false`) {
		t.Fatalf("query failed: %d %s", resp.StatusCode, body)
	}
	// 单条完整记录。
	items, _, err := fx.store.Search(ctx, domain.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resp = fx.authed(t, "GET", "/api/v1/items/"+strconv.FormatInt(items[0].ID, 10), "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(readResponseBody(t, resp), "hello from agent") {
		t.Fatal("get item failed")
	}
	// 未知记录：404 not_found。
	resp = fx.authed(t, "GET", "/api/v1/items/99999", "")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(readResponseBody(t, resp), `"error":"not_found"`) {
		t.Fatal("missing item must be 404 not_found")
	}
	// 关闭后端口真正释放。
	cfg := DefaultConfig()
	cfg.DataAPI.Enabled = false
	if err := fx.svc.ApplyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if fx.svc.Status().Running {
		t.Fatal("service must stop when disabled")
	}
	if _, err := http.Get(fx.base + "/api/v1/health"); err == nil {
		t.Fatal("listener must be closed after disable")
	}
}

// TestDataAPIActivationGate 未激活时所有认证端点返回 403 activation_required。
func TestDataAPIActivationGate(t *testing.T) {
	fx := startDataAPITestService(t)
	fx.svc.activatedFn = func() bool { return false }
	resp := fx.authed(t, "GET", "/api/v1/metadata", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, `"error":"activation_required"`) {
		t.Fatalf("unactivated must be 403: %d %s", resp.StatusCode, body)
	}
	// health 仍可达，但报告未激活。
	resp, err := http.Get(fx.base + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	if body := readResponseBody(t, resp); !strings.Contains(body, `"activated":false`) || !strings.Contains(body, `"status":"restricted"`) {
		t.Fatalf("health must report restricted: %s", body)
	}
}

// TestDataAPIBusyGovernor 并发槽占满时立即 429 api_busy 并带 Retry-After。
func TestDataAPIBusyGovernor(t *testing.T) {
	fx := startDataAPITestService(t)
	for i := 0; i < dataAPIMaxConcurrent; i++ {
		fx.svc.gov <- struct{}{}
	}
	defer func() {
		for i := 0; i < dataAPIMaxConcurrent; i++ {
			<-fx.svc.gov
		}
	}()
	resp := fx.authed(t, "GET", "/api/v1/metadata", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusTooManyRequests || !strings.Contains(body, `"error":"api_busy"`) {
		t.Fatalf("busy must be 429 api_busy: %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("busy response must carry Retry-After")
	}
	if fx.svc.busyRejections.Load() != 1 {
		t.Fatalf("busyRejections=%d, want 1", fx.svc.busyRejections.Load())
	}
}

// TestDataAPIRateLimit 每 IP 令牌耗尽时 429 too_many_requests。
func TestDataAPIRateLimit(t *testing.T) {
	fx := startDataAPITestService(t)
	fx.svc.rateMu.Lock()
	fx.svc.rateMap["127.0.0.1"] = &dataAPIRateBucket{tokens: 0, last: time.Now()}
	fx.svc.rateMu.Unlock()
	resp := fx.authed(t, "GET", "/api/v1/metadata", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusTooManyRequests || !strings.Contains(body, `"error":"too_many_requests"`) {
		t.Fatalf("rate limited must be 429: %d %s", resp.StatusCode, body)
	}
}

// TestDataAPIPanicRecovery handler panic 被恢复为 500 internal_error，服务不退出。
func TestDataAPIPanicRecovery(t *testing.T) {
	fx := startDataAPITestService(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/panic", nil)
	handler := fx.svc.recoverMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), `"error":"internal_error"`) {
		t.Fatalf("panic must recover to 500: %d %s", recorder.Code, recorder.Body.String())
	}
	// 服务仍健康。
	resp := fx.authed(t, "GET", "/api/v1/metadata", "")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("service must survive panic: %d", resp.StatusCode)
	}
}

// TestDataAPIBatchGet 批量获取保持请求顺序，跳过缺失 ID。
func TestDataAPIBatchGet(t *testing.T) {
	fx := startDataAPITestService(t,
		ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "first"},
		ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "second"},
	)
	items, _, err := fx.store.Search(context.Background(), domain.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%d, want 2", len(items))
	}
	// 倒序请求 + 一个不存在的 ID。
	body := `{"ids": ["` + strconv.FormatInt(items[1].ID, 10) + `", "99999", "` + strconv.FormatInt(items[0].ID, 10) + `"]}`
	resp := fx.authed(t, "POST", "/api/v1/items/batch-get", body)
	payload := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batch-get failed: %d %s", resp.StatusCode, payload)
	}
	var result struct {
		Items []dataAPIItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		t.Fatal(err)
	}
	// Search 按 created_at 倒序：items[0] 是 "second"。请求顺序为 [items[1]("first"), 99999, items[0]("second")]，
	// 输出必须保持请求顺序。
	if len(result.Items) != 2 || *result.Items[0].Text != "first" || *result.Items[1].Text != "second" {
		t.Fatalf("unexpected batch result %+v", result.Items)
	}
	// 超限拒绝。
	tooMany := strings.Repeat(`"1",`, dataAPIMaxBatchIDs) + `"2"`
	resp = fx.authed(t, "POST", "/api/v1/items/batch-get", `{"ids": [`+tooMany+`]}`)
	if resp.StatusCode != http.StatusBadRequest {
		readResponseBody(t, resp)
		t.Fatalf("oversized batch must be 400: %d", resp.StatusCode)
	}
	readResponseBody(t, resp)
}

// TestDataAPIItemContent 文本条目经 content 端点原样返回；无内容条目 404。
func TestDataAPIItemContent(t *testing.T) {
	fx := startDataAPITestService(t,
		ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "plain body"},
		ClipboardItem{Kind: "files", Format: "CF_HDROP", Text: "C:\\some\\file.txt"},
	)
	items, _, err := fx.store.Search(context.Background(), domain.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var textID, fileID int64
	for _, item := range items {
		if item.Kind == "text" {
			textID = item.ID
		} else {
			fileID = item.ID
		}
	}
	resp := fx.authed(t, "GET", "/api/v1/items/"+strconv.FormatInt(textID, 10)+"/content", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || body != "plain body" {
		t.Fatalf("text content failed: %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("unexpected content type %q", ct)
	}
	// 文件路径记录不提供内容（文件系统边界）。
	resp = fx.authed(t, "GET", "/api/v1/items/"+strconv.FormatInt(fileID, 10)+"/content", "")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(readResponseBody(t, resp), `"error":"not_found"`) {
		t.Fatalf("file record content must be 404: %d", resp.StatusCode)
	}
}

func TestDataAPIAuditStore(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()
	events := []APIAuditEvent{
		{Timestamp: time.Now().UTC(), TokenID: 7, TokenName: "MacBook AI", RemoteIP: "192.168.1.23",
			Method: "POST", Endpoint: "/api/v1/query", Operation: "query_items", ItemCount: 18, DurationMs: 24, StatusCode: 200},
		{Timestamp: time.Now().UTC(), RemoteIP: "192.168.1.36", Method: "GET", Endpoint: "/api/v1/metadata",
			Operation: "auth", StatusCode: 401, ErrorCode: "invalid_token"},
	}
	if err := store.InsertAPIAuditEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	listed, total, err := store.ListAPIAuditEvents(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(listed) != 2 {
		t.Fatalf("audit total=%d len=%d, want 2", total, len(listed))
	}
	if listed[0].Operation != "auth" || listed[1].TokenName != "MacBook AI" {
		t.Fatalf("unexpected audit order %+v", listed)
	}
	if err := store.ClearAPIAuditEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if _, total, err := store.ListAPIAuditEvents(ctx, 10, 0); err != nil || total != 0 {
		t.Fatalf("after clear total=%d err=%v", total, err)
	}
}
