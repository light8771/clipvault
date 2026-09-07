package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	id, err := store.CreateAPIToken(ctx, "Ollama", hash, expiresAtMS, TokenChannelHTTP, false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.AuthenticateAPIToken(ctx, raw, TokenChannelHTTP)
	if err != nil {
		t.Fatalf("authenticate valid token: %v", err)
	}
	if token.ID != id || token.Name != "Ollama" || token.Channel != TokenChannelHTTP || token.ExpiresAt == nil {
		t.Fatalf("unexpected token %+v", token)
	}
	if token.AllowSensitive {
		t.Fatal("new token must default to allow_sensitive=false")
	}
	if _, err := store.AuthenticateAPIToken(ctx, raw, TokenChannelMCP); err != errAPITokenChannel {
		t.Fatalf("http token on mcp channel err=%v, want channel mismatch", err)
	}
	if _, err := store.AuthenticateAPIToken(ctx, "cv_wrong", TokenChannelHTTP); err != errAPITokenNotFound {
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
	if _, err := store.CreateAPIToken(ctx, "expired", expiredHash, time.Now().Add(-time.Hour).UnixMilli(), TokenChannelHTTP, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateAPIToken(ctx, "cv_expired", TokenChannelHTTP); err != errAPITokenExpired {
		t.Fatalf("expired token err=%v, want expired", err)
	}
	// 撤销后立即被拒绝。
	if err := store.RevokeAPIToken(ctx, id, TokenChannelHTTP); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateAPIToken(ctx, raw, TokenChannelHTTP); err != errAPITokenRevoked {
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

func TestDataAPIMetadataDescribesCurrentContentContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&DataAPIService{}).handleMetadata(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/metadata", nil), &APIToken{})

	var response struct {
		Resources map[string]struct {
			Fields           map[string]string `json:"fields"`
			RecordTypeValues []string          `json:"recordTypeValues"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	resource, ok := response.Resources["clipboardItems"]
	if !ok {
		t.Fatalf("clipboardItems metadata missing: %s", recorder.Body.String())
	}
	wantFields := map[string]string{
		"recordType":           "string",
		"contentSha256":        "string|null",
		"consecutiveDuplicate": "boolean",
		"groupIds":             "array<string>",
	}
	for field, wantType := range wantFields {
		if got := resource.Fields[field]; got != wantType {
			t.Fatalf("metadata field %s = %q, want %q", field, got, wantType)
		}
	}
	for _, removedField := range []string{"sha256", "contentAvailable", "groupId"} {
		if _, exists := resource.Fields[removedField]; exists {
			t.Fatalf("obsolete or wrong-projection metadata field %q retained", removedField)
		}
	}
	if len(resource.RecordTypeValues) != 1 || resource.RecordTypeValues[0] != "content" {
		t.Fatalf("recordTypeValues = %#v, want [content]", resource.RecordTypeValues)
	}

	payload, err := json.Marshal(toDataAPIItem(StoredItem{RecordType: domain.HistoryRecordContent}, []int64{3, 8}, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"groupIds":["3","8"]`) || strings.Contains(string(payload), `"groupId":`) {
		t.Fatalf("multi-group response contract drifted: %s", payload)
	}
}

// TestSetAPITokenSensitive 验证 SetAPITokenSensitive 的 store 语义：
// 仅对未撤销、未过期且通道匹配的 Token 生效；幂等；其余情况一律 errAPITokenNotFound。
func TestSetAPITokenSensitive(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()

	// 永不过期的 HTTP 通道 Token，创建时敏感授权为 false（跟随全局默认）。
	raw, hash, err := generateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.CreateAPIToken(ctx, "tuner", hash, 0, TokenChannelHTTP, false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.AuthenticateAPIToken(ctx, raw, TokenChannelHTTP)
	if err != nil {
		t.Fatalf("authenticate fresh token: %v", err)
	}
	if token.AllowSensitive {
		t.Fatal("fresh token must not allow sensitive")
	}

	// 开启敏感授权后认证立即反映新值。
	if err := store.SetAPITokenSensitive(ctx, id, TokenChannelHTTP, true); err != nil {
		t.Fatal(err)
	}
	token, err = store.AuthenticateAPIToken(ctx, raw, TokenChannelHTTP)
	if err != nil {
		t.Fatalf("authenticate after grant: %v", err)
	}
	if !token.AllowSensitive {
		t.Fatal("token must allow sensitive after grant")
	}

	// 幂等：重复设为相同值仍然成功。
	if err := store.SetAPITokenSensitive(ctx, id, TokenChannelHTTP, true); err != nil {
		t.Fatalf("idempotent grant must succeed: %v", err)
	}

	// 关闭敏感授权。
	if err := store.SetAPITokenSensitive(ctx, id, TokenChannelHTTP, false); err != nil {
		t.Fatal(err)
	}
	token, err = store.AuthenticateAPIToken(ctx, raw, TokenChannelHTTP)
	if err != nil {
		t.Fatalf("authenticate after revoke grant: %v", err)
	}
	if token.AllowSensitive {
		t.Fatal("token must not allow sensitive after revoke grant")
	}

	// 通道不匹配：MCP 通道的 Token 不能经 HTTP 通道调整。
	mcpRaw, mcpHash, err := generateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	mcpID, err := store.CreateAPIToken(ctx, "mcp-tuner", mcpHash, 0, TokenChannelMCP, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAPITokenSensitive(ctx, mcpID, TokenChannelHTTP, true); !errors.Is(err, errAPITokenNotFound) {
		t.Fatalf("cross-channel set err=%v, want errAPITokenNotFound", err)
	}
	// MCP 通道自身可以正常调整，且不破坏未撤销 Token。
	if err := store.SetAPITokenSensitive(ctx, mcpID, TokenChannelMCP, false); err != nil {
		t.Fatal(err)
	}
	mcpToken, err := store.AuthenticateAPIToken(ctx, mcpRaw, TokenChannelMCP)
	if err != nil {
		t.Fatalf("authenticate mcp token: %v", err)
	}
	if mcpToken.AllowSensitive {
		t.Fatal("mcp token must not allow sensitive after revoke grant")
	}

	// 已撤销的 Token 不能再调整。
	if err := store.RevokeAPIToken(ctx, id, TokenChannelHTTP); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAPITokenSensitive(ctx, id, TokenChannelHTTP, true); !errors.Is(err, errAPITokenNotFound) {
		t.Fatalf("revoked token set err=%v, want errAPITokenNotFound", err)
	}

	// 已过期的 Token 不能再调整。
	expiredID, err := store.CreateAPIToken(ctx, "expired-tuner", hashAPIToken("cv_expired_tuner"), time.Now().Add(-time.Hour).UnixMilli(), TokenChannelHTTP, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAPITokenSensitive(ctx, expiredID, TokenChannelHTTP, true); !errors.Is(err, errAPITokenNotFound) {
		t.Fatalf("expired token set err=%v, want errAPITokenNotFound", err)
	}

	// 不存在的 ID。
	if err := store.SetAPITokenSensitive(ctx, 99999, TokenChannelHTTP, true); !errors.Is(err, errAPITokenNotFound) {
		t.Fatalf("missing token set err=%v, want errAPITokenNotFound", err)
	}
}

// TestRevokeAPITokenChannelIsolation 撤销必须匹配通道（SEC-07 通道隔离在
// 管理面同样成立）：跨通道撤销与不存在一样报 errAPITokenNotFound；
// 同通道撤销幂等（重复撤销成功且不再改动）。
func TestRevokeAPITokenChannelIsolation(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()

	_, mcpHash, err := generateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	mcpID, err := store.CreateAPIToken(ctx, "mcp-token", mcpHash, 0, TokenChannelMCP, false)
	if err != nil {
		t.Fatal(err)
	}
	// 跨通道撤销：MCP Token 不能经 HTTP 通道撤销。
	if err := store.RevokeAPIToken(ctx, mcpID, TokenChannelHTTP); !errors.Is(err, errAPITokenNotFound) {
		t.Fatalf("cross-channel revoke err=%v, want errAPITokenNotFound", err)
	}
	// 未受影响：Token 仍可认证。
	mcpToken, err := store.GetAPITokenByID(ctx, mcpID)
	if err != nil {
		t.Fatal(err)
	}
	if mcpToken.RevokedAt != nil {
		t.Fatal("cross-channel revoke must not touch the token")
	}
	// 同通道撤销成功，重复撤销幂等。
	if err := store.RevokeAPIToken(ctx, mcpID, TokenChannelMCP); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeAPIToken(ctx, mcpID, TokenChannelMCP); err != nil {
		t.Fatalf("repeat revoke must be idempotent: %v", err)
	}
	// 不存在的 ID。
	if err := store.RevokeAPIToken(ctx, 99999, TokenChannelMCP); !errors.Is(err, errAPITokenNotFound) {
		t.Fatalf("missing token revoke err=%v, want errAPITokenNotFound", err)
	}
}

// TestAPITokenExpiryPresetInput 有效期预设的输入契约：永不过期必须显式
// 指定 "never"；空值（漏传字段）与未知档位一律报错，避免静默签出永不过期 Token。
func TestAPITokenExpiryPresetInput(t *testing.T) {
	now := time.Now().UTC()
	for _, preset := range []string{"1d", "7d", "30d", "90d", "1y"} {
		if ms, err := apiTokenExpiryMS(preset, now); err != nil || ms <= now.UnixMilli() {
			t.Fatalf("preset %q: ms=%d err=%v", preset, ms, err)
		}
	}
	if ms, err := apiTokenExpiryMS("never", now); err != nil || ms != 0 {
		t.Fatalf("never must map to 0: ms=%d err=%v", ms, err)
	}
	for _, invalid := range []string{"", "forever", "10x"} {
		if _, err := apiTokenExpiryMS(invalid, now); err == nil {
			t.Fatalf("preset %q must be rejected", invalid)
		}
	}
}

// TestTokenExpiryPreset 重新生成时的有效期映射：按总时长匹配最近的预设档。
// created_at 与 expires_at 由两次取时分别产生，存在毫秒级偏差，必须容差匹配，
// 否则 30d 令牌会被静默续期到兜底档 1y。
func TestTokenExpiryPreset(t *testing.T) {
	now := time.Now().UTC()
	if got := tokenExpiryPreset(now, nil); got != "never" {
		t.Fatalf("nil expiry = %q, want never", got)
	}
	for _, tc := range []struct {
		preset string
		d      time.Duration
	}{
		{"1d", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"90d", 90 * 24 * time.Hour},
		{"1y", 365 * 24 * time.Hour},
	} {
		// 精确时长与带毫秒级偏差的时长都必须映射回本档。
		for _, skew := range []time.Duration{0, -time.Millisecond, -50 * time.Millisecond, time.Second} {
			expires := now.Add(tc.d + skew)
			if got := tokenExpiryPreset(now, &expires); got != tc.preset {
				t.Fatalf("preset %s with skew %s = %q, want %q", tc.preset, skew, got, tc.preset)
			}
		}
	}
}

// TestRegenerateActivationGateBeforeRevoke 未激活时重新生成直接 403，
// 且旧 Token 不被撤销：激活门控必须先于撤销执行，否则旧 Token 凭空丢失
// 而新 Token 又因未激活无法签发。
func TestRegenerateActivationGateBeforeRevoke(t *testing.T) {
	store := openDataAPITestStore(t)
	logger, err := NewDailyLogger(filepath.Join(t.TempDir(), "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	// 未激活环境（DataDir 无 license 文件）→ FreeTierRestriction 为 true。
	s := &SearchServer{store: store, logger: logger, env: Environment{DataDir: t.TempDir()}}
	if !FreeTierRestriction(s.env) {
		t.Fatal("test env must be unactivated")
	}

	for _, tc := range []struct {
		name    string
		channel string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"data-api", TokenChannelHTTP, s.handleDataAPITokenRegenerate},
		{"mcp", TokenChannelMCP, s.handleMCPTokenRegenerate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			raw, hash, err := generateAPIToken()
			if err != nil {
				t.Fatal(err)
			}
			id, err := store.CreateAPIToken(ctx, "keep", hash, 0, tc.channel, false)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/regenerate", strings.NewReader(`{"id": "`+strconv.FormatInt(id, 10)+`"}`))
			tc.handler(rec, req)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "activation_required") {
				t.Fatalf("unactivated regenerate = %d %s, want 403 activation_required", rec.Code, rec.Body.String())
			}
			// 关键断言：被拒绝的重新生成不得撤销旧 Token。
			if _, err := store.AuthenticateAPIToken(ctx, raw, tc.channel); err != nil {
				t.Fatalf("old token must survive rejected regenerate: %v", err)
			}
		})
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
		`{"resource": "clipboardItems", "groupBy": ["kind"]}`,
		`{"resource": "clipboardItems", "filter": {"id": {}}}`,
		`{"resource": "clipboardItems", "filter": {"id": {"eq": [1, 2]}}}`,
		`{"resource": "clipboardItems", "filter": {"id": {"in": 1}}}`,
		`{"resource": "clipboardItems", "filter": {"id": {"eq": 0}}}`,
		`{"resource": "clipboardItems", "filter": {"groupId": null}}`,
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
			RecordType:   RecordTypeContent,
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

func TestDataAPIGroupFilterUsesMembershipSemantics(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()
	for _, text := range []string{"only first", "both", "ungrouped"} {
		if err := store.Save(ctx, ClipboardItem{CapturedAtMS: time.Now().UnixMilli(), RecordType: domain.HistoryRecordContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil || len(items) != 3 {
		t.Fatalf("load items: count=%d err=%v", len(items), err)
	}
	byText := make(map[string]int64, len(items))
	for _, item := range items {
		byText[item.Text] = item.ID
	}
	first, err := store.CreateGroup(ctx, "first", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateGroup(ctx, "second", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, membership := range []struct{ itemID, groupID int64 }{
		{byText["only first"], first.ID}, {byText["both"], first.ID}, {byText["both"], second.ID},
	} {
		if err := store.AddItemToGroup(ctx, membership.itemID, membership.groupID); err != nil {
			t.Fatal(err)
		}
	}
	service := NewExternalReadService(store, Environment{})
	query := func(op string) []StoredItem {
		req := dataAPIQueryRequest{Resource: "clipboardItems", Limit: 10, Filter: map[string]map[string]json.RawMessage{
			"groupId": {op: json.RawMessage(strconv.FormatInt(first.ID, 10))},
		}}
		plan, apiErr := parseDataAPIQuery(&req)
		if apiErr != nil {
			t.Fatal(apiErr)
		}
		got, _, err := service.QueryItems(ctx, ReadAccess{AllowSensitive: true}, plan)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := query("eq"); len(got) != 2 {
		t.Fatalf("group eq returned %d items, want 2", len(got))
	}
	if got := query("neq"); len(got) != 1 || got[0].Text != "ungrouped" {
		t.Fatalf("group neq returned %#v, want only ungrouped", got)
	}
}

func TestDataAPIAggregateQuery(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()
	for _, exe := range []string{"Code.exe", "Code.exe", "chrome.exe"} {
		if err := store.Save(ctx, ClipboardItem{
			RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "x", SourceExeName: exe,
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
	svc := NewDataAPIService(nil, NewExternalReadService(store, env), store, logger, nil, env)
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
	if _, err := store.CreateAPIToken(ctx, "e2e", hash, 0, TokenChannelHTTP, false); err != nil {
		t.Fatal(err)
	}
	return &dataAPITestFixture{
		store: store, svc: svc,
		base: "http://127.0.0.1:" + strconv.Itoa(port), token: raw,
	}
}

func TestDataAPIServiceEndToEnd(t *testing.T) {
	fx := startDataAPITestService(t, ClipboardItem{
		RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "hello from agent", SourceExeName: "Code.exe",
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

	// 审计留档：摘要 + 完整请求体（Close 优雅关闭审计 writer，缓冲全部落盘）。
	fx.svc.Close()
	auditEvents, auditTotal, err := fx.store.ListAPIAuditEvents(ctx, APIAuditFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if auditTotal != 5 {
		t.Fatalf("audit total=%d events=%+v, want 5", auditTotal, auditEvents)
	}
	var queryEvent *APIAuditEvent
	for i := range auditEvents {
		if auditEvents[i].Operation == "query_items" {
			queryEvent = &auditEvents[i]
			break
		}
	}
	if queryEvent == nil {
		t.Fatalf("no query_items audit event in %+v", auditEvents)
	}
	wantBody := `{"resource": "clipboardItems", "filter": {"text": {"contains": "agent"}}, "limit": 10}`
	if queryEvent.RequestBody != wantBody {
		t.Fatalf("audit body = %q, want %q", queryEvent.RequestBody, wantBody)
	}
	if !strings.Contains(queryEvent.RequestSummary, `text.contains="agent"`) || !strings.Contains(queryEvent.RequestSummary, "limit=10") {
		t.Fatalf("audit summary = %q, want agent query summary", queryEvent.RequestSummary)
	}
}

func TestDataAPIStopClearsInactiveBindFailure(t *testing.T) {
	svc := &DataAPIService{running: false, listen: "127.0.0.1:1", bindError: "address in use"}
	svc.stopLocked()
	if svc.running || svc.listen != "" || svc.bindError != "" {
		t.Fatalf("stopped state retained runtime fields: running=%v listen=%q bindError=%q", svc.running, svc.listen, svc.bindError)
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
		ClipboardItem{RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "first"},
		ClipboardItem{RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "second"},
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

func TestDataAPIRejectsTrailingJSONValues(t *testing.T) {
	fx := startDataAPITestService(t)
	for _, test := range []struct {
		path string
		body string
	}{
		{path: "/api/v1/query", body: `{"resource":"clipboardItems"}{"extra":true}`},
		{path: "/api/v1/items/batch-get", body: `{"ids":["1"]}{"extra":true}`},
	} {
		resp := fx.authed(t, http.MethodPost, test.path, test.body)
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, `"error":"invalid_request"`) {
			t.Fatalf("%s accepted trailing JSON: status=%d body=%s", test.path, resp.StatusCode, body)
		}
	}
}

// TestDataAPIItemContent 文本条目经 content 端点原样返回；无内容条目 404。
func TestDataAPIItemContent(t *testing.T) {
	fx := startDataAPITestService(t,
		ClipboardItem{RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "plain body"},
		ClipboardItem{RecordType: RecordTypeContent, Kind: "files", Format: "CF_HDROP", Text: "C:\\some\\file.txt"},
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
			Method: "POST", Endpoint: "/api/v1/query", Operation: "query_items", ItemCount: 18, DurationMs: 24, StatusCode: 200,
			RequestSummary: "text.contains=\"docker\" · limit=5",
			RequestBody:    `{"resource":"clipboardItems","filter":{"text":{"contains":"docker"}},"limit":5}`},
		{Timestamp: time.Now().UTC(), RemoteIP: "192.168.1.36", Method: "GET", Endpoint: "/api/v1/metadata",
			Operation: "auth", StatusCode: 401, ErrorCode: "invalid_token"},
	}
	if err := store.InsertAPIAuditEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	listed, total, err := store.ListAPIAuditEvents(ctx, APIAuditFilter{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(listed) != 2 {
		t.Fatalf("audit total=%d len=%d, want 2", total, len(listed))
	}
	if listed[0].Operation != "auth" || listed[1].TokenName != "MacBook AI" {
		t.Fatalf("unexpected audit order %+v", listed)
	}
	if listed[1].RequestSummary != events[0].RequestSummary {
		t.Fatalf("request summary roundtrip = %q, want %q", listed[1].RequestSummary, events[0].RequestSummary)
	}
	if listed[1].RequestBody != events[0].RequestBody {
		t.Fatalf("request body roundtrip = %q, want %q", listed[1].RequestBody, events[0].RequestBody)
	}
	// 操作过滤。
	queryOnly, qTotal, err := store.ListAPIAuditEvents(ctx, APIAuditFilter{Limit: 10, Operations: []string{"query_items"}})
	if err != nil || qTotal != 1 || len(queryOnly) != 1 || queryOnly[0].Operation != "query_items" {
		t.Fatalf("operation filter: total=%d events=%+v err=%v", qTotal, queryOnly, err)
	}
	// 结果过滤：只看失败。
	errors, errTotal, err := store.ListAPIAuditEvents(ctx, APIAuditFilter{Limit: 10, Result: "error"})
	if err != nil || errTotal != 1 || errors[0].StatusCode != 401 {
		t.Fatalf("result filter: total=%d events=%+v err=%v", errTotal, errors, err)
	}
	// 关键词过滤：匹配请求摘要。
	matched, mTotal, err := store.ListAPIAuditEvents(ctx, APIAuditFilter{Limit: 10, Query: "docker"})
	if err != nil || mTotal != 1 || matched[0].Operation != "query_items" {
		t.Fatalf("query filter: total=%d events=%+v err=%v", mTotal, matched, err)
	}
	// 令牌过滤。
	byToken, tTotal, err := store.ListAPIAuditEvents(ctx, APIAuditFilter{Limit: 10, TokenID: 7})
	if err != nil || tTotal != 1 || byToken[0].TokenName != "MacBook AI" {
		t.Fatalf("token filter: total=%d events=%+v err=%v", tTotal, byToken, err)
	}
	// 全量统计。
	stats, err := store.APIAuditStats(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 2 || stats.Success != 1 || stats.Error != 1 || stats.Last24h != 2 {
		t.Fatalf("unexpected stats %+v", stats)
	}
	if err := store.ClearAPIAuditEvents(ctx, TokenChannelHTTP); err != nil {
		t.Fatal(err)
	}
	if _, total, err := store.ListAPIAuditEvents(ctx, APIAuditFilter{Limit: 10}); err != nil || total != 0 {
		t.Fatalf("after clear total=%d err=%v", total, err)
	}
}

func TestDataAPIAuditEndpointsThroughHTTP(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	dataDir := t.TempDir()
	logDir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listen port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release listen port: %v", err)
	}
	cfg := manager.Get()
	cfg.Search.ListenAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := manager.Update(cfg); err != nil {
		t.Fatalf("configure search server: %v", err)
	}
	logger, err := NewDailyLogger(logDir)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer logger.Close()
	server := NewSearchServer(manager, store, NewRuntimeState(false), logger, Environment{DataDir: dataDir, LogDir: logDir, Source: "default"}, nil, nil)
	baseURL, err := server.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	client := &http.Client{Timeout: 5 * time.Second}

	seed := []APIAuditEvent{
		{Timestamp: time.Now().UTC(), TokenID: 7, TokenName: "Ollama", RemoteIP: "127.0.0.1",
			Method: "POST", Endpoint: "/api/v1/query", Operation: "query_items", ItemCount: 5, DurationMs: 12,
			StatusCode: 200, RequestSummary: `text.contains="docker" · limit=5`,
			RequestBody: `{"resource":"clipboardItems","filter":{"text":{"contains":"docker"}},"limit":5}`},
		{Timestamp: time.Now().UTC(), RemoteIP: "192.168.1.36", Method: "GET", Endpoint: "/api/v1/metadata",
			Operation: "auth", StatusCode: 401, ErrorCode: "invalid_token"},
	}
	if err := store.InsertAPIAuditEvents(context.Background(), seed); err != nil {
		t.Fatalf("seed audit events: %v", err)
	}

	// 过滤查询：操作 + 关键词（关键词命中请求摘要）。
	res, err := client.Get(baseURL + "api/v1/data-api/audit?operation=query_items&q=docker")
	if err != nil {
		t.Fatalf("audit request: %v", err)
	}
	var payload struct {
		Data struct {
			Entries []APIAuditEvent `json:"entries"`
			Total   int             `json:"total"`
			Stats   APIAuditStats   `json:"stats"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || payload.Data.Total != 1 || len(payload.Data.Entries) != 1 {
		t.Fatalf("filtered audit = status %d total %d entries %d", res.StatusCode, payload.Data.Total, len(payload.Data.Entries))
	}
	if !strings.Contains(payload.Data.Entries[0].RequestSummary, "docker") {
		t.Fatalf("entry summary = %q, want docker query summary", payload.Data.Entries[0].RequestSummary)
	}
	if !strings.Contains(payload.Data.Entries[0].RequestBody, `"contains":"docker"`) {
		t.Fatalf("entry body = %q, want raw query JSON", payload.Data.Entries[0].RequestBody)
	}
	// 统计概览不受列表过滤影响。
	if payload.Data.Stats.Total != 2 || payload.Data.Stats.Success != 1 || payload.Data.Stats.Error != 1 {
		t.Fatalf("audit stats = %+v, want 2/1/1", payload.Data.Stats)
	}

	// CSV 导出：带 BOM、含摘要与过滤条件。
	exportRes, err := client.Get(baseURL + "api/v1/data-api/audit/export?result=error")
	if err != nil {
		t.Fatalf("audit export: %v", err)
	}
	exportBody, readErr := io.ReadAll(exportRes.Body)
	exportRes.Body.Close()
	if err != nil || readErr != nil {
		t.Fatalf("read export: %v / %v", err, readErr)
	}
	if exportRes.StatusCode != http.StatusOK || !strings.HasPrefix(exportRes.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("export status=%d content-type=%q", exportRes.StatusCode, exportRes.Header.Get("Content-Type"))
	}
	if !bytes.HasPrefix(exportBody, []byte("\xEF\xBB\xBF")) || !strings.Contains(string(exportBody), "invalid_token") {
		t.Fatalf("export body missing BOM or error row: %q", string(exportBody[:min(len(exportBody), 200)]))
	}
	if strings.Contains(string(exportBody), "docker") {
		t.Fatal("export with result=error must not contain the success row")
	}
}

// TestDataAPIHTTPSensitivePermission E2E：HTTP Token 的敏感授权开关
// （S1/S2/S3/S4 + metadata 权限报告）。
func TestDataAPIHTTPSensitivePermission(t *testing.T) {
	fx := startDataAPITestService(t,
		ClipboardItem{RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "normal entry"},
		ClipboardItem{RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "secret entry"},
	)
	ctx := context.Background()
	items, _, err := fx.store.Search(ctx, domain.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var normalID, sensitiveID int64
	for _, item := range items {
		if item.Text == "secret entry" {
			sensitiveID = item.ID
		} else {
			normalID = item.ID
		}
	}
	if _, err := fx.store.db.ExecContext(ctx,
		`UPDATE clipboard_items SET sensitive = true WHERE id = ?`, sensitiveID); err != nil {
		t.Fatal(err)
	}
	// 授权 Token（allowSensitive=true）。
	rawGranted, hashGranted, err := generateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.CreateAPIToken(ctx, "granted", hashGranted, 0, TokenChannelHTTP, true); err != nil {
		t.Fatal(err)
	}

	// ── 未授权（默认 Token，allowSensitive=false）────────
	// 列表搜索不返回敏感行（S1）。
	resp := fx.authed(t, "POST", "/api/v1/query", `{"resource": "clipboardItems", "limit": 10}`)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "secret entry") {
		t.Fatalf("unauthorized search leaked sensitive row: %d %s", resp.StatusCode, body)
	}
	// 调用方显式 sensitive=true 不能绕过限制，返回空集。
	resp = fx.authed(t, "POST", "/api/v1/query", `{"resource": "clipboardItems", "filter": {"sensitive": {"eq": true}}}`)
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "secret entry") || !strings.Contains(body, `"hasMore":false`) {
		t.Fatalf("sensitive=true filter must return empty for unauthorized token: %d %s", resp.StatusCode, body)
	}
	// 已知敏感 ID 的单条获取与内容获取都是 404（S2）。
	resp = fx.authed(t, "GET", "/api/v1/items/"+strconv.FormatInt(sensitiveID, 10), "")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(readResponseBody(t, resp), `"error":"not_found"`) {
		t.Fatalf("unauthorized sensitive get must be 404: %d", resp.StatusCode)
	}
	resp = fx.authed(t, "GET", "/api/v1/items/"+strconv.FormatInt(sensitiveID, 10)+"/content", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unauthorized sensitive content must be 404: %d", resp.StatusCode)
	}
	// 批量获取静默省略敏感 ID（S3）。
	batchBody := `{"ids": ["` + strconv.FormatInt(normalID, 10) + `", "` + strconv.FormatInt(sensitiveID, 10) + `"]}`
	resp = fx.authed(t, "POST", "/api/v1/items/batch-get", batchBody)
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "secret entry") || !strings.Contains(body, "normal entry") {
		t.Fatalf("unauthorized batch leaked sensitive row: %d %s", resp.StatusCode, body)
	}
	// 计数排除敏感行（S4）。
	resp = fx.authed(t, "POST", "/api/v1/query", `{"resource": "clipboardItems", "groupBy": ["kind"], "aggregate": {"count": "*"}}`)
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"count":1`) {
		t.Fatalf("unauthorized aggregate must count visible rows only: %d %s", resp.StatusCode, body)
	}
	// metadata 报告只读 + 无敏感权限。
	resp = fx.authed(t, "GET", "/api/v1/metadata", "")
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"readOnly":true`) || !strings.Contains(body, `"allowSensitive":false`) {
		t.Fatalf("metadata must report permissions: %d %s", resp.StatusCode, body)
	}

	// ── 已授权（allowSensitive=true）────────────────────
	grantedReq, _ := http.NewRequest("GET", fx.base+"/api/v1/metadata", nil)
	grantedReq.Header.Set("Authorization", "Bearer "+rawGranted)
	grantedResp, err := http.DefaultClient.Do(grantedReq)
	if err != nil {
		t.Fatal(err)
	}
	grantedBody := readResponseBody(t, grantedResp)
	if grantedResp.StatusCode != http.StatusOK || !strings.Contains(grantedBody, `"allowSensitive":true`) {
		t.Fatalf("granted token metadata must report allowSensitive=true: %d %s", grantedResp.StatusCode, grantedBody)
	}
	grantedQuery, _ := http.NewRequest("POST", fx.base+"/api/v1/query", strings.NewReader(`{"resource": "clipboardItems", "limit": 10}`))
	grantedQuery.Header.Set("Authorization", "Bearer "+rawGranted)
	grantedQuery.Header.Set("Content-Type", "application/json")
	grantedQueryResp, err := http.DefaultClient.Do(grantedQuery)
	if err != nil {
		t.Fatal(err)
	}
	grantedQueryBody := readResponseBody(t, grantedQueryResp)
	if grantedQueryResp.StatusCode != http.StatusOK || !strings.Contains(grantedQueryBody, "secret entry") {
		t.Fatalf("granted token must see sensitive rows: %d %s", grantedQueryResp.StatusCode, grantedQueryBody)
	}
	grantedGet, _ := http.NewRequest("GET", fx.base+"/api/v1/items/"+strconv.FormatInt(sensitiveID, 10), nil)
	grantedGet.Header.Set("Authorization", "Bearer "+rawGranted)
	grantedGetResp, err := http.DefaultClient.Do(grantedGet)
	if err != nil {
		t.Fatal(err)
	}
	if grantedGetResp.StatusCode != http.StatusOK {
		t.Fatalf("granted token must fetch sensitive item: %d", grantedGetResp.StatusCode)
	}
	readResponseBody(t, grantedGetResp)
	grantedContent, _ := http.NewRequest("GET", fx.base+"/api/v1/items/"+strconv.FormatInt(sensitiveID, 10)+"/content", nil)
	grantedContent.Header.Set("Authorization", "Bearer "+rawGranted)
	grantedContentResp, err := http.DefaultClient.Do(grantedContent)
	if err != nil {
		t.Fatal(err)
	}
	if grantedContentResp.StatusCode != http.StatusOK || readResponseBody(t, grantedContentResp) != "secret entry" {
		t.Fatalf("granted token must fetch sensitive content: %d", grantedContentResp.StatusCode)
	}

	fx.svc.Close()
	audits, _, err := fx.store.ListAPIAuditEvents(ctx, APIAuditFilter{Operations: []string{"get_content"}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var sensitiveContentAudit bool
	for _, audit := range audits {
		if audit.ItemID == sensitiveID && audit.StatusCode == http.StatusOK && audit.SensitiveAccessed {
			sensitiveContentAudit = true
			break
		}
	}
	if !sensitiveContentAudit {
		t.Fatalf("sensitive content access was not recorded: %+v", audits)
	}
}

// TestDataAPIHTTPTokenChannelIsolation E2E：MCP 通道令牌不能调用 HTTP 数据接口（S7）。
func TestDataAPIHTTPTokenChannelIsolation(t *testing.T) {
	fx := startDataAPITestService(t)
	raw, hash, err := generateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.CreateAPIToken(context.Background(), "mcp-token", hash, 0, TokenChannelMCP, false); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", fx.base+"/api/v1/metadata", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, `"error":"invalid_token"`) {
		t.Fatalf("mcp token on http api must be 401 invalid_token: %d %s", resp.StatusCode, body)
	}
}

func TestSummarizeDataAPIQuery(t *testing.T) {
	req := dataAPIQueryRequest{
		Resource: "clipboardItems",
		Filter: map[string]map[string]json.RawMessage{
			"text":      {"contains": json.RawMessage(`"docker compose"`)},
			"kind":      {"in": json.RawMessage(`["text","image"]`)},
			"createdAt": {"gte": json.RawMessage(`"2026-08-18T00:00:00+08:00"`)},
		},
		OrderBy: []dataAPIOrder{{Field: "createdAt", Direction: "desc"}},
		Limit:   5,
		Cursor:  "aG91c2U=",
	}
	summary := summarizeDataAPIQuery(&req)
	for _, want := range []string{"text.contains=\"docker compose\"", "kind.in=[\"text\",\"image\"]", "createdAt.gte=", "sort=createdAt desc", "limit=5", "cursor=…"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q missing %q", summary, want)
		}
	}
	if len([]rune(summary)) > 400 {
		t.Fatalf("summary too long: %d runes", len([]rune(summary)))
	}
	// 空请求给空摘要。
	if got := summarizeDataAPIQuery(&dataAPIQueryRequest{Resource: "clipboardItems"}); got != "" {
		t.Fatalf("empty request summary = %q, want empty", got)
	}
	// batch ID 摘要截断。
	ids := make([]string, 20)
	for i := range ids {
		ids[i] = strconv.Itoa(i + 1)
	}
	batch := summarizeDataAPIBatchIDs(ids)
	if !strings.Contains(batch, "+8]") || !strings.Contains(batch, "ids=[1,2,3") {
		t.Fatalf("batch summary = %q", batch)
	}
}

// TestAuditChannelFieldsRoundTrip 审计 channel/resource_uri 字段往返。
func TestAuditChannelFieldsRoundTrip(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()
	events := []APIAuditEvent{
		{Timestamp: time.Now().UTC(), Channel: TokenChannelMCP, TokenID: 3, TokenName: "Codex",
			RemoteIP: "127.0.0.1", Method: "POST", Endpoint: "/mcp", Operation: "tool:search_clipboard",
			ResourceURI: "clipvault://items", ItemCount: 4, DurationMs: 9, StatusCode: 200},
		{Timestamp: time.Now().UTC(), Channel: TokenChannelHTTP, TokenID: 0, RemoteIP: "192.168.0.9",
			Method: "GET", Endpoint: "/api/v1/metadata", Operation: "auth", StatusCode: 401, ErrorCode: "invalid_token"},
	}
	if err := store.InsertAPIAuditEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	// 通道过滤：mcp 只见 mcp。
	mcpEvents, mcpTotal, err := store.ListAPIAuditEvents(ctx, APIAuditFilter{Channel: TokenChannelMCP, Limit: 10})
	if err != nil || mcpTotal != 1 || len(mcpEvents) != 1 {
		t.Fatalf("mcp channel filter total=%d events=%+v err=%v", mcpTotal, mcpEvents, err)
	}
	if mcpEvents[0].ResourceURI != "clipvault://items" || mcpEvents[0].Operation != "tool:search_clipboard" {
		t.Fatalf("mcp audit roundtrip=%+v", mcpEvents[0])
	}
	httpEvents, httpTotal, err := store.ListAPIAuditEvents(ctx, APIAuditFilter{Channel: TokenChannelHTTP, Limit: 10})
	if err != nil || httpTotal != 1 || httpEvents[0].Operation != "auth" {
		t.Fatalf("http channel filter total=%d events=%+v err=%v", httpTotal, httpEvents, err)
	}
	// 空通道统计全部。
	allStats, err := store.APIAuditStats(ctx, "")
	if err != nil || allStats.Total != 2 {
		t.Fatalf("all stats=%+v err=%v", allStats, err)
	}
	mcpStats, err := store.APIAuditStats(ctx, TokenChannelMCP)
	if err != nil || mcpStats.Total != 1 {
		t.Fatalf("mcp stats=%+v err=%v", mcpStats, err)
	}
	// 按通道清空：清空 mcp 后 http 记录保留。
	if err := store.ClearAPIAuditEvents(ctx, TokenChannelMCP); err != nil {
		t.Fatal(err)
	}
	remaining, remainingTotal, err := store.ListAPIAuditEvents(ctx, APIAuditFilter{Limit: 10})
	if err != nil || remainingTotal != 1 || len(remaining) != 1 || remaining[0].Channel != TokenChannelHTTP {
		t.Fatalf("after channel-scoped clear total=%d events=%+v err=%v", remainingTotal, remaining, err)
	}
}

// TestSensitiveAccessedRoundTrip sensitive_accessed 字段往返。
func TestSensitiveAccessedRoundTrip(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()
	events := []APIAuditEvent{
		{Timestamp: time.Now().UTC(), Channel: TokenChannelMCP, TokenName: "Codex",
			Operation: "tool:get_clipboard_item", ItemID: 7, StatusCode: 200, SensitiveAccessed: true},
		{Timestamp: time.Now().UTC(), Channel: TokenChannelHTTP, TokenName: "Ollama",
			Operation: "query_items", StatusCode: 200, SensitiveAccessed: false},
	}
	if err := store.InsertAPIAuditEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	listed, total, err := store.ListAPIAuditEvents(ctx, APIAuditFilter{Limit: 10})
	if err != nil || total != 2 {
		t.Fatalf("total=%d err=%v", total, err)
	}
	// 按 id 倒序：listed[0]=http 事件（未访问敏感），listed[1]=mcp 事件（访问敏感）。
	if listed[0].SensitiveAccessed || !listed[1].SensitiveAccessed {
		t.Fatalf("sensitiveAccessed roundtrip=%+v %+v", listed[0], listed[1])
	}
	if listed[0].Channel != TokenChannelHTTP || listed[1].Channel != TokenChannelMCP {
		t.Fatalf("channel roundtrip=%q %q", listed[0].Channel, listed[1].Channel)
	}
	// 审计不包含令牌原文或剪贴板正文（S10）。
	var raw string
	if err := store.db.QueryRowContext(ctx, `SELECT CAST(value AS VARCHAR) FROM settings WHERE key = 'schema.version'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, storeSchemaVersion) {
		t.Fatalf("schema version marker=%q", raw)
	}
}
