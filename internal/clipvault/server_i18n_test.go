package clipvault

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newI18nHTTPTestServer 构建带 i18n 服务的 SearchServer（不监听，直接调
// handler），并注册模板服务指针供 {{T}} 渲染。withConfig 为 true 时附带
// ConfigManager（持久化语言切换路径）。
func newI18nHTTPTestServer(t *testing.T, withConfig bool) (*SearchServer, *I18nService) {
	t.Helper()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var cfgMgr *ConfigManager
	if withConfig {
		cfgMgr, err = NewConfigManager(store)
		if err != nil {
			t.Fatal(err)
		}
	}
	svc, err := NewI18nService(store, cfgMgr, nil)
	if err != nil {
		t.Fatal(err)
	}
	prev := templateI18nService.Swap(svc)
	t.Cleanup(func() { templateI18nService.Store(prev) })
	server := &SearchServer{store: store, i18n: svc, cfgMgr: cfgMgr}
	return server, svc
}

func callJSON(t *testing.T, handler http.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func decodeJSONBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return payload
}

func TestI18nDictJSEndpoint(t *testing.T) {
	server, svc := newI18nHTTPTestServer(t, false)

	rec := httptest.NewRecorder()
	server.handleI18nDictJS(rec, httptest.NewRequest(http.MethodGet, "/i18n-dict.js", nil))

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/javascript; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	// 是合法的 JS 赋值语句（CSP 允许的外部脚本），且包含当前语言与合成词条。
	if !strings.HasPrefix(body, "window.__clipvaultI18n = {") {
		t.Fatalf("body must assign window.__clipvaultI18n, got %q", firstLine(body))
	}
	if !strings.Contains(body, `"lang":"`+svc.CurrentLanguage()+`"`) {
		t.Fatalf("body missing current language %q", svc.CurrentLanguage())
	}
	if !strings.Contains(body, `"common.save":"`) {
		t.Fatal("body missing composed dict entries")
	}

	// 切换语言后字典随当前语言变化（no-cache 语义由响应头保证）。
	if err := svc.SetLanguage(I18nEnglish); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	server.handleI18nDictJS(rec2, httptest.NewRequest(http.MethodGet, "/i18n-dict.js", nil))
	if body2 := rec2.Body.String(); !strings.Contains(body2, `"lang":"en-US"`) {
		t.Fatalf("body after switch = %s, want en-US", firstLine(body2))
	}
}

func TestApplicationPageRendersI18n(t *testing.T) {
	server, svc := newI18nHTTPTestServer(t, false)

	// SSR 模板：{{T}} 经包级服务指针取当前语言；<html lang> 与标题联动。
	renderPage := func(path string) string {
		t.Helper()
		page, ok := applicationPages[path]
		if !ok {
			t.Fatalf("page %s not found", path)
		}
		rec := httptest.NewRecorder()
		server.serveApplicationPage(rec, page)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %s status = %d, body = %s", path, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	if err := svc.SetLanguage(I18nChinese); err != nil {
		t.Fatal(err)
	}
	home := renderPage("/")
	for _, want := range []string{`lang="zh-CN"`, "历史", "设置", "<title>历史 · ClipVault</title>"} {
		if !strings.Contains(home, want) {
			t.Fatalf("zh-CN home page missing %q", want)
		}
	}

	if err := svc.SetLanguage(I18nEnglish); err != nil {
		t.Fatal(err)
	}
	homeEN := renderPage("/")
	for _, want := range []string{`lang="en-US"`, "History", "Settings", "<title>History · ClipVault</title>"} {
		if !strings.Contains(homeEN, want) {
			t.Fatalf("en-US home page missing %q", want)
		}
	}
	// 免费版横幅与导航按钮。
	if !strings.Contains(homeEN, "Activate Now") {
		t.Fatal("en-US home page missing localized banner button")
	}

	// 其余页面标题同样走词条（抽查设置页）。
	settingsEN := renderPage("/settings")
	if !strings.Contains(settingsEN, "<title>Settings · ClipVault</title>") {
		t.Fatalf("en-US settings title missing, got %s", firstLine(settingsEN))
	}
}

