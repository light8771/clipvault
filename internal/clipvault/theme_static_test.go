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

func TestApplicationModuleIsComposedInOrder(t *testing.T) {
	server := &SearchServer{}
	res := httptest.NewRecorder()
	server.handleStatic(res, httptest.NewRequest(http.MethodGet, "http://clipvault.local/script.js", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("application module status=%d", res.Code)
	}
	body := res.Body.String()
	if !strings.HasPrefix(body, "import { apiFetch") || !strings.Contains(body, "// ── Boot") || !strings.HasSuffix(strings.TrimSpace(body), "}());") {
		t.Fatal("application module fragments are missing or out of order")
	}
}

func TestApplicationPageIsComposedInOrder(t *testing.T) {
	server := &SearchServer{}
	res := httptest.NewRecorder()
	server.handleIndex(res, httptest.NewRequest(http.MethodGet, "http://clipvault.local/", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("application page status=%d", res.Code)
	}
	body := res.Body.String()
	if !strings.HasPrefix(strings.TrimSpace(body), "<!doctype html>") || !strings.Contains(body, "id=\"s-backup\"") || !strings.HasSuffix(strings.TrimSpace(body), "</html>") {
		t.Fatal("application page fragments are missing or out of order")
	}
}
