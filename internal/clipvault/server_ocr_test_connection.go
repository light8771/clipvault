package clipvault

import (
	_ "embed"
	"net/http"
)

//go:embed ocr/testcard.png
var ocrTestCard []byte

type ocrSelection struct {
	ConnectionID string `json:"connectionId"`
	Fingerprint  string `json:"fingerprint"`
	Consent      bool   `json:"consent"`
	Force        bool   `json:"force"`
}

func (s *SearchServer) handleOCRTest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Draft   ocrConfigInput `json:"draft"`
		Consent bool           `json:"consent"`
	}
	if decodeJSONRequest(w, r, &input, 32<<10) != nil {
		s.ocrError(w, 400, "ocr.error.test_format")
		return
	}
	cfg, revision, err := s.store.readOCRSettings(r.Context())
	if err != nil {
		s.ocrError(w, 409, err)
		return
	}
	if input.Draft.Revision != revision {
		s.ocrError(w, 409, "ocr.error.revision")
		return
	}
	index := -1
	for i, profile := range cfg.Profiles {
		if profile.ID == input.Draft.ID {
			index = i
			break
		}
	}
	// 测试只校验连接参数和凭据，不要求名称，也不占用已保存连接名额。
	input.Draft.Name = "test"
	if input.Draft.ID == "" {
		cfg.Profiles = nil
	}
	p, err := makeOCRProfile(input.Draft, cfg.Profiles, index)
	if err != nil {
		s.ocrError(w, 400, err)
		return
	}
	if p.Location == "cloud" && !input.Consent {
		s.ocrError(w, 400, "ocr.error.test_consent")
		return
	}
	result, err := s.recognizeOCR(r.Context(), p, ocrTestCard)
	if err != nil {
		s.ocrError(w, 502, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, map[string]any{"text": result.Text, "empty": result.Empty})
}
