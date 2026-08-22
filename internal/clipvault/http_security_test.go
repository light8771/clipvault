package clipvault

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateListenAddr(t *testing.T) {
	valid := []string{"127.0.0.1:15678", "localhost:15678", "[::1]:15678", "0.0.0.0:15678", "192.168.1.10:15678", "[::]:15678"}
	for _, addr := range valid {
		if _, _, err := validateListenAddr(addr); err != nil {
			t.Fatalf("expected %q to be valid: %v", addr, err)
		}
	}
	invalid := []string{":15678", "127.0.0.1:0", "127.0.0.1:80", "127.0.0.1:1023", "127.0.0.1:65536", "127.0.0.1"}
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
	}), "127.0.0.1", 15678)

	tests := []struct {
		name   string
		origin string
		token  string
		want   int
	}{
		{name: "valid", origin: "http://127.0.0.1:15678", token: "test-token", want: http.StatusNoContent},
		{name: "missing token", origin: "http://127.0.0.1:15678", want: http.StatusForbidden},
		{name: "cross origin", origin: "https://example.com", token: "test-token", want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:15678/api/v1/test", nil)
			req.Host = "127.0.0.1:15678"
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
	}), "127.0.0.1", 15678)
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
	}), "192.168.1.10", 15678)

	for _, tt := range []struct {
		host string
		want int
	}{
		{host: "192.168.1.10:15678", want: http.StatusNoContent},
		{host: "127.0.0.1:15678", want: http.StatusNoContent},
		{host: "192.168.1.11:15678", want: http.StatusForbidden},
		{host: "attacker.example:15678", want: http.StatusForbidden},
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

func TestRequestUsesHTTPSOnlyForTLSConnections(t *testing.T) {
	directRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:15678/api/v1/session", nil)
	if requestUsesHTTPS(directRequest) {
		t.Fatal("plain HTTP request was treated as HTTPS")
	}
	tlsRequest := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:15678/api/v1/session", nil)
	if !requestUsesHTTPS(tlsRequest) {
		t.Fatal("TLS request was not treated as HTTPS")
	}
}

// TestSecureHTTPSecurityErrorsAreTranslated 断言三处安全错误消息经 i18n
// 服务返回当前语言译文（zh-CN），而非硬编码英文或裸 Key——防止回退链断裂。
func TestSecureHTTPSecurityErrorsAreTranslated(t *testing.T) {
	server, _ := newI18nHTTPTestServer(t, false)
	server.csrfToken = "test-token"
	h := server.secureHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "127.0.0.1", 15678)

	tests := []struct {
		name string
		req  func() *http.Request
		code string
		want string
	}{
		{
			name: "invalid host",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "http://attacker.example/api/v1/session", nil)
				r.Host = "attacker.example"
				return r
			},
			code: "invalid_host",
			want: "访问地址无效",
		},
		{
			name: "invalid origin",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:15678/api/v1/config", nil)
				r.Host = "127.0.0.1:15678"
				r.Header.Set("Origin", "http://evil.example.com")
				return r
			},
			code: "invalid_origin",
			want: "请求来源无效",
		},
		{
			name: "csrf invalid",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:15678/api/v1/config", nil)
				r.Host = "127.0.0.1:15678"
				r.Header.Set("Origin", "http://127.0.0.1:15678")
				return r
			},
			code: "csrf_invalid",
			want: "安全校验失败，请刷新页面后重试",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, tt.req())
			if w.Code != http.StatusForbidden {
				t.Fatalf("got %d, want %d", w.Code, http.StatusForbidden)
			}
			var envelope struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode %q: %v", w.Body.String(), err)
			}
			if envelope.Error.Code != tt.code {
				t.Fatalf("code=%q, want %q", envelope.Error.Code, tt.code)
			}
			if envelope.Error.Message != tt.want {
				t.Fatalf("message=%q, want %q", envelope.Error.Message, tt.want)
			}
		})
	}
}
