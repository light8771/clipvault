package clipvault

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type apiEnvelopeWriter struct {
	http.ResponseWriter
	status      int
	jsonBody    bytes.Buffer
	passthrough bool
}

func (w *apiEnvelopeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *apiEnvelopeWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		w.passthrough = true
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *apiEnvelopeWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.passthrough {
		return w.ResponseWriter.Write(data)
	}
	return w.jsonBody.Write(data)
}

func (w *apiEnvelopeWriter) finalize() {
	if w.passthrough {
		return
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	var payload any
	if err := json.Unmarshal(w.jsonBody.Bytes(), &payload); err != nil {
		payload = map[string]any{"ok": false, "code": "invalid_response", "error": "服务响应无效"}
		status = http.StatusInternalServerError
	}
	if object, ok := payload.(map[string]any); ok {
		if failed, _ := object["ok"].(bool); status >= 400 || (object["ok"] != nil && !failed) {
			code, _ := object["code"].(string)
			if code == "" {
				code = "request_failed"
			}
			message, _ := object["error"].(string)
			if message == "" {
				message = http.StatusText(status)
			}
			payload = map[string]any{"error": map[string]any{"code": code, "message": message}}
		} else {
			payload = map[string]any{"data": payload}
		}
	} else if status >= 400 {
		payload = map[string]any{"error": map[string]any{"code": "request_failed", "message": http.StatusText(status)}}
	} else {
		payload = map[string]any{"data": payload}
	}
	w.ResponseWriter.WriteHeader(status)
	_ = json.NewEncoder(w.ResponseWriter).Encode(payload)
}

func (s *SearchServer) envelopeJSON(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := &apiEnvelopeWriter{ResponseWriter: w}
		next(wrapped, r)
		wrapped.finalize()
	})
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func (s *SearchServer) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// writeAPIError 统一 JSON 错误契约：{ok:false, code, message}。
// 原始 err 只写日志，不回传客户端（防 SQL/DuckDB 内部细节泄漏，#19）。
// 前端以 d.error 字段消费错误，message 保留为可读文案；code 供程序化判断。
func (s *SearchServer) writeAPIError(w http.ResponseWriter, status int, code, userMsg string, err error) {
	if err != nil && s.logger != nil {
		s.logger.Warn("api_error", map[string]any{"code": code, "error": err.Error()})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": code, "error": userMsg})
}

// writeInternalError 内部错误便捷封装：不透传原始 err（仅日志），统一 500。
func (s *SearchServer) writeInternalError(w http.ResponseWriter, err error) {
	s.writeAPIError(w, http.StatusInternalServerError, "internal_error", "操作失败，请查看日志", err)
}

func (s *SearchServer) writeError(w http.ResponseWriter, err error) {
	s.writeInternalError(w, err)
}

func (s *SearchServer) writeItemLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrItemNotFound) {
		s.writeAPIError(w, http.StatusNotFound, "not_found", "记录不存在", nil)
		return
	}
	s.writeInternalError(w, err)
}

// formatDuration 将时长格式化为中文可读字符串。
func formatDuration(d time.Duration) string {
	if d >= time.Minute {
		min := int((d + time.Minute - 1) / time.Minute)
		return fmt.Sprintf("%d 分钟", min)
	}
	seconds := int((d + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d 秒", seconds)
}

func (s *SearchServer) writeRateLimitBlocked(w http.ResponseWriter, status RateLimitStatus) {
	s.writeJSON(w, map[string]any{
		"ok":                     false,
		"error":                  fmt.Sprintf("5 分钟内最多尝试 10 次，请 %s 后再试", formatDuration(status.RetryAfter)),
		"remainingAttempts":      0,
		"retryAfterSeconds":      int((status.RetryAfter + time.Second - 1) / time.Second),
		"rateLimitWindowMinutes": 5,
	})
}

func (s *SearchServer) writePasswordFailure(w http.ResponseWriter, label string, status RateLimitStatus) {
	if !status.Allowed {
		s.writeRateLimitBlocked(w, status)
		return
	}
	s.writeJSON(w, map[string]any{
		"ok":                     false,
		"error":                  fmt.Sprintf("%s；5 分钟内还可尝试 %d 次", label, status.Remaining),
		"remainingAttempts":      status.Remaining,
		"rateLimitWindowMinutes": 5,
	})
}
