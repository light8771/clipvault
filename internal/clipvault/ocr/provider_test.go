package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrepareImage(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 64, 32))); err != nil {
		t.Fatal(err)
	}
	prepared, hash, err := Prepare(b.Bytes())
	if err != nil || len(hash) != 64 {
		t.Fatalf("prepare: %v %s", err, hash)
	}
	if _, err := png.Decode(bytes.NewReader(prepared)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{nil, []byte("不是图片"), make([]byte, MaxImageBytes+1)} {
		if _, _, err := Prepare(raw); err == nil {
			t.Fatal("accepted invalid image")
		}
	}
}

func TestProviderResponses(t *testing.T) {
	for _, test := range []struct {
		name, body       string
		status           int
		wantError, empty bool
	}{
		{"text", `{"choices":[{"message":{"content":"订单 123\\n第二行"},"finish_reason":"stop"}]}`, 200, false, false},
		{"empty", `{"choices":[{"message":{"content":"[未检测到文字]"},"finish_reason":"stop"}]}`, 200, false, true},
		{"truncated", `{"choices":[{"message":{"content":"半截"},"finish_reason":"length"}]}`, 200, true, false},
		{"blank", `{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`, 200, true, false},
		{"malformed", `not json`, 200, true, false},
		{"unauthorized", `private upstream body`, 401, true, false},
		{"oversized", strings.Repeat("a", MaxTextBytes*8+1), 200, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-key" {
					t.Error("missing authentication")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != DefaultModel {
					t.Error("invalid request")
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer srv.Close()
			cfg := DefaultConfig(ChatCompletions)
			cfg.BaseURL, cfg.Model = srv.URL, DefaultModel
			result, err := recognize(context.Background(), srv.Client(), cfg, "synthetic-key", []byte("synthetic-image"))
			if (err != nil) != test.wantError || result.Empty != test.empty {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if err != nil && (strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "synthetic-key")) {
				t.Fatal("error leaked upstream data")
			}
		})
	}
}

func TestConfigAndCacheIsolation(t *testing.T) {
	cfg := DefaultConfig(Bailian)
	cfg.BaseURL = "https://workspace.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"http://dashscope.aliyuncs.com/compatible-mode/v1", "https://example.com/compatible-mode/v1", "https://dashscope.aliyuncs.com.evil.example/compatible-mode/v1", "https://key@dashscope.aliyuncs.com/compatible-mode/v1", "https://dashscope.aliyuncs.com/compatible-mode/v1?key=secret"} {
		bad := cfg
		bad.BaseURL = address
		if bad.Validate() == nil {
			t.Fatalf("accepted %s", address)
		}
	}
	first := CacheKey("hash", cfg)
	cfg.Model = "another-model"
	if first == CacheKey("hash", cfg) {
		t.Fatal("model did not isolate cache")
	}
	cfg.Model = DefaultModel
	cfg.BaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	if first == CacheKey("hash", cfg) {
		t.Fatal("provider did not isolate cache")
	}
}
