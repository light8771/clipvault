package clipvault

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateListenAddr(t *testing.T) {
	valid := []string{"127.0.0.1:18765", "localhost:18765", "[::1]:18765", "0.0.0.0:18765", "192.168.1.10:18765", "[::]:18765"}
	for _, addr := range valid {
		if _, _, err := validateListenAddr(addr); err != nil {
			t.Fatalf("expected %q to be valid: %v", addr, err)
		}
	}
	invalid := []string{":18765", "127.0.0.1:0", "127.0.0.1"}
	for _, addr := range invalid {
		if _, _, err := validateListenAddr(addr); err == nil {
			t.Fatalf("expected %q to be invalid", addr)
		}
	}
}

func TestSecureHTTPRejectsCrossOriginAndMissingCSRF(t *testing.T) {
	s := &SearchServer{csrfToken: "test-token"}
	h := s.secureHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "127.0.0.1", 18765)

	tests := []struct {
		name   string
		origin string
		token  string
		want   int
	}{
		{name: "valid", origin: "http://127.0.0.1:18765", token: "test-token", want: http.StatusNoContent},
		{name: "missing token", origin: "http://127.0.0.1:18765", want: http.StatusForbidden},
		{name: "cross origin", origin: "https://example.com", token: "test-token", want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18765/api/v1/test", nil)
			req.Host = "127.0.0.1:18765"
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.token != "" {
				req.Header.Set(csrfHeaderName, tt.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("got %d, want %d", w.Code, tt.want)
			}
			if tt.want == http.StatusForbidden {
				assertAPIErrorEnvelope(t, w, map[string]string{
					"missing token": "csrf_invalid",
					"cross origin":  "invalid_origin",
				}[tt.name])
			}
		})
	}
}

func assertAPIErrorEnvelope(t *testing.T, recorder *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("security response is not JSON: %v; body=%q", err, recorder.Body.String())
	}
	if envelope.Error.Code != wantCode || envelope.Error.Message == "" {
		t.Fatalf("security envelope=%+v, want code %q", envelope, wantCode)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("security API error is cacheable: %q", recorder.Header().Get("Cache-Control"))
	}
}

func TestSecureHTTPRejectsInvalidHost(t *testing.T) {
	s := &SearchServer{csrfToken: "test-token"}
	h := s.secureHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "127.0.0.1", 18765)
	req := httptest.NewRequest(http.MethodGet, "http://attacker.example/api/v1/runtime", nil)
	req.Host = "attacker.example"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d, want %d", w.Code, http.StatusForbidden)
	}
	assertAPIErrorEnvelope(t, w, "invalid_host")
}

func TestSecureHTTPAllowsConfiguredLANHostAndRejectsOtherHosts(t *testing.T) {
	s := &SearchServer{csrfToken: "test-token"}
	h := s.secureHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "192.168.1.10", 18765)

	for _, tt := range []struct {
		host string
		want int
	}{
		{host: "192.168.1.10:18765", want: http.StatusNoContent},
		{host: "127.0.0.1:18765", want: http.StatusNoContent},
		{host: "192.168.1.11:18765", want: http.StatusForbidden},
		{host: "attacker.example:18765", want: http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+"/api/v1/runtime", nil)
		req.Host = tt.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tt.want {
			t.Errorf("host %q: got %d, want %d", tt.host, w.Code, tt.want)
		}
	}
}

