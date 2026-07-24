//go:build windows

package clipvault

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryPageIsChineseAndExplainsCredentials(t *testing.T) {
	required := []string{
		`lang="zh-CN"`,
		"恢复剪贴板数据库",
		"这不是软件激活码，也不是锁屏密码",
		"仅在本机处理",
		"创建新数据库",
		`role="status"`,
		`aria-live="polite"`,
	}
	for _, text := range required {
		if !strings.Contains(recoveryHTML, text) {
			t.Fatalf("recovery page is missing %q", text)
		}
	}
	for _, stale := range []string{"Data Recovery", "Recovery Password", "location.reload()"} {
		if strings.Contains(recoveryHTML, stale) {
			t.Fatalf("recovery page still contains stale content %q", stale)
		}
	}
}

func TestRecoveryHandlerRequiresPasswordInChinese(t *testing.T) {
	rs := NewRecoveryServer(t.TempDir())
	req := httptest.NewRequest("POST", "/api/recovery/recover", strings.NewReader(`{"password":""}`))
	recorder := httptest.NewRecorder()
	rs.handleRecover(recorder, req)

	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error != "请输入数据恢复密码" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestRecoveryAppearanceSnapshotAndAssets(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Brand = BrandConfig{
		Title:              "恢复站点",
		LogoContentType:    "image/png",
		LogoVersion:        10,
		FaviconContentType: "image/x-icon",
		FaviconVersion:     11,
	}
	cfg.Background = BackgroundConfig{
		ContentType: "video/mp4", Version: 12, Fit: "contain", Opacity: 50, Overlay: 20,
		Brightness: 100, Contrast: 100, Saturation: 100, Scale: 100, PositionX: 50, PositionY: 50,
	}
	cfg.Security.LockPasswordHash = "must-not-leak"
	if err := WriteRecoveryAppearance(dir, cfg); err != nil {
		t.Fatalf("write appearance: %v", err)
	}
	snapshot, err := os.ReadFile(filepath.Join(dir, recoveryAppearanceFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(snapshot, []byte("must-not-leak")) || bytes.Contains(snapshot, []byte("lockPasswordHash")) {
		t.Fatal("recovery appearance snapshot includes sensitive configuration")
	}
	if err := os.WriteFile(filepath.Join(dir, assetFileName(brandLogoAsset, cfg.Brand.LogoVersion)), []byte("logo"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, assetFileName(brandFaviconAsset, cfg.Brand.FaviconVersion)), []byte("favicon"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, assetFileName(backgroundMediaAsset, cfg.Background.Version)), []byte("background"), 0600); err != nil {
		t.Fatal(err)
	}

	rs := NewRecoveryServer(dir)
	appearanceResult := httptest.NewRecorder()
	rs.handleAppearance(appearanceResult, httptest.NewRequest("GET", "/appearance", nil))
	var appearance RecoveryAppearance
	if err := json.Unmarshal(appearanceResult.Body.Bytes(), &appearance); err != nil {
		t.Fatal(err)
	}
	if appearance.Brand.Title != cfg.Brand.Title || appearance.Background.Version != cfg.Background.Version || appearance.Background.Fit != "contain" {
		t.Fatalf("appearance = %#v", appearance)
	}

	for _, tt := range []struct {
		name        string
		path        string
		contentType string
		want        []byte
	}{
		{name: "logo", path: "/appearance/logo", contentType: "image/png", want: []byte("logo")},
		{name: "favicon", path: "/appearance/favicon", contentType: "image/x-icon", want: []byte("favicon")},
		{name: "background", path: "/appearance/background", contentType: "video/mp4", want: []byte("background")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := httptest.NewRecorder()
			rs.serveAppearanceAsset(result, httptest.NewRequest("GET", tt.path, nil), tt.name)
			if result.Code != 200 || result.Header().Get("Content-Type") != tt.contentType || !bytes.Equal(result.Body.Bytes(), tt.want) {
				t.Fatalf("asset status=%d contentType=%q data=%q", result.Code, result.Header().Get("Content-Type"), result.Body.Bytes())
			}
		})
	}
	stale := httptest.NewRecorder()
	rs.handleAppearanceLogo(stale, httptest.NewRequest(http.MethodGet, "/appearance/logo?v=9", nil))
	if stale.Code != http.StatusNoContent {
		t.Fatalf("stale recovery logo version status = %d, want %d", stale.Code, http.StatusNoContent)
	}
}

func TestRecoveryAppearanceRouteIsAvailableAfterStart(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRecoveryAppearance(dir, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	rs := NewRecoveryServer(dir)
	url, err := rs.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()

	response, err := http.Get(strings.SplitN(url, "#", 2)[0] + "appearance")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("appearance route status=%d contentType=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
}
