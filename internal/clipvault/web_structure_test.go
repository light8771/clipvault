package clipvault

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebUIHasNoInlineBehaviorOrPresentation(t *testing.T) {
	for _, name := range []string{"web/index.html", "web/script.js"} {
		data, err := fs.ReadFile(webFS, name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, forbidden := range []string{"onclick=", "onchange=", "oninput=", "onsubmit=", "style=", "window.do", "window.openLicense", "window.closeSecondary", "window.switchView"} {
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
