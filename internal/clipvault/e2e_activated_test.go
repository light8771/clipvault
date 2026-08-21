//go:build windows

package clipvault

// 激活态端到端测试：真实主 Web 服务（信封契约 + CSRF 中间件）+ 真实外部数据接口
// （独立端口 + Bearer 认证），完整走一遍
// 启用 → 创建（默认无敏感授权）→ 单独开启敏感授权 → 外部查询 → 单独调整敏感
// 授权（含敏感可见性翻转与单条 404）→ 跨通道隔离 → regenerate（旧令牌失效/授权
// 与有效期保持）→ 审计查询与 CSV 导出。
//
// 激活凭据由测试自签：默认构建的内置信任公钥为空（生产公钥经 -ldflags 注入，
// 私钥离线保管），本测试同样要求注入与固定种子派生私钥配对的测试公钥：
//
//	go test ./internal/clipvault/ -run TestActivatedExternalServiceE2E -count=1 -v ^
//	  -ldflags "-X clipboard_ext/internal/licensing.builtinPublicKeyBase64=d/VQgFjNhKsovVm2/cey7Wn8hKcOEpqOBsL8Sdajc2A"
//
// 未注入公钥时自动 Skip，不影响常规 go test / CI。

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
	"clipboard_ext/internal/licensing"
)

// e2eLicenseSeedMaterial 派生 E2E 激活签名私钥的固定种子（公钥见文件头注释）。
const e2eLicenseSeedMaterial = "clipvault-e2e-license-test-key"

func e2ePrivateKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(e2eLicenseSeedMaterial))
	return ed25519.NewKeyFromSeed(seed[:])
}

// activateE2ELicense 为 dataDir 写入一张绑定本机、由测试私钥签名的有效凭据。
// 前置条件：-ldflags 已注入配对的内置公钥，否则 Skip。
func activateE2ELicense(t *testing.T, dataDir string) {
	t.Helper()
	trusted := licensing.BuiltinTrustedKeys()
	if len(trusted) == 0 {
		t.Skip("内置信任公钥未注入：请按 e2e_activated_test.go 文件头注释用 -ldflags 注入测试公钥后运行")
	}
	priv := e2ePrivateKey()
	want := priv.Public().(ed25519.PublicKey)
	got, ok := trusted[licensing.BuiltinKeyID()]
	if !ok || !bytes.Equal(want, got) {
		t.Fatal("注入的内置公钥与 E2E 测试私钥不配对，请核对 -ldflags 中的 base64 公钥")
	}
	machineID, err := licensing.MachineID()
	if err != nil {
		t.Skipf("无法读取本机机器码：%v", err)
	}
	token, err := licensing.SignToken(
		licensing.NewPerpetualClaims(licensing.BuiltinKeyID(), "CV-E2E-TEST-0001", machineID), priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := licensing.WriteReceipt(filepath.Join(dataDir, "license", "clipvault.lic"), token); err != nil {
		t.Fatal(err)
	}
	if FreeTierRestriction(Environment{DataDir: dataDir}) {
		t.Fatal("已写入有效凭据但 FreeTierRestriction 仍为 true")
	}
}

// e2eJSONMap 解析 JSON 对象。
func e2eJSONMap(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("invalid JSON %q: %v", body, err)
	}
	return out
}

// e2eDataOf 解包主服务信封 {"data": {...}}。
func e2eDataOf(t *testing.T, body string) map[string]any {
	t.Helper()
	envelope := e2eJSONMap(t, body)
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("response missing data envelope: %s", body)
	}
	return data
}

// e2eErrorOf 解包主服务信封 {"error": {"code": ...}}。
func e2eErrorOf(t *testing.T, body string) map[string]any {
	t.Helper()
	envelope := e2eJSONMap(t, body)
	errObj, ok := envelope["error"].(map[string]any)
	if !ok {
		t.Fatalf("response missing error envelope: %s", body)
	}
	return errObj
}

func e2eFreePort(t *testing.T) int {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	return port
}

