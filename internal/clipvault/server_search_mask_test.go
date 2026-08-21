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
		Kind: "text", Format: "CF_UNICODETEXT", Text: "secret-token-value",
		MetaJSON: `{"k":"v"}`, Sensitive: true, IsDuplicate: true,
		SourceExeName: "passwordmgr.exe", SourceExePath: `C:\Apps\passwordmgr.exe`,
		SourceWindowTitle: "密码管理器", SourceMethod: "foreground",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: "normal note", SourceExeName: "notepad.exe",
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
	var resp struct {
		Items []struct {
			ID                int64  `json:"id"`
			Kind              string `json:"kind"`
			Text              string `json:"text"`
			Sensitive         bool   `json:"sensitive"`
			SHA256            string `json:"sha256"`
			MetaJSON          string `json:"metaJson"`
			Format            string `json:"format"`
			ByteLen           int    `json:"byteLen"`
			CharLen           int    `json:"charLen"`
			SourceExeName     string `json:"sourceExeName"`
			SourceExePath     string `json:"sourceExePath"`
			SourceWindowTitle string `json:"sourceWindowTitle"`
			SourceMethod      string `json:"sourceMethod"`
			IsDuplicate       bool   `json:"isDuplicate"`
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
			if it.Text != "normal note" || it.SourceExeName != "notepad.exe" {
				t.Fatalf("plain item altered: %+v", it)
			}
			continue
		}
		masked++
		// 默认 zh-CN 环境下占位文本即中文词条值。
		if it.Text != "【敏感内容，点击查看】" {
			t.Fatalf("masked text = %q", it.Text)
		}
		if it.SHA256 != "" || it.MetaJSON != "" || it.Format != "" ||
			it.ByteLen != 0 || it.CharLen != 0 ||
			it.SourceExeName != "" || it.SourceExePath != "" ||
			it.SourceWindowTitle != "" || it.SourceMethod != "" || it.IsDuplicate {
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
		Kind: "text", Format: "CF_UNICODETEXT", Text: "secret-token-value", Sensitive: true,
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