// TestApplicationPageRendersWithoutService 服务未注册时模板仍可渲染
// （T 退化为 Key、lang 回退 zh-CN），保证测试与极端启动顺序不崩。
func TestApplicationPageRendersWithoutService(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	prev := templateI18nService.Swap(nil)
	t.Cleanup(func() { templateI18nService.Store(prev) })
	server := &SearchServer{store: store}
	rec := httptest.NewRecorder()
	server.serveApplicationPage(rec, applicationPages["/settings/appearance"])
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `lang="zh-CN"`) {
		t.Fatal("fallback lang must be zh-CN")
	}
}

func firstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}

// ─── 语言管理 API ───────────────────────────────────────────────────────────

func TestI18nLanguagesAPI(t *testing.T) {
	server, svc := newI18nHTTPTestServer(t, true)

	// 列表：官方两种 + 完成度（官方词条全部已翻译）。
	rec := callJSON(t, server.handleI18nLanguagesGet, http.MethodGet, "/api/v1/i18n/languages", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
	}
	payload := decodeJSONBody(t, rec)
	languages, _ := payload["languages"].([]any)
	if len(languages) != 2 {
		t.Fatalf("languages = %v, want 2 builtin", languages)
	}

	// 创建：非法 code / 官方 code / 非法 base / 正常路径。
	for _, tc := range []struct {
		code     string
		name     string
		base     string
		wantCode int
		wantErr  string
	}{
		{"bad code!", "X", I18nEnglish, http.StatusBadRequest, "i18n_code_invalid"},
		{I18nChinese, "X", I18nEnglish, http.StatusBadRequest, "i18n_code_exists"},
		{"xx-XX", "X", "ja-JP", http.StatusBadRequest, "i18n_base_invalid"},
		{"", "", I18nEnglish, http.StatusBadRequest, "i18n_code_invalid"},
	} {
		rec := callJSON(t, server.handleI18nLanguagesCreate, http.MethodPost, "/api/v1/i18n/languages",
			map[string]any{"code": tc.code, "name": tc.name, "baseLanguageCode": tc.base})
		if rec.Code != tc.wantCode || !strings.Contains(rec.Body.String(), tc.wantErr) {
			t.Fatalf("create %q status=%d body=%s, want %d/%s", tc.code, rec.Code, rec.Body.String(), tc.wantCode, tc.wantErr)
		}
	}
	rec = callJSON(t, server.handleI18nLanguagesCreate, http.MethodPost, "/api/v1/i18n/languages",
		map[string]any{"code": "ja-JP", "name": "Japanese", "nativeName": "日本語", "baseLanguageCode": I18nEnglish})
	if rec.Code != http.StatusOK {
		t.Fatalf("create ja-JP status = %d: %s", rec.Code, rec.Body.String())
	}
	// 重复创建。
	rec = callJSON(t, server.handleI18nLanguagesCreate, http.MethodPost, "/api/v1/i18n/languages",
		map[string]any{"code": "ja-JP", "name": "Japanese", "baseLanguageCode": I18nEnglish})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "i18n_code_exists") {
		t.Fatalf("duplicate create status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 更新：官方 403 / 不存在 404 / 正常更新。
	rec = callJSON(t, server.handleI18nLanguagesUpdate, http.MethodPut, "/api/v1/i18n/languages?code=zh-CN",
		map[string]any{"name": "X"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "i18n_builtin_readonly") {
		t.Fatalf("update builtin status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = callJSON(t, server.handleI18nLanguagesUpdate, http.MethodPut, "/api/v1/i18n/languages?code=nope",
		map[string]any{"name": "X"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update missing status = %d", rec.Code)
	}
	disabled := false
	rec = callJSON(t, server.handleI18nLanguagesUpdate, http.MethodPut, "/api/v1/i18n/languages?code=ja-JP",
		map[string]any{"isEnabled": disabled})
	if rec.Code != http.StatusOK {
		t.Fatalf("update ja-JP status = %d: %s", rec.Code, rec.Body.String())
	}
	// 停用语言不可设为当前语言。
	rec = callJSON(t, server.handleI18nCurrentLanguagePut, http.MethodPut, "/api/v1/i18n/current-language",
		map[string]any{"code": "ja-JP"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "i18n_language_disabled") {
		t.Fatalf("switch to disabled status=%d body=%s", rec.Code, rec.Body.String())
	}
	// 重新启用。
	enabled := true
	if rec := callJSON(t, server.handleI18nLanguagesUpdate, http.MethodPut, "/api/v1/i18n/languages?code=ja-JP",
		map[string]any{"isEnabled": enabled}); rec.Code != http.StatusOK {
		t.Fatalf("re-enable status = %d", rec.Code)
	}

	// 切换当前语言：持久化 + 服务状态。
	rec = callJSON(t, server.handleI18nCurrentLanguagePut, http.MethodPut, "/api/v1/i18n/current-language",
		map[string]any{"code": "ja-JP"})
	if rec.Code != http.StatusOK {
		t.Fatalf("switch status = %d: %s", rec.Code, rec.Body.String())
	}
	if svc.CurrentLanguage() != "ja-JP" {
		t.Fatalf("current = %s, want ja-JP", svc.CurrentLanguage())
	}
	if server.cfgMgr.Get().UI.Language != "ja-JP" {
		t.Fatalf("persisted = %s, want ja-JP", server.cfgMgr.Get().UI.Language)
	}
	// 切换到 auto / 未知语言。
	if rec := callJSON(t, server.handleI18nCurrentLanguagePut, http.MethodPut, "/api/v1/i18n/current-language",
		map[string]any{"code": I18nAuto}); rec.Code != http.StatusOK {
		t.Fatalf("switch to auto status = %d", rec.Code)
	}
	if rec := callJSON(t, server.handleI18nCurrentLanguagePut, http.MethodPut, "/api/v1/i18n/current-language",
		map[string]any{"code": "xx-XX"}); rec.Code != http.StatusNotFound {
		t.Fatalf("switch to unknown status = %d", rec.Code)
	}

	// 删除：官方 403；删除当前语言时自动切换。
	if err := svc.SetLanguage("ja-JP"); err != nil {
		t.Fatal(err)
	}
	rec = callJSON(t, server.handleI18nLanguagesDelete, http.MethodDelete, "/api/v1/i18n/languages?code=en-US", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete builtin status = %d", rec.Code)
	}
	rec = callJSON(t, server.handleI18nLanguagesDelete, http.MethodDelete, "/api/v1/i18n/languages?code=ja-JP", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete current status = %d: %s", rec.Code, rec.Body.String())
	}
	if svc.CurrentLanguage() != I18nEnglish {
		t.Fatalf("after deleting current language = %s, want en-US", svc.CurrentLanguage())
	}
	if server.cfgMgr.Get().UI.Language != I18nEnglish {
		t.Fatalf("persisted after delete = %s, want en-US", server.cfgMgr.Get().UI.Language)
	}
}

func TestI18nEntriesAPI(t *testing.T) {
	server, svc := newI18nHTTPTestServer(t, false)

	// 创建编辑目标语言。
	if rec := callJSON(t, server.handleI18nLanguagesCreate, http.MethodPost, "/api/v1/i18n/languages",
		map[string]any{"code": "ja-JP", "name": "Japanese", "nativeName": "日本語", "baseLanguageCode": I18nEnglish}); rec.Code != http.StatusOK {
		t.Fatalf("create ja-JP status = %d: %s", rec.Code, rec.Body.String())
	}

	// 词条查询：自定义语言 base=en-US；非法状态 400。
	rec := callJSON(t, server.handleI18nEntriesGet, http.MethodGet, "/api/v1/i18n/entries?lang=ja-JP&status=bogus", nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "i18n_invalid_status") {
		t.Fatalf("invalid status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = callJSON(t, server.handleI18nEntriesGet, http.MethodGet, "/api/v1/i18n/entries?lang=nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing language status = %d", rec.Code)
	}
	rec = callJSON(t, server.handleI18nEntriesGet, http.MethodGet, "/api/v1/i18n/entries?lang=ja-JP", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("entries status = %d: %s", rec.Code, rec.Body.String())
	}
	payload := decodeJSONBody(t, rec)
	entries, _ := payload["entries"].([]any)
	total, _ := payload["total"].(float64)
	wantEntries := min(len(BuiltinTranslations), DefaultI18nEditorPageSize)
	if int(total) != len(BuiltinTranslations) || len(entries) != wantEntries {
		t.Fatalf("total=%v entries=%d, want %d/%d (default page size caps entries)", total, len(entries), len(BuiltinTranslations), wantEntries)
	}
	if canEdit, _ := payload["canEdit"].(bool); !canEdit {
		t.Fatal("custom language must be editable")
	}
	// 分页。
	rec = callJSON(t, server.handleI18nEntriesGet, http.MethodGet, "/api/v1/i18n/entries?lang=ja-JP&pageSize=10&page=2", nil)
	payload = decodeJSONBody(t, rec)
	if page, _ := payload["page"].(float64); int(page) != 2 {
		t.Fatalf("page = %v, want 2", page)
	}
	entries, _ = payload["entries"].([]any)
	if len(entries) != 10 {
		t.Fatalf("page size = %d, want 10", len(entries))
	}

	// 官方语言可查看但不可编辑。
	rec = callJSON(t, server.handleI18nEntriesGet, http.MethodGet, "/api/v1/i18n/entries?lang=zh-CN", nil)
	payload = decodeJSONBody(t, rec)
	if canEdit, _ := payload["canEdit"].(bool); canEdit {
		t.Fatal("builtin language must not be editable")
	}
	if baseCode, _ := payload["baseCode"].(string); baseCode != I18nEnglish {
		t.Fatalf("zh-CN base = %v, want en-US (builtin 互为参考)", baseCode)
	}

	// 保存译文：正常 / 缓存即时生效 / 空白即删 / 超长 400 / 官方 403。
	save := func(key, value string) *httptest.ResponseRecorder {
		return callJSON(t, server.handleI18nEntriesPut, http.MethodPut,
			"/api/v1/i18n/entries?lang=ja-JP&key="+key, map[string]any{"value": value})
	}
	if rec := save("common.save", "保存する"); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	if err := svc.SetLanguage("ja-JP"); err != nil {
		t.Fatal(err)
	}
	if got := svc.T("common.save", nil); got != "保存する" {
		t.Fatalf("T after save = %q, want 保存する", got)
	}
	if rec := save("common.save", "   "); rec.Code != http.StatusOK {
		t.Fatalf("blank save status = %d", rec.Code)
	}
	if got := svc.T("common.save", nil); got != "Save" {
		t.Fatalf("T after delete = %q, want base fallback Save", got)
	}
	if rec := save("common.save", strings.Repeat("x", MaxI18nTranslationBytes+1)); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize status = %d", rec.Code)
	}
	rec = callJSON(t, server.handleI18nEntriesPut, http.MethodPut,
		"/api/v1/i18n/entries?lang=en-US&key=common.save", map[string]any{"value": "tamper"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("save to builtin status = %d", rec.Code)
	}
	if got := svc.T("common.save", nil); got != "Save" {
		t.Fatalf("builtin translation must be protected, got %q", got)
	}
}

func TestCustomLanguageTranslatesMCPSidebarAndTitle(t *testing.T) {
	server, svc := newI18nHTTPTestServer(t, false)
	pack := sampleLanguagePack()
	pack.Language = I18nPackLanguage{Code: "zhcn", Name: "Chinese custom", NativeName: "简体中文", BaseLanguageCode: I18nChinese}
	pack.Translations = map[string]string{"app.title.mcp": "连接器"}
	response := callJSON(t, server.handleI18nPackImport, http.MethodPost, "/api/v1/i18n/import", map[string]any{"pack": pack})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	if err := svc.SetLanguage("zhcn"); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/settings/language", "/mcp", "/settings"} {
		body := renderApplicationPage(t, route)
		start := strings.Index(body, `href="/mcp"`)
		if start < 0 {
			t.Fatalf("%s has no MCP menu", route)
		}
		end := strings.Index(body[start:], "</a>")
		if end < 0 || !strings.Contains(body[start:start+end], "连接器") {
			t.Fatalf("%s sidebar did not use custom translation", route)
		}
		if route == "/mcp" && !strings.Contains(body, `<h1 class="sd-title">连接器</h1>`) {
			t.Fatal("page title did not use custom translation")
		}
	}
}

func TestPasswordValidationUsesActiveLanguage(t *testing.T) {
	server, svc := newI18nHTTPTestServer(t, false)
	pack := sampleLanguagePack()
	pack.Translations = map[string]string{
		"auth.password_whitespace": "custom whitespace",
		"auth.password_too_short":  "custom minimum {n}",
		"auth.password_too_long":   "custom maximum",
	}
	response := callJSON(t, server.handleI18nPackImport, http.MethodPost, "/api/v1/i18n/import", map[string]any{"pack": pack})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	for _, language := range []struct{ code, whitespace, short, long string }{
		{I18nChinese, "密码首尾不能有空格", "密码至少需要 8 个字符", "密码过长"},
		{I18nEnglish, "The password cannot have leading or trailing spaces", "The password needs at least 8 characters", "The password is too long"},
		{pack.Language.Code, "custom whitespace", "custom minimum 8", "custom maximum"},
	} {
		if err := svc.SetLanguage(language.code); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ password, want string }{
			{" password", language.whitespace},
			{"short", language.short},
			{strings.Repeat("x", maxAuthPasswordBytes+1), language.long},
		} {
			if err := validateAuthPassword(tc.password); err == nil || err.Error() != tc.want {
				t.Fatalf("%s: got %v, want %q", language.code, err, tc.want)
			}
		}
		if err := validateAuthPassword("valid-password"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestI18nReloadReconcilesDisabledCurrentLanguage(t *testing.T) {
	server, svc := newI18nHTTPTestServer(t, true)
	pack := sampleLanguagePack()
	pack.Translations = map[string]string{"common.save": "custom-save"}
	imported := callJSON(t, server.handleI18nPackImport, http.MethodPost, "/api/v1/i18n/import", map[string]any{"pack": pack})
	if imported.Code != http.StatusOK {
		t.Fatal(imported.Body.String())
	}
	if err := svc.SetLanguage(pack.Language.Code); err != nil {
		t.Fatal(err)
	}
	var changes []string
	svc.OnLanguageChanged(func() { changes = append(changes, svc.CurrentLanguage()+":"+svc.T("common.save", nil)) })
	for _, enabled := range []bool{false, true} {
		result := callJSON(t, server.handleI18nLanguagesUpdate, http.MethodPut, "/api/v1/i18n/languages?code="+pack.Language.Code, map[string]any{"isEnabled": enabled})
		if result.Code != http.StatusOK {
			t.Fatal(result.Body.String())
		}
		wantCode, wantText := I18nEnglish, "Save"
		if enabled {
			wantCode, wantText = pack.Language.Code, "custom-save"
		}
		if svc.CurrentLanguage() != wantCode || svc.T("common.save", nil) != wantText {
			t.Fatalf("enabled=%v: current=%s text=%s", enabled, svc.CurrentLanguage(), svc.T("common.save", nil))
		}
		if code, dict := svc.DictionarySnapshot(); code != wantCode || dict["common.save"] != wantText {
			t.Fatal("frontend dictionary disagrees with runtime")
		}
		if server.cfgMgr.Get().UI.Language != pack.Language.Code {
			t.Fatal("fallback overwrote user's configured language")
		}
	}
	if len(changes) != 2 {
		t.Fatalf("language notifications=%v", changes)
	}
}