func TestSecureHTTPAllowsOnlyTrustedLoopbackReverseProxy(t *testing.T) {
	newHandler := func(enabled bool) http.Handler {
		s := &SearchServer{
			csrfToken: "test-token",
			reverseProxy: ReverseProxyConfig{
				Enabled:   enabled,
				PublicURL: "https://clipvault.example.com",
			},
		}
		return s.secureHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}), "127.0.0.1", 18765)
	}

	tests := []struct {
		name           string
		enabled        bool
		remoteAddr     string
		forwardedProto []string
		forwardedHost  []string
		origin         string
		want           int
	}{
		{
			name: "valid proxy request", enabled: true, remoteAddr: "127.0.0.1:54321",
			forwardedProto: []string{"https"}, forwardedHost: []string{"clipvault.example.com"},
			origin: "https://clipvault.example.com", want: http.StatusNoContent,
		},
		{
			name: "case insensitive public host", enabled: true, remoteAddr: "[::1]:54321",
			forwardedProto: []string{"https"}, forwardedHost: []string{"CLIPVAULT.EXAMPLE.COM"},
			origin: "https://clipvault.example.com", want: http.StatusNoContent,
		},
		{
			name: "feature disabled", enabled: false, remoteAddr: "127.0.0.1:54321",
			forwardedProto: []string{"https"}, forwardedHost: []string{"clipvault.example.com"},
			origin: "https://clipvault.example.com", want: http.StatusForbidden,
		},
		{
			name: "non loopback peer", enabled: true, remoteAddr: "192.168.1.20:54321",
			forwardedProto: []string{"https"}, forwardedHost: []string{"clipvault.example.com"},
			origin: "https://clipvault.example.com", want: http.StatusForbidden,
		},
		{
			name: "http forwarded scheme", enabled: true, remoteAddr: "127.0.0.1:54321",
			forwardedProto: []string{"http"}, forwardedHost: []string{"clipvault.example.com"},
			origin: "https://clipvault.example.com", want: http.StatusForbidden,
		},
		{
			name: "wrong forwarded host", enabled: true, remoteAddr: "127.0.0.1:54321",
			forwardedProto: []string{"https"}, forwardedHost: []string{"attacker.example"},
			origin: "https://clipvault.example.com", want: http.StatusForbidden,
		},
		{
			name: "duplicate forwarded scheme", enabled: true, remoteAddr: "127.0.0.1:54321",
			forwardedProto: []string{"https", "https"}, forwardedHost: []string{"clipvault.example.com"},
			origin: "https://clipvault.example.com", want: http.StatusForbidden,
		},
		{
			name: "cross origin mutation", enabled: true, remoteAddr: "127.0.0.1:54321",
			forwardedProto: []string{"https"}, forwardedHost: []string{"clipvault.example.com"},
			origin: "https://attacker.example", want: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18765/api/test", nil)
			req.Host = "127.0.0.1:18765"
			req.RemoteAddr = tt.remoteAddr
			req.Header[forwardedProtoHeaderName] = tt.forwardedProto
			req.Header[forwardedHostHeaderName] = tt.forwardedHost
			req.Header.Set("Origin", tt.origin)
			req.Header.Set(csrfHeaderName, "test-token")
			w := httptest.NewRecorder()
			newHandler(tt.enabled).ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("got %d, want %d; body=%q", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestSecureHTTPKeepsDirectLoopbackHTTPAvailableInProxyMode(t *testing.T) {
	s := &SearchServer{
		csrfToken:    "test-token",
		reverseProxy: ReverseProxyConfig{Enabled: true, PublicURL: "https://clipvault.example.com"},
	}
	h := s.secureHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "127.0.0.1", 18765)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18765/api/test", nil)
	req.Host = "127.0.0.1:18765"
	req.Header.Set("Origin", "http://127.0.0.1:18765")
	req.Header.Set(csrfHeaderName, "test-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
	}
}

func TestSessionCookieSecureAttribute(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "https"}[secure], func(t *testing.T) {
			w := httptest.NewRecorder()
			SetSessionCookie(w, "token", 10, secure)
			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("cookie count=%d", len(cookies))
			}
			if cookies[0].Secure != secure {
				t.Fatalf("Secure=%v, want %v", cookies[0].Secure, secure)
			}
		})
	}
}

func TestRequestUsesHTTPSOnlyForValidatedEndpoint(t *testing.T) {
	s := &SearchServer{reverseProxy: ReverseProxyConfig{
		Enabled:   true,
		PublicURL: "https://clipvault.example.com",
	}}
	proxyRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18765/api/auth/status", nil)
	proxyRequest.RemoteAddr = "127.0.0.1:54321"
	proxyRequest.Header.Set(forwardedProtoHeaderName, "https")
	proxyRequest.Header.Set(forwardedHostHeaderName, "clipvault.example.com")
	if !s.requestUsesHTTPS(proxyRequest) {
		t.Fatal("validated loopback proxy request was not treated as HTTPS")
	}

	spoofedRequest := proxyRequest.Clone(proxyRequest.Context())
	spoofedRequest.RemoteAddr = "192.168.1.20:54321"
	if s.requestUsesHTTPS(spoofedRequest) {
		t.Fatal("non-loopback forwarded request was treated as HTTPS")
	}

	directRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18765/api/auth/status", nil)
	if s.requestUsesHTTPS(directRequest) {
		t.Fatal("direct HTTP request was treated as HTTPS")
	}
}
