package ocr

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

type chatAdapter struct{}

func (chatAdapter) request(c Config, png []byte) map[string]any {
	body := map[string]any{}
	for key, value := range c.Extra {
		body[key] = value
	}
	body["model"], body["stream"], body[c.TokenLimitField] = c.Model, false, c.MaxTokens
	body["messages"] = []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": c.Prompt},
		map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)}},
	}}}
	return body
}
func (chatAdapter) response(raw []byte) (Result, error) {
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Finish string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Choices) != 1 {
		return Result{}, errors.New("ocr.error.chat_response")
	}
	choice := response.Choices[0]
	if choice.Finish != "stop" {
		return Result{}, errors.New("ocr.error.incomplete")
	}
	return textResult(choice.Message.Content, false)
}

type ollamaAdapter struct{}

func (ollamaAdapter) request(c Config, png []byte) map[string]any {
	body := map[string]any{}
	for key, value := range c.Extra {
		body[key] = value
	}
	body["model"], body["stream"], body["options"] = c.Model, false, map[string]any{"num_predict": c.MaxTokens}
	body["messages"] = []any{map[string]any{"role": "user", "content": c.Prompt, "images": []string{base64.StdEncoding.EncodeToString(png)}}}
	return body
}
func (ollamaAdapter) response(raw []byte) (Result, error) {
	var response struct {
		Message struct {
			Content string            `json:"content"`
			Tools   []json.RawMessage `json:"tool_calls"`
		} `json:"message"`
		Done   bool   `json:"done"`
		Reason string `json:"done_reason"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Error != "" {
		return Result{}, errors.New("ocr.error.ollama_response")
	}
	if !response.Done || response.Reason != "stop" || len(response.Message.Tools) > 0 {
		return Result{}, errors.New("ocr.error.ollama_incomplete")
	}
	return textResult(response.Message.Content, false)
}

type umiAdapter struct{}

func (umiAdapter) request(c Config, png []byte) map[string]any {
	options := map[string]any{"data.format": "text"}
	for key, value := range c.Extra {
		options[key] = value
	}
	return map[string]any{"base64": base64.StdEncoding.EncodeToString(png), "options": options}
}
func (umiAdapter) response(raw []byte) (Result, error) {
	var response struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return Result{}, errors.New("ocr.error.umi_response")
	}
	if response.Code == 101 {
		return Result{Empty: true}, nil
	}
	if response.Code != 100 {
		return Result{}, errors.New("ocr.error.umi_failed")
	}
	var text string
	if len(response.Data) == 0 || string(response.Data) == "null" || json.Unmarshal(response.Data, &text) != nil {
		return Result{}, errors.New("ocr.error.umi_text")
	}
	return textResult(text, true)
}
