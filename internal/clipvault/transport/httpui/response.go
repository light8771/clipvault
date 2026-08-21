package httpui

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type envelopeWriter struct {
	http.ResponseWriter
	status      int
	jsonBody    bytes.Buffer
	passthrough bool
}

func (w *envelopeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *envelopeWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if w.bypassEnvelope() {
		w.passthrough = true
		w.ResponseWriter.WriteHeader(status)
	}
}

// bypassEnvelope 报告响应是否绕过 JSON API 信封原样透传。
// 信封只重写 JSON API 载荷（{data}/{error}）；以下响应体已是最终形态：
//  1. 附件下载（设置了 Content-Disposition，如数据导出文件）——
//     handler 已按真实文件大小写死 Content-Length，缓冲重包会导致
//     响应体与 Content-Length 不一致而被 net/http 截断
//  2. 非 JSON 内容（文件、图片等二进制响应）
func (w *envelopeWriter) bypassEnvelope() bool {
	if w.Header().Get("Content-Disposition") != "" {
		return true
	}
	return !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json")
}

func (w *envelopeWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.passthrough {
		return w.ResponseWriter.Write(data)
	}
	return w.jsonBody.Write(data)
}

func (w *envelopeWriter) finalize() {
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
			if status < http.StatusBadRequest {
				status = http.StatusInternalServerError
			}
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

func Envelope(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := &envelopeWriter{ResponseWriter: w}
		next(wrapped, r)
		wrapped.finalize()
	})
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
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
