// Package ocr 提供有界图片预处理和可配置识别协议适配，不依赖存储或界面。
package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type Result struct {
	Text  string
	Empty bool
}
type adapter interface {
	request(Config, []byte) map[string]any
	response([]byte) (Result, error)
}

func protocol(provider string) (adapter, error) {
	switch provider {
	case Bailian, ChatCompletions:
		return chatAdapter{}, nil
	case Ollama:
		return ollamaAdapter{}, nil
	case Umi:
		return umiAdapter{}, nil
	default:
		return nil, errors.New("ocr.error.unsupported_provider")
	}
}

// Recognize 不自动重试、不重定向、不回退到其他连接；错误不含请求或响应正文。
func Recognize(ctx context.Context, c Config, key string, png []byte) (Result, error) {
	if err := c.Validate(); err != nil {
		return Result{}, err
	}
	if strings.ContainsAny(key, "\r\n") || len(key) > 4096 {
		return Result{}, errors.New("ocr.error.key_format")
	}
	if c.Provider == Bailian && strings.TrimSpace(key) == "" {
		return Result{}, errors.New("ocr.error.bailian_key")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.Location == "local" {
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if host == "localhost" {
				host = "127.0.0.1"
			}
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() {
				return nil, errors.New("ocr.error.local_target")
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(host, port))
		}
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Duration(c.TimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return recognize(ctx, client, c, key, png)
}

func recognize(ctx context.Context, client *http.Client, c Config, key string, png []byte) (Result, error) {
	if len(png) == 0 || len(png) > MaxImageBytes {
		return Result{}, errors.New("ocr.error.image_size")
	}
	a, err := protocol(c.Provider)
	if err != nil {
		return Result{}, err
	}
	body, err := json.Marshal(a.request(c, png))
	if err != nil {
		return Result{}, errors.New("ocr.error.request_prepare")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return Result{}, errors.New("ocr.error.endpoint")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, errors.New("ocr.error.network")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, &MessageError{Key: "ocr.error.http_status", Params: map[string]any{"status": resp.StatusCode}}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxTextBytes*8+1))
	if err != nil || len(raw) > MaxTextBytes*8 {
		return Result{}, errors.New("ocr.error.response_size")
	}
	return a.response(raw)
}
func textResult(text string, allowBlank bool) (Result, error) {
	text = strings.TrimSpace(text)
	if len(text) > MaxTextBytes || (!allowBlank && text == "") {
		return Result{}, errors.New("ocr.error.text_size")
	}
	if text == "[未检测到文字]" || text == "" {
		return Result{Empty: true}, nil
	}
	return Result{Text: text}, nil
}
