package clipvault

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	"clipboard_ext/internal/clipvault/domain"
)

func (s *SearchServer) handleCompositePreview(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_id", nil), nil)
		return
	}
	item, ok := s.loadAuthorizedItem(w, r, id)
	if !ok {
		return
	}
	if item.Format != domain.FormatComposite || item.Content == nil {
		s.writeAPIError(w, http.StatusBadRequest, "content_unavailable", s.i18nT("history.content_action_unavailable", nil), nil)
		return
	}
	assets := map[string]string{}
	for _, a := range item.Content.Assets {
		assets[a.ID] = "data:" + a.MediaType + ";base64," + base64.StdEncoding.EncodeToString(a.Data)
	}
	hasPreviewImage := false
	preview := compositePreviewHTML(item.Content, func(id string) string {
		url := assets[id]
		hasPreviewImage = hasPreviewImage || url != ""
		return url
	})
	images := []string{}
	files := []string{}
	textContent := *item.Content
	textContent.Representations = nil
	for _, repr := range item.Content.Representations {
		if repr.Format == compositeFiles {
			if err := json.Unmarshal(repr.Data, &files); err != nil {
				s.writeError(w, err)
				return
			}
		} else {
			textContent.Representations = append(textContent.Representations, repr)
		}
	}
	if !hasPreviewImage {
		for _, repr := range item.Content.Representations {
			if repr.Format == compositePNG {
				images = append(images, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(repr.Data))
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, map[string]any{"html": preview, "text": compositePasteText(&textContent), "images": images, "files": files, "incomplete": len(item.Content.Issues) > 0, "issues": item.Content.Issues})
}

func (s *SearchServer) handleRestoreItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_id", nil), nil)
		return
	}
	item, ok := s.loadAuthorizedItem(w, r, id)
	if !ok {
		return
	}
	if err := restoreItemFromWeb(item, s.env.DataDir); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}
