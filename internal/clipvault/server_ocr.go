package clipvault

import (
	"errors"
	"net/http"
	"strconv"

	"clipboard_ext/internal/clipvault/domain"
	"clipboard_ext/internal/clipvault/ocr"
)

type ocrTask struct {
	Running bool   `json:"running"`
	Error   string `json:"error"`
	message any
}

func (s *SearchServer) handleOCR(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		s.ocrError(w, 400, "ocr.error.record_id")
		return
	}
	item, ok := s.loadAuthorizedItem(w, r, id)
	if !ok {
		return
	}
	images, err := itemOCRImages(item, s.env)
	if err != nil {
		s.ocrError(w, 400, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	for i := range images {
		images[i].Name = s.i18nT(images[i].Name, map[string]any{"number": images[i].number})
	}
	if r.Method == http.MethodGet && r.URL.Query().Get("image") == "" {
		s.ocrMu.Lock()
		task := s.ocrTasks[id]
		s.ocrMu.Unlock()
		if task.message != nil {
			task.Error = s.ocrMessage(task.message)
		}
		s.writeJSON(w, map[string]any{"images": images, "results": item.OCR, "task": task})
		return
	}
	imageID := r.URL.Query().Get("image")
	var selected *ocrImage
	for i := range images {
		if images[i].ID == imageID {
			selected = &images[i]
			break
		}
	}
	if selected == nil {
		s.ocrError(w, 400, "ocr.error.select_image")
		return
	}
	if r.Method == http.MethodGet {
		raw, err := readOCRImage(*selected)
		if err != nil {
			s.ocrError(w, 400, err)
			return
		}
		png, _, err := ocr.Prepare(raw)
		if err != nil {
			s.ocrError(w, 400, err)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
		return
	}
	if r.Method == http.MethodPatch {
		var input struct {
			Text string `json:"text"`
		}
		if decodeJSONRequest(w, r, &input, ocr.MaxTextBytes*6+1024) != nil || len(input.Text) > ocr.MaxTextBytes {
			s.ocrError(w, 400, "ocr.error.edit_format")
			return
		}
		err := s.store.updateImageOCR(r.Context(), id, func(records []domain.ImageOCR) ([]domain.ImageOCR, error) {
			for i := range records {
				if records[i].ImageID == imageID {
					records[i].Edited = &input.Text
					return records, nil
				}
			}
			return nil, errors.New("ocr.error.recognize_first")
		})
		if err != nil {
			s.ocrError(w, 409, "ocr.error.edit_save")
			return
		}
		s.writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if r.URL.Query().Get("action") == "copy" {
		for _, record := range item.OCR {
			if record.ImageID != imageID {
				continue
			}
			text := record.Text()
			if text == "" {
				s.ocrError(w, 400, "ocr.error.no_copy_text")
				return
			}
			if err := restoreItemFromWeb(StoredItem{RecordType: domain.HistoryRecordContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: text}, s.env.DataDir); err != nil {
				s.ocrError(w, 500, "ocr.error.copy")
				return
			}
			s.writeJSON(w, map[string]bool{"ok": true})
			return
		}
		s.ocrError(w, 400, "ocr.error.recognize_first")
		return
	}
	var input ocrSelection
	if decodeJSONRequest(w, r, &input, 1024) != nil {
		s.ocrError(w, 400, "ocr.error.request_format")
		return
	}
	cfg, err := s.store.selectedOCR(r.Context(), input.ConnectionID, input.Fingerprint)
	if err != nil {
		s.ocrError(w, 409, err)
		return
	}
	if cfg.Location == "cloud" && !input.Consent {
		s.ocrError(w, 400, "ocr.error.consent")
		return
	}
	if !s.beginOCRTask(id) {
		s.ocrError(w, 409, "ocr.error.busy")
		return
	}
	record, hit, err := s.recognizeImage(r.Context(), id, *selected, cfg, input.Force)
	s.endOCRTask(id, err)
	if err != nil {
		s.ocrError(w, 502, err)
		return
	}

	s.writeJSON(w, map[string]any{"result": record, "cached": hit})
}
