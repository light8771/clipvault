package clipvault

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIEnvelopeSuccessAndError(t *testing.T) {
	server := &SearchServer{}

	success := httptest.NewRecorder()
	server.envelopeJSON(func(w http.ResponseWriter, _ *http.Request) {
		server.writeJSON(w, map[string]any{"value": 7})
	}).ServeHTTP(success, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
	var successBody map[string]any
	if err := json.Unmarshal(success.Body.Bytes(), &successBody); err != nil {
		t.Fatal(err)
	}
	if successBody["data"] == nil || successBody["error"] != nil {
		t.Fatalf("success envelope = %s", success.Body.String())
	}

	failure := httptest.NewRecorder()
	server.envelopeJSON(func(w http.ResponseWriter, _ *http.Request) {
		server.writeAPIError(w, http.StatusBadRequest, "invalid", "bad request", nil)
	}).ServeHTTP(failure, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
	var failureBody map[string]any
	if err := json.Unmarshal(failure.Body.Bytes(), &failureBody); err != nil {
		t.Fatal(err)
	}
	if failure.Code != http.StatusBadRequest || failureBody["error"] == nil || failureBody["data"] != nil {
		t.Fatalf("error envelope = status %d body %s", failure.Code, failure.Body.String())
	}
}

func TestAPIEnvelopeLeavesDownloadsUnwrapped(t *testing.T) {
	server := &SearchServer{}
	result := httptest.NewRecorder()
	server.envelopeJSON(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("download"))
	}).ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/api/v1/download", nil))
	if result.Body.String() != "download" {
		t.Fatalf("download body = %q", result.Body.String())
	}
}

func TestAPIEnvelopeNeverReturnsBusinessFailureAsSuccess(t *testing.T) {
	server := &SearchServer{}
	tests := []struct {
		name   string
		status int
		code   string
	}{
		{name: "invalid request", status: http.StatusBadRequest, code: "invalid_request"},
		{name: "unauthorized", status: http.StatusUnauthorized, code: "invalid_password"},
		{name: "forbidden", status: http.StatusForbidden, code: "secondary_required"},
		{name: "rate limited", status: http.StatusTooManyRequests, code: "rate_limited"},
		{name: "server failure", status: http.StatusInternalServerError, code: "operation_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := httptest.NewRecorder()
			server.envelopeJSON(func(w http.ResponseWriter, _ *http.Request) {
				server.writeAPIError(w, test.status, test.code, "message", nil)
			}).ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
			if result.Code != test.status {
				t.Fatalf("status=%d, want %d", result.Code, test.status)
			}
			var envelope struct {
				Data  any `json:"data"`
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(result.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Data != nil || envelope.Error == nil || envelope.Error.Code != test.code || envelope.Error.Message != "message" {
				t.Fatalf("envelope=%s", result.Body.String())
			}
		})
	}
}
