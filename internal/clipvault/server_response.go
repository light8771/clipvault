package clipvault

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"clipboard_ext/internal/clipvault/transport/httpui"
)

func (s *SearchServer) envelopeJSON(next http.HandlerFunc) http.Handler {
	return httpui.Envelope(next)
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	return httpui.DecodeJSON(w, r, dst, maxBytes)
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
