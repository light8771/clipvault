package clipvault

import (
	"errors"
	"net/http"
	"strings"

	"clipboard_ext/internal/clipvault/ocr"
)

func (s *SearchServer) ocrMessage(message any) string {
	key := "ocr.error.failed"
	var params map[string]any
	switch value := message.(type) {
	case string:
		key = value
	case error:
		var localized *ocr.MessageError
		if errors.As(value, &localized) {
			key, params = localized.Key, localized.Params
		} else {
			key = value.Error()
		}
	}
	if !strings.HasPrefix(key, "ocr.error.") {
		key = "ocr.error.failed"
	}
	return s.i18nT(key, params)
}

func (s *SearchServer) ocrError(w http.ResponseWriter, status int, message any) {
	s.writeAPIError(w, status, "ocr_error", s.ocrMessage(message), nil)
}
