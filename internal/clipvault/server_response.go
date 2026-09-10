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
			message = templateT("api.error.not_found", nil)
		case http.StatusMethodNotAllowed:
			code = "method_not_allowed"
			message = templateT("api.error.method_not_allowed", nil)
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
	s.writeAPIError(w, http.StatusInternalServerError, "internal_error", s.i18nT("api.error.internal", nil), err)
}

func (s *SearchServer) writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrConfigConflict) {
		s.writeAPIError(w, http.StatusConflict, "config_conflict", s.i18nT("config.conflict", nil), err)
		return
	}
	s.writeInternalError(w, err)
}

func (s *SearchServer) writeItemLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrItemNotFound) {
		s.writeAPIError(w, http.StatusNotFound, "not_found", s.i18nT("api.error.item_not_found", nil), nil)
		return
	}
	s.writeInternalError(w, err)
}

// formatDuration 将时长格式化为当前语言的可读字符串（{n} 参数化词条）。
func (s *SearchServer) formatDuration(d time.Duration) string {
	if d >= time.Minute {
		min := int((d + time.Minute - 1) / time.Minute)
		return s.i18nT("api.duration.minutes", map[string]any{"n": min})
	}
	seconds := int((d + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return s.i18nT("api.duration.seconds", map[string]any{"n": seconds})
}

func (s *SearchServer) writeRateLimitBlocked(w http.ResponseWriter, status RateLimitStatus) {
	w.Header().Set("Retry-After", fmt.Sprintf("%d", int((status.RetryAfter+time.Second-1)/time.Second)))
	s.writeAPIError(w, http.StatusTooManyRequests, "rate_limited",
		s.i18nT("api.error.rate_limited", map[string]any{"duration": s.formatDuration(status.RetryAfter)}), nil)
}

func (s *SearchServer) writePasswordFailure(w http.ResponseWriter, label string, status RateLimitStatus) {
	if !status.Allowed {
		s.writeRateLimitBlocked(w, status)
		return
	}
	s.writeAPIError(w, http.StatusUnauthorized, "invalid_password",
		s.i18nT("api.error.invalid_password", map[string]any{"label": label, "remaining": status.Remaining}), nil)
}
