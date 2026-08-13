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
