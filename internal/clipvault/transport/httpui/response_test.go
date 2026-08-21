package httpui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
		{"success-status failure rejected", 0, `{"ok":false,"code":"bad_input","error":"bad"}`, `"error"`},
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
			if test.name == "success-status failure rejected" && result.Code < http.StatusBadRequest {
				t.Fatalf("business failure returned HTTP %d", result.Code)
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

// TestEnvelopePassesAttachmentsThrough 验证附件下载（Content-Disposition）
// 绕过信封重包：body 原样透传。走真实 HTTP 传输层（httptest.NewServer），
// 因为 ResponseRecorder 不校验 Content-Length 超写，只有 net/http 会。
func TestEnvelopePassesAttachmentsThrough(t *testing.T) {
	body := `{"app":"ClipVault","items":[{"id":1}]}`
	ts := httptest.NewServer(Envelope(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="clipvault-data.json"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("GET attachment: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read attachment body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("attachment body = %q, want %q", got, body)
	}
	if got := resp.Header.Get("Content-Disposition"); got == "" {
		t.Fatal("Content-Disposition lost in transit")
	}
}

// TestEnvelopeAttachmentErrorsKeepEnvelope 验证错误路径（无
// Content-Disposition）仍走信封重包为 {"error":{...}} 契约。
func TestEnvelopeAttachmentErrorsKeepEnvelope(t *testing.T) {
	result := httptest.NewRecorder()
	Envelope(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"code":"unsupported_format","error":"不支持的导出格式"}`))
	}).ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/", nil))
	if result.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", result.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Body.Bytes(), &payload); err != nil {
		t.Fatalf("error body = %s", result.Body.String())
	}
	errObj, ok := payload["error"].(map[string]any)
	if !ok || errObj["code"] != "unsupported_format" {
		t.Fatalf("error envelope = %s", result.Body.String())
	}
}
