package clipvault

import (
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
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18765/api/test", nil)
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
		})
	}
}

func TestSecureHTTPRejectsInvalidHost(t *testing.T) {
	s := &SearchServer{csrfToken: "test-token"}
	h := s.secureHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "127.0.0.1", 18765)
	req := httptest.NewRequest(http.MethodGet, "http://attacker.example/api/state", nil)
	req.Host = "attacker.example"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d, want %d", w.Code, http.StatusForbidden)
	}
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
		req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+"/api/state", nil)
		req.Host = tt.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tt.want {
			t.Errorf("host %q: got %d, want %d", tt.host, w.Code, tt.want)
		}
	}
}
