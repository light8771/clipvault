package clipvault

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"clipboard_ext/internal/clipvault/transport/httpui"
)

func (s *SearchServer) envelopeJSON(next http.HandlerFunc) http.Handler {
	return httpui.Envelope(next)
}

type apiContractWriter struct {
	http.ResponseWriter
	requestPath string
	suppressed  bool
}

func (w *apiContractWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *apiContractWriter) WriteHeader(status int) {
	if w.suppressed {
		return
	}
	if (w.requestPath == "/api/v1" || strings.HasPrefix(w.requestPath, "/api/v1/")) && status >= http.StatusBadRequest &&
		!strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		code := "request_failed"
		message := http.StatusText(status)
		switch status {
		case http.StatusNotFound:
			code = "not_found"
			message = "资源不存在"
		case http.StatusMethodNotAllowed:
			code = "method_not_allowed"
			message = "请求方法不允许"
		}
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.ResponseWriter.WriteHeader(status)
		_ = json.NewEncoder(w.ResponseWriter).Encode(map[string]any{
			"error": map[string]string{"code": code, "message": message},
		})
		w.suppressed = true
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *apiContractWriter) Write(data []byte) (int, error) {
	if w.suppressed {
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}

func ensureAPIErrorContract(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&apiContractWriter{ResponseWriter: w, requestPath: r.URL.Path}, r)
	})
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	return httpui.DecodeJSON(w, r, dst, maxBytes)
}

func (s *SearchServer) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// writeAPIError writes the current API error contract. envelopeJSON converts
// this internal representation to {"error":{"code","message"}}.
// Original errors are logged only and are never returned to the client.
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
	w.Header().Set("Retry-After", fmt.Sprintf("%d", int((status.RetryAfter+time.Second-1)/time.Second)))
	s.writeAPIError(w, http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("5 分钟内最多尝试 10 次，请 %s 后再试", formatDuration(status.RetryAfter)), nil)
}

func (s *SearchServer) writePasswordFailure(w http.ResponseWriter, label string, status RateLimitStatus) {
	if !status.Allowed {
		s.writeRateLimitBlocked(w, status)
		return
	}
	s.writeAPIError(w, http.StatusUnauthorized, "invalid_password", fmt.Sprintf("%s；5 分钟内还可尝试 %d 次", label, status.Remaining), nil)
}
