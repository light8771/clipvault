package clipvault

import (
	"encoding/json"
	"errors"
	"net/http"
)

func (s *SearchServer) handleI18nPackExport(w http.ResponseWriter, r *http.Request) {
	pack, err := s.store.ExportI18nLanguagePack(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		if errors.Is(err, ErrI18nLanguageNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "i18n_not_found", s.i18nT("i18n.error.not_found", nil), err)
		} else {
			s.writeError(w, err)
		}
		return
	}
	payload, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="clipvault-language-`+pack.Language.Code+`.json"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(payload)
}

func (s *SearchServer) handleI18nPackImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pack  I18nLanguagePack `json:"pack"`
		Merge bool             `json:"merge"`
	}
	if err := decodeJSONRequest(w, r, &req, maxI18nPackRequestBytes); err != nil {
		status := http.StatusBadRequest
		key := "i18n.pack.invalid"
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
			key = "i18n.pack.too_large"
		}
		s.writeAPIError(w, status, "i18n_pack_invalid", s.i18nT(key, nil), err)
		return
	}
	created, err := s.store.ImportI18nLanguagePack(r.Context(), req.Pack, req.Merge)
	if err != nil {
		status, key := http.StatusBadRequest, "i18n.pack.invalid"
		switch {
		case errors.Is(err, ErrI18nBuiltinReadonly):
			status, key = http.StatusForbidden, "i18n.pack.builtin"
		case errors.Is(err, ErrI18nLanguageExists):
			status, key = http.StatusConflict, "i18n.pack.exists"
		case errors.Is(err, errI18nPackInvalid):
		default:
			s.writeError(w, err)
			return
		}
		s.writeAPIError(w, status, "i18n_pack_rejected", s.i18nT(key, nil), err)
		return
	}
	warning := ""
	if s.i18n != nil {
		if err := s.i18n.Reload(); err != nil {
			warning = s.i18nT("i18n.pack.reload_failed", nil)
			s.logger.Warn("i18n_pack_reload_failed", map[string]any{"error": err.Error()})
		}
	}
	s.writeJSON(w, map[string]any{"ok": true, "created": created, "count": len(req.Pack.Translations), "warning": warning})
}
