package httpui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnvelopeContracts(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"success", 0, `{"ok":true,"value":7}`, `"data"`},
		{"error", http.StatusBadRequest, `{"ok":false,"code":"bad_input","error":"bad"}`, `"error"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := httptest.NewRecorder()
			Envelope(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if test.status != 0 {
					w.WriteHeader(test.status)
				}
				_, _ = w.Write([]byte(test.body))
			}).ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/", nil))
			if !strings.Contains(result.Body.String(), test.want) {
				t.Fatalf("body = %s", result.Body.String())
			}
		})
	}
}

func TestDecodeJSONIsStrict(t *testing.T) {
	var dst struct {
		Value string `json:"value"`
	}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":"ok","extra":1}`))
	if err := DecodeJSON(httptest.NewRecorder(), request, &dst, 1024); err == nil {
		t.Fatal("unknown field accepted")
	}
}
