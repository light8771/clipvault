package clipvault

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebUIHasNoInlineBehaviorOrPresentation(t *testing.T) {
	files := append([]string{}, applicationPageParts...)
	for _, asset := range applicationModuleAssets {
		files = append(files, "web"+asset)
	}
	for _, name := range files {
		data, err := fs.ReadFile(webFS, name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, forbidden := range []string{"onclick=", "onchange=", "oninput=", "onsubmit=", "style=", "window.do", "window.openLicense", "window.closeSecondary", "switchView", "switch-view", "viewSwitchSequence"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains forbidden inline/global pattern %q", name, forbidden)
			}
		}
	}
}

func TestMainCSPRequiresExternalScriptsAndStyles(t *testing.T) {
	server := &SearchServer{reverseProxy: ReverseProxyConfig{}}
	handler := server.secureHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}), "127.0.0.1", 18765)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18765/", nil))
	csp := result.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP still allows inline code: %s", csp)
	}
}

func TestSecurityPagesRequireExternalScriptsAndStyles(t *testing.T) {
	for name, page := range map[string]string{
		"startup":  startupGateTemplate.Tree.Root.String(),
		"recovery": recoveryHTML,
	} {
		for _, forbidden := range []string{"<style", "<script>", "onclick=", "onchange=", "oninput=", "onsubmit=", " style="} {
			if strings.Contains(page, forbidden) {
				t.Errorf("%s page contains inline behavior or presentation %q", name, forbidden)
			}
		}
	}

	startup := NewStartupGate(nil, t.TempDir(), startupGateSetCredentials)
	startup.port = 18765
	startupResult := httptest.NewRecorder()
	startup.secureHTTP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(
		startupResult, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18765/", nil),
	)
	recovery := NewRecoveryServer(t.TempDir())
	recovery.port = 18766
	recoveryResult := httptest.NewRecorder()
	recovery.secureHTTP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(
		recoveryResult, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18766/", nil),
	)
	for name, csp := range map[string]string{
		"startup":  startupResult.Header().Get("Content-Security-Policy"),
		"recovery": recoveryResult.Header().Get("Content-Security-Policy"),
	} {
		if strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "style-src 'self'") {
			t.Errorf("%s CSP does not require external scripts and styles: %s", name, csp)
		}
	}
}

func TestSecurityPageAssetsAreEmbedded(t *testing.T) {
	for _, name := range []string{"web/startup.css", "web/startup.js", "web/recovery.css", "web/recovery.js"} {
		data, err := fs.ReadFile(webFS, name)
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestRemovedSinglePageShellIsNotEmbedded(t *testing.T) {
	for _, name := range []string{"web/pages/shell.html", "web/pages/overlays.html"} {
		if _, err := fs.ReadFile(webFS, name); err == nil {
			t.Errorf("removed single-page asset %s is still embedded", name)
		}
	}
}

func TestStandaloneSecurityErrorsUseCurrentEnvelope(t *testing.T) {
	tests := []struct {
		name    string
		handler http.Handler
		url     string
	}{
		{
			name:    "startup",
			handler: (&StartupGate{token: "token", port: 18765}).secureHTTP(http.NotFoundHandler()),
			url:     "http://attacker.invalid:18765/api/v1/startup/status",
		},
		{
			name:    "recovery",
			handler: (&RecoveryServer{token: "token", port: 18766}).secureHTTP(http.NotFoundHandler()),
			url:     "http://attacker.invalid:18766/api/v1/recovery/unlock",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := httptest.NewRecorder()
			tt.handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, tt.url, nil))
			if result.Code != http.StatusForbidden || result.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d cache=%q body=%s", result.Code, result.Header().Get("Cache-Control"), result.Body.String())
			}
			var response struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil || response.Error.Code != "invalid_host" || response.Error.Message == "" {
				t.Fatalf("invalid current error envelope: body=%s err=%v", result.Body.String(), err)
			}
		})
	}
}
