package clipvault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newMaskTestServer 构造带二次密码、指定会话的搜索服务器。
func newMaskTestServer(t *testing.T, store *Store) (*SearchServer, *AuthManager, string) {
	t.Helper()
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	hash, err := newPasswordHash("SecondaryPass1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Security.SecondaryPasswordHash = hash
	auth := NewAuthManager()
	session, err := auth.CreateSession(10)
	if err != nil {
		t.Fatal(err)
	}
	// i18n 服务注册到全局模板指针：服务端占位文案按当前语言渲染。
	i18nSvc, err := NewI18nService(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prev := templateI18nService.Swap(i18nSvc)
	t.Cleanup(func() { templateI18nService.Store(prev) })
	server := &SearchServer{
		store:  store,
		cfgMgr: &ConfigManager{cfg: cfg, store: store},
		auth:   auth,
		logger: logger,
		i18n:   i18nSvc,
	}
	return server, auth, session
}

// TestSearchMasksSensitiveMetadata 验证未过二次认证时，敏感条目除渲染
// 占位卡片所必需的字段（ID/时间/类型/收藏/敏感标志）外，全部元数据
// 被清除——来源应用、路径、窗口标题、字数、哈希、重复标记都是旁信道。
func TestSearchMasksSensitiveMetadata(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Save(ctx, ClipboardItem{
		RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "secret-token-value",
		MetaJSON: `{"k":"v"}`, Sensitive: true,
		SourceExeName: "passwordmgr.exe", SourceExePath: `C:\Apps\passwordmgr.exe`,
		SourceWindowTitle: "密码管理器", SourceAttribution: "foreground",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, ClipboardItem{
		RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "normal note", SourceExeName: "notepad.exe",
	}); err != nil {
		t.Fatal(err)
	}

	server, _, session := newMaskTestServer(t, store)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/items", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	result := httptest.NewRecorder()
	server.handleSearch(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", result.Code, result.Body.String())
	}
	explicitRequest := httptest.NewRequest(http.MethodGet, "/api/v1/items?record_type=content", nil)
	explicitRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	explicitResult := httptest.NewRecorder()
	server.handleSearch(explicitResult, explicitRequest)
	if explicitResult.Code != result.Code || explicitResult.Body.String() != result.Body.String() {
		t.Fatalf("explicit content projection changed masked response:\ndefault=%s\nexplicit=%s", result.Body.String(), explicitResult.Body.String())
	}
	var resp struct {
		Items []struct {
			ID            int64   `json:"id"`
			Kind          string  `json:"kind"`
			Text          string  `json:"text"`
			Sensitive     bool    `json:"sensitive"`
			ContentSHA256 *string `json:"contentSha256"`
			MetaJSON      string  `json:"metaJson"`
			Format        string  `json:"format"`
			ByteLen       int     `json:"byteLen"`
			CharLen       int     `json:"charLen"`
			Source        *struct {
				ExeName     string `json:"exeName"`
				ExePath     string `json:"exePath"`
				WindowTitle string `json:"windowTitle"`
				Attribution string `json:"attribution"`
			} `json:"source"`
		} `json:"items"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(resp.Items))
	}

	var masked, plain int
	for _, it := range resp.Items {
		if !it.Sensitive {
			plain++
			if it.Text != "normal note" || it.Source == nil || it.Source.ExeName != "notepad.exe" {
				t.Fatalf("plain item altered: %+v", it)
			}
			continue
		}
		masked++
		// 默认 zh-CN 环境下占位文本即中文词条值。
		if it.Text != "【敏感内容，点击查看】" {
			t.Fatalf("masked text = %q", it.Text)
		}
		if it.ContentSHA256 != nil || it.MetaJSON != "" || it.Format != "" ||
			it.ByteLen != 0 || it.CharLen != 0 ||
			it.Source != nil {
			t.Fatalf("sensitive metadata leaked: %+v", it)
		}
		if it.Kind != "text" || it.ID == 0 {
			t.Fatalf("masking broke render-required fields: %+v", it)
		}
	}
	if masked != 1 || plain != 1 {
		t.Fatalf("masked = %d, plain = %d", masked, plain)
	}
}

// TestSearchSecondaryAuthedShowsSensitive 验证通过二次认证后敏感明文可见。
func TestSearchSecondaryAuthedShowsSensitive(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), ClipboardItem{
		RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "secret-token-value", Sensitive: true,
	}); err != nil {
		t.Fatal(err)
	}

	server, auth, session := newMaskTestServer(t, store)
	auth.GrantSecondaryAuth(session, 1)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/items", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	result := httptest.NewRecorder()
	server.handleSearch(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", result.Code, result.Body.String())
	}
	if !strings.Contains(result.Body.String(), "secret-token-value") {
		t.Fatalf("secondary-authed search hides sensitive plaintext: %s", result.Body.String())
	}
}

func TestSearchWithoutSecondaryAuthHidesSensitiveAdjacency(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, sensitive := range []bool{true, false} {
		if err := store.Save(context.Background(), ClipboardItem{
			RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT",
			Text: "shared value", Sensitive: sensitive,
		}); err != nil {
			t.Fatal(err)
		}
	}
	server, _, session := newMaskTestServer(t, store)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/items", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	result := httptest.NewRecorder()
	server.handleSearch(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	var response struct {
		Items []struct {
			Sensitive            bool `json:"sensitive"`
			ConsecutiveDuplicate bool `json:"consecutiveDuplicate"`
		} `json:"items"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.Items[0].Sensitive || response.Items[0].ConsecutiveDuplicate {
		t.Fatalf("sensitive predecessor leaked through adjacency: %+v", response.Items)
	}
}