// TestActivatedExternalServiceE2E 全链路（详见文件头）。认证环境：无锁屏密码
// （默认配置）时 requireAuth 放行，但 CSRF 中间件对全部 mutation 请求强制校验，
// 测试真实携带 CSRF 令牌走完整安全链路。
func TestActivatedExternalServiceE2E(t *testing.T) {
	dataDir := t.TempDir()
	activateE2ELicense(t, dataDir)

	ctx := context.Background()
	env, err := newEnvironment(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	cfgMgr, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// 种子数据：一条普通 + 一条敏感剪贴板记录。
	if err := store.Save(ctx, ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: "e2e-public-entry", SourceExeName: "Code.exe",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: "e2e-secret-entry", SourceExeName: "Code.exe", Sensitive: true,
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.Search(ctx, domain.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sensitiveItems, _, err := store.Search(ctx, domain.SearchOptions{SensitiveOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var publicID, secretID int64
	for _, item := range items {
		if item.Text == "e2e-public-entry" {
			publicID = item.ID
		}
	}
	for _, item := range sensitiveItems {
		if item.Text == "e2e-secret-entry" {
			secretID = item.ID
		}
	}
	if publicID == 0 || secretID == 0 {
		t.Fatalf("seed items missing: public=%d secret=%d", publicID, secretID)
	}

	// 服务装配（与 app_windows.go 同构：共享读取服务 + OnChange 即时启停）。
	readSvc := NewExternalReadService(store, env)
	dataAPI := NewDataAPIService(cfgMgr, readSvc, store, logger, nil, env)
	cfgMgr.OnChange(func(newCfg Config) error { return dataAPI.ApplyConfig(newCfg) })
	if err := dataAPI.ApplyConfig(cfgMgr.Get()); err != nil {
		t.Fatal(err)
	}
	server := NewSearchServer(cfgMgr, store, NewRuntimeState(false), logger, env, nil, nil)
	server.SetDataAPIService(dataAPI)

	// 预置监听地址（未启用数据接口）。
	dapiPort := e2eFreePort(t)
	dapiAddr := "127.0.0.1:" + strconv.Itoa(dapiPort)
	cfg := cfgMgr.Get()
	cfg.Search.ListenAddr = "127.0.0.1:" + strconv.Itoa(e2eFreePort(t))
	cfg.DataAPI.Enabled = false
	cfg.DataAPI.ListenAddr = dapiAddr
	if err := cfgMgr.Update(cfg); err != nil {
		t.Fatal(err)
	}

	base, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	base = strings.TrimSuffix(base, "/")
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Close(closeCtx)
		dataAPI.Close()
		_ = logger.Close()
		_ = store.Close()
	})

	client := &http.Client{Timeout: 10 * time.Second}
	do := func(method, path, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	readBody := func(resp *http.Response) string {
		t.Helper()
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return string(data)
	}

	// CSRF 令牌（mutation 必需）。
	resp := do(http.MethodGet, "/api/v1/session/csrf", "")
	csrf := e2eDataOf(t, readBody(resp))["token"].(string)
	mutate := func(method, path, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-ClipVault-CSRF", csrf)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// 外部数据接口请求（Bearer 认证）。
	extQuery := func(token, filter string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost,
			"http://"+dapiAddr+"/api/v1/query",
			strings.NewReader(`{"resource": "clipboardItems", "filter": {"text": {"contains": "`+filter+`"}}, "limit": 10}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		extResp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body := readBody(extResp)
		return extResp.StatusCode, body
	}
	extGetItem := func(token string, id int64) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet,
			fmt.Sprintf("http://%s/api/v1/items/%d", dapiAddr, id), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		extResp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer extResp.Body.Close()
		return extResp.StatusCode
	}

	// ── 1. 初始状态：未启用，但已激活。 ──────────────────────────────
	resp = do(http.MethodGet, "/api/v1/data-api", "")
	status := e2eDataOf(t, readBody(resp))
	if status["enabled"] != false || status["activated"] != true {
		t.Fatalf("initial status = %v", status)
	}
	if _, ok := status["sensitiveDefault"]; ok {
		t.Fatalf("sensitiveDefault must not be exposed: %v", status)
	}

	// ── 2. 启用：真实监听。 ─────────────────────────────────────────
	resp = mutate(http.MethodPut, "/api/v1/data-api/config",
		`{"enabled": true, "listenAddr": "`+dapiAddr+`"}`)
	body := readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable config = %d %s", resp.StatusCode, body)
	}
	enabled := e2eDataOf(t, body)
	if enabled["running"] != true || enabled["url"] == "" {
		t.Fatalf("data-api not running after enable: %v", enabled)
	}

	// ── 3. 创建令牌：默认不授权敏感数据（安全默认）。 ─────────────────
	resp = mutate(http.MethodPost, "/api/v1/data-api/tokens",
		`{"name": "e2e-lifecycle", "expiresIn": "30d"}`)
	body = readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token create = %d %s", resp.StatusCode, body)
	}
	created := e2eDataOf(t, body)
	tokenID, _ := created["id"].(string)
	rawToken, _ := created["token"].(string)
	if tokenID == "" || rawToken == "" || created["allowSensitive"] != false {
		t.Fatalf("token create response = %v", created)
	}

	// ── 4. 默认态外部查询：敏感条目不可见，普通条目可见。 ────────────
	code, body := extQuery(rawToken, "e2e-")
	if code != http.StatusOK || strings.Contains(body, "e2e-secret-entry") {
		t.Fatalf("default token must not see sensitive entry: %d %s", code, body)
	}
	if !strings.Contains(body, "e2e-public-entry") {
		t.Fatalf("default token must see public entry: %s", body)
	}

	// ── 5. 单独开启该令牌的敏感授权：敏感条目变为可见。 ──────────────
	resp = mutate(http.MethodPost, "/api/v1/data-api/tokens/sensitive",
		`{"id": "`+tokenID+`", "allowSensitive": true}`)
	body = readBody(resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"allowSensitive":true`) {
		t.Fatalf("sensitive enable = %d %s", resp.StatusCode, body)
	}
	code, body = extQuery(rawToken, "e2e-")
	if code != http.StatusOK || !strings.Contains(body, "e2e-secret-entry") {
		t.Fatalf("authorized query must see sensitive entry: %d %s", code, body)
	}
	if got := extGetItem(rawToken, secretID); got != http.StatusOK {
		t.Fatalf("authorized get sensitive item = %d, want 200", got)
	}

	// ── 6. 单独调整：关闭该令牌的敏感授权。 ─────────────────────────
	resp = mutate(http.MethodPost, "/api/v1/data-api/tokens/sensitive",
		`{"id": "`+tokenID+`", "allowSensitive": false}`)
	body = readBody(resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"allowSensitive":false`) {
		t.Fatalf("sensitive toggle = %d %s", resp.StatusCode, body)
	}
	code, body = extQuery(rawToken, "e2e-")
	if code != http.StatusOK || strings.Contains(body, "e2e-secret-entry") {
		t.Fatalf("restricted query must hide sensitive entry: %d %s", code, body)
	}
	if !strings.Contains(body, "e2e-public-entry") {
		t.Fatalf("restricted query must still see public entry: %s", body)
	}
	if got := extGetItem(rawToken, secretID); got != http.StatusNotFound {
		t.Fatalf("restricted get sensitive item = %d, want 404", got)
	}

	// ── 7. 跨通道隔离：MCP 令牌不能被 HTTP 端点撤销/调整。 ───────────
	resp = mutate(http.MethodPost, "/api/v1/mcp/tokens", `{"name": "e2e-mcp", "expiresIn": "never"}`)
	body = readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mcp token create = %d %s", resp.StatusCode, body)
	}
	mcpTokenID, _ := e2eDataOf(t, body)["id"].(string)
	resp = mutate(http.MethodPost, "/api/v1/data-api/tokens/revoke", `{"id": "`+mcpTokenID+`"}`)
	body = readBody(resp)
	if resp.StatusCode != http.StatusNotFound || e2eErrorOf(t, body)["code"] != "not_found" {
		t.Fatalf("cross-channel revoke = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	resp = mutate(http.MethodPost, "/api/v1/data-api/tokens/sensitive", `{"id": "`+mcpTokenID+`", "allowSensitive": true}`)
	body = readBody(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-channel sensitive = %d %s, want 404", resp.StatusCode, body)
	}

	// ── 8. regenerate：旧令牌立即失效，新令牌保持调整后的授权与 30d 有效期。 ──
	resp = mutate(http.MethodPost, "/api/v1/data-api/tokens/regenerate", `{"id": "`+tokenID+`"}`)
	body = readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("regenerate = %d %s", resp.StatusCode, body)
	}
	regen := e2eDataOf(t, body)
	newTokenID, _ := regen["id"].(string)
	newRaw, _ := regen["token"].(string)
	if newTokenID == "" || newRaw == "" || newTokenID == tokenID {
		t.Fatalf("regenerate response = %v", regen)
	}
	if regen["allowSensitive"] != false {
		t.Fatalf("regenerated token must keep adjusted allowSensitive=false: %v", regen)
	}
	expiresAt, _ := regen["expiresAt"].(string)
	if expiresAt == "" {
		t.Fatalf("regenerated token must have expiry (30d preserved): %v", regen)
	}
	expiryTime, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		t.Fatalf("parse expiresAt %q: %v", expiresAt, err)
	}
	if d := time.Until(expiryTime); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("regenerated expiry = %v, want ~30d", d)
	}
	// 旧令牌立即失效（撤销态有专用错误码，不与无效凭据混淆）。
	if code, body = extQuery(rawToken, "e2e-"); code != http.StatusUnauthorized || !strings.Contains(body, "token_revoked") {
		t.Fatalf("old token after regenerate = %d %s, want 401 token_revoked", code, body)
	}
	// 新令牌可用，且敏感授权保持关闭。
	code, body = extQuery(newRaw, "e2e-")
	if code != http.StatusOK || strings.Contains(body, "e2e-secret-entry") {
		t.Fatalf("new token query = %d %s: sensitive must stay hidden", code, body)
	}
	// 列表：旧 revoked、新 active。
	resp = do(http.MethodGet, "/api/v1/data-api/tokens", "")
	listed := e2eDataOf(t, readBody(resp))["tokens"].([]any)
	states := map[string]string{}
	for _, entry := range listed {
		token := entry.(map[string]any)
		states[token["id"].(string)] = token["state"].(string)
	}
	if states[tokenID] != "revoked" || states[newTokenID] != "active" {
		t.Fatalf("token states after regenerate = %v", states)
	}

	// ── 9. 审计：异步落库（500ms 批量），轮询直到覆盖外部查询。 ──────
	deadline := time.Now().Add(10 * time.Second)
	var auditTotal float64
	for time.Now().Before(deadline) {
		resp = do(http.MethodGet, "/api/v1/data-api/audit?limit=100", "")
		audit := e2eDataOf(t, readBody(resp))
		auditTotal, _ = audit["total"].(float64)
		if auditTotal >= 4 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if auditTotal < 4 {
		t.Fatalf("audit events missing: total=%v", auditTotal)
	}
	// CSV 导出：UTF-8 BOM + 表头 + 本令牌的查询记录。
	resp = do(http.MethodGet, "/api/v1/data-api/audit/export", "")
	csvBody := readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit export = %d", resp.StatusCode)
	}
	if !strings.HasPrefix(csvBody, "\xEF\xBB\xBF") || !strings.Contains(csvBody, "令牌") || !strings.Contains(csvBody, "e2e-lifecycle") {
		t.Fatalf("audit export CSV incomplete: %.200s", csvBody)
	}
}
