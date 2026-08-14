package clipvault

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThemeUtilsAssetServed(t *testing.T) {
	server := &SearchServer{}
	req := httptest.NewRequest(http.MethodGet, "http://clipvault.local/theme-utils.js", nil)
	res := httptest.NewRecorder()

	server.handleStatic(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("theme utility asset status = %d, want %d", res.Code, http.StatusOK)
	}
	if contentType := res.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/javascript") {
		t.Fatalf("theme utility content type = %q, want JavaScript", contentType)
	}
	if !strings.Contains(res.Body.String(), "ensureContrast") {
		t.Fatal("theme utility asset does not contain contrast guard")
	}
}

func TestHotkeyUtilsAssetServed(t *testing.T) {
	server := &SearchServer{}
	req := httptest.NewRequest(http.MethodGet, "http://clipvault.local/hotkey-utils.js", nil)
	res := httptest.NewRecorder()

	server.handleStatic(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("hotkey utility asset status = %d, want %d", res.Code, http.StatusOK)
	}
	if contentType := res.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/javascript") {
		t.Fatalf("hotkey utility content type = %q, want JavaScript", contentType)
	}
	if !strings.Contains(res.Body.String(), "parseVkInput") {
		t.Fatal("hotkey utility asset does not contain parser")
	}
}

func TestModuleAssetsServed(t *testing.T) {
	server := &SearchServer{}
	for _, path := range []string{"/api.js", "/store.js"} {
		res := httptest.NewRecorder()
		server.handleStatic(res, httptest.NewRequest(http.MethodGet, "http://clipvault.local"+path, nil))
		if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "application/javascript") {
			t.Fatalf("module %s status=%d content-type=%q", path, res.Code, res.Header().Get("Content-Type"))
		}
	}
	for _, path := range []string{"/tokens.css", "/history.css", "/settings.css", "/modals.css", "/responsive.css", "/components.css", "/security.css", "/groups.css", "/mobile.css", "/source-apps.css", "/utilities.css"} {
		res := httptest.NewRecorder()
		server.handleStatic(res, httptest.NewRequest(http.MethodGet, "http://clipvault.local"+path, nil))
		if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/css") {
			t.Fatalf("stylesheet %s status=%d content-type=%q", path, res.Code, res.Header().Get("Content-Type"))
		}
	}
}

func TestApplicationModulesAreServedIndependently(t *testing.T) {
	server := &SearchServer{}
	for _, path := range applicationModuleAssets {
		res := httptest.NewRecorder()
		server.handleStatic(res, httptest.NewRequest(http.MethodGet, "http://clipvault.local"+path, nil))
		if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "application/javascript") {
			t.Errorf("application module %s status=%d content-type=%q", path, res.Code, res.Header().Get("Content-Type"))
		}
	}
	for _, path := range []string{
		"/script.js", "/app/bootstrap.js", "/app/lifecycle.js", "/app/navigation.js", "/app/view-navigation.js",
	} {
		removed := httptest.NewRecorder()
		server.handleStatic(removed, httptest.NewRequest(http.MethodGet, "http://clipvault.local"+path, nil))
		if removed.Code != http.StatusNotFound {
			t.Errorf("removed module %s status=%d, want 404", path, removed.Code)
		}
	}
}

func TestApplicationPageIsComposedInOrder(t *testing.T) {
	server := &SearchServer{}
	tests := []struct {
		path        string
		present     []string
		absent      []string
		currentPage string
	}{
		{
			path:        "/",
			present:     []string{`id="viewHistory"`, `/history.css`, `/app/history-bootstrap.js`, `id="viewModal"`, `id="clearBtn"`},
			absent:      []string{`id="viewSettings"`, `/settings.css`, `/app/settings-bootstrap.js`, `id="sourceAppsModal"`, `id="restartOverlay"`},
			currentPage: `id="tabHistory" href="/" aria-current="page"`,
		},
		{
			path:        "/settings",
			present:     []string{`id="viewSettings"`, `/settings.css`, `/app/settings-bootstrap.js`, `id="sourceAppsModal"`, `id="restartOverlay"`},
			absent:      []string{`id="viewHistory"`, `/history.css`, `/app/history-bootstrap.js`, `id="viewModal"`, `id="clearBtn"`},
			currentPage: `id="tabSettings" href="/settings" aria-current="page"`,
		},
	}
	for _, tt := range tests {
		res := httptest.NewRecorder()
		server.handleIndex(res, httptest.NewRequest(http.MethodGet, "http://clipvault.local"+tt.path, nil))
		if res.Code != http.StatusOK {
			t.Fatalf("%s page status=%d", tt.path, res.Code)
		}
		body := res.Body.String()
		valid := strings.HasPrefix(strings.TrimSpace(body), "<!doctype html>") && strings.Contains(body, tt.currentPage) && strings.HasSuffix(strings.TrimSpace(body), "</html>")
		for _, required := range tt.present {
			valid = valid && strings.Contains(body, required)
		}
		for _, forbidden := range tt.absent {
			valid = valid && !strings.Contains(body, forbidden)
		}
		if !valid {
			t.Fatalf("%s page is not independently composed", tt.path)
		}
	}
}
