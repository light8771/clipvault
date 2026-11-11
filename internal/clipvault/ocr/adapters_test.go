package ocr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLocalProtocolRequests(t *testing.T) {
	for _, provider := range []string{ChatCompletions, Ollama, Umi} {
		t.Run(provider, func(t *testing.T) {
			png := []byte("synthetic image bytes")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Method != "POST" {
					t.Error("unexpected authentication or method")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Fatal("invalid body")
				}
				encoded := base64.StdEncoding.EncodeToString(png)
				switch provider {
				case Umi:
					if r.URL.Path != "/api/ocr" || body["base64"] != encoded {
						t.Error("wrong Umi request")
					}
					options := body["options"].(map[string]any)
					if options["data.format"] != "text" || options["ocr.cls"] != true {
						t.Error("missing Umi options")
					}
					_, _ = w.Write([]byte(`{"code":100,"data":"订单\n12345"}`))
				case Ollama:
					if r.URL.Path != "/api/chat" || body["stream"] != false || body["think"] != false {
						t.Error("wrong Ollama options")
					}
					message := body["messages"].([]any)[0].(map[string]any)
					if message["images"].([]any)[0] != encoded || body["options"].(map[string]any)["num_predict"] != float64(16384) {
						t.Error("wrong Ollama image/token limit")
					}
					_, _ = w.Write([]byte(`{"message":{"content":"订单\n12345"},"done":true,"done_reason":"stop"}`))
				default:
					if r.URL.Path != "/v1/custom-chat" || body["stream"] != false || body["max_completion_tokens"] != float64(16384) || body["temperature"] != float64(0) {
						t.Error("wrong compatible API options")
					}
					content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
					if content[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/png;base64,"+encoded {
						t.Error("wrong image encoding")
					}
					_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"订单\n12345"},"finish_reason":"stop"}]}`))
				}
			}))
			defer srv.Close()
			cfg := DefaultConfig(provider)
			cfg.BaseURL = srv.URL
			cfg.Location = "local"
			switch provider {
			case Umi:
				cfg.Extra = map[string]json.RawMessage{"ocr.cls": json.RawMessage("true")}
			case Ollama:
				cfg.Model = "vision-test"
				cfg.Extra = map[string]json.RawMessage{"think": json.RawMessage("false")}
			default:
				cfg.Model = "vision-test"
				cfg.BaseURL += "/v1"
				cfg.RequestPath = "/custom-chat"
				cfg.TokenLimitField = "max_completion_tokens"
				cfg.Extra = map[string]json.RawMessage{"temperature": json.RawMessage("0")}
			}
			result, err := Recognize(context.Background(), cfg, "", png)
			if err != nil || result.Text != "订单\n12345" || result.Empty {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestLocalProtocolResponseFailures(t *testing.T) {
	for _, tc := range []struct {
		provider, body string
		empty, fail    bool
	}{
		{Umi, `{"code":101,"data":[]}`, true, false},
		{Umi, `{"code":100,"data":""}`, true, false},
		{Umi, `{"code":100,"data":null}`, false, true},
		{Umi, `{"code":100,"data":[]}`, false, true},
		{Umi, `{"code":500,"data":"private response"}`, false, true},
		{Ollama, `{"message":{"content":"half"},"done":false}`, false, true},
		{Ollama, `{"message":{"content":"half"},"done":true,"done_reason":"length"}`, false, true},
		{Ollama, `{"message":{"content":"x","tool_calls":[{}]},"done":true,"done_reason":"stop"}`, false, true},
		{Ollama, `{"error":"private response"}`, false, true},
	} {
		a, _ := protocol(tc.provider)
		result, err := a.response([]byte(tc.body))
		if (err != nil) != tc.fail || result.Empty != tc.empty {
			t.Fatalf("%s: %+v %v", tc.body, result, err)
		}
		if err != nil && strings.Contains(err.Error(), "private") {
			t.Fatal("response leaked")
		}
	}
}

func TestConnectionBoundariesAndCache(t *testing.T) {
	cfg := DefaultConfig(Umi)
	for _, address := range []string{"http://192.168.1.2:1224", "http://example.org", "file:///image", "http://127.0.0.1:1224?token=x"} {
		bad := cfg
		bad.BaseURL = address
		if bad.Validate() == nil {
			t.Errorf("accepted local address %s", address)
		}
	}
	for _, path := range []string{"//evil.example/", "/../ocr", "/api/../ocr", "/api/ocr?key=x", "/%2e/ocr", "https://example.org"} {
		bad := cfg
		bad.RequestPath = path
		if bad.Validate() == nil {
			t.Errorf("accepted request path %s", path)
		}
	}
	cfg = DefaultConfig(ChatCompletions)
	cfg.Model = "test"
	cfg.BaseURL = "https://example.org/v1"
	first := CacheKey("image", cfg)
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Prompt += "\n其他提示" },
		func(c *Config) { c.RequestPath = "/v2/chat" },
		func(c *Config) { c.TokenLimitField = "max_completion_tokens" },
		func(c *Config) { c.Extra = map[string]json.RawMessage{"temperature": json.RawMessage("0")} },
	} {
		changed := cfg
		mutate(&changed)
		if CacheKey("image", changed) == first {
			t.Error("configuration shares cache")
		}
	}
	for _, key := range []string{"messages", "Authorization", "stream", "tools", "model"} {
		bad := cfg
		bad.Extra = map[string]json.RawMessage{key: json.RawMessage("null")}
		if bad.Validate() == nil {
			t.Errorf("accepted reserved field %s", key)
		}
	}
	cfg.Extra = map[string]json.RawMessage{"seed": json.RawMessage("9007199254740992")}
	first = CacheKey("image", cfg)
	cfg.Extra["seed"] = json.RawMessage("9007199254740993")
	if first == CacheKey("image", cfg) {
		t.Fatal("large integer parameters share a cache key")
	}
}

func TestNoRedirectRetryOrCanceledUpload(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(307)
	}))
	defer srv.Close()
	cfg := DefaultConfig(Umi)
	cfg.BaseURL = srv.URL
	if _, err := Recognize(context.Background(), cfg, "", []byte("test")); err == nil || calls.Load() != 1 {
		t.Fatalf("redirect calls=%d err=%v", calls.Load(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Recognize(ctx, cfg, "", []byte("test")); err == nil || calls.Load() != 1 {
		t.Fatalf("canceled upload calls=%d err=%v", calls.Load(), err)
	}
}
