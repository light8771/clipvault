package clipvault

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"clipboard_ext/internal/clipvault/ocr"
)

type ocrConfigInput struct {
	ocr.Config
	ID         string `json:"id"`
	Name       string `json:"name"`
	Revision   string `json:"revision"`
	APIKey     string `json:"apiKey"`
	ClearKey   bool   `json:"clearKey"`
	MakeActive bool   `json:"makeActive"`
	Action     string `json:"action"`
}

func (s *SearchServer) handleOCRConfig(w http.ResponseWriter, r *http.Request) {
	s.ocrConfigMu.Lock()
	defer s.ocrConfigMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	cfg, revision, err := s.store.readOCRSettings(r.Context())
	needsReset := errors.Is(err, errOCRSettings)
	if err != nil && !needsReset {
		s.ocrError(w, 500, "ocr.error.settings_read")
		return
	}
	if r.Method != http.MethodGet {
		var input ocrConfigInput
		if decodeJSONRequest(w, r, &input, 32<<10) != nil {
			s.ocrError(w, 400, "ocr.error.settings_format")
			return
		}
		if input.Revision != revision {
			s.ocrError(w, 409, "ocr.error.revision")
			return
		}
		if r.Method == http.MethodPost {
			if input.Action != "reset" {
				s.ocrError(w, 400, "ocr.error.settings_action")
				return
			}
			cfg = ocrSettings{Version: 1, Profiles: []ocrProfile{}}
		} else {
			if needsReset {
				s.ocrError(w, 409, errOCRSettings)
				return
			}
			index := -1
			for i, p := range cfg.Profiles {
				if p.ID == input.ID {
					index = i
					break
				}
			}
			if r.Method == http.MethodDelete {
				if index < 0 {
					s.ocrError(w, 404, "ocr.error.connection_missing")
					return
				}
				if cfg.ActiveID == input.ID && len(cfg.Profiles) > 1 {
					s.ocrError(w, 409, "ocr.error.active_delete")
					return
				}
				cfg.Profiles = append(cfg.Profiles[:index], cfg.Profiles[index+1:]...)
				if len(cfg.Profiles) == 0 {
					cfg.ActiveID = ""
				}
			} else {
				profile, err := makeOCRProfile(input, cfg.Profiles, index)
				if err != nil {
					s.ocrError(w, 400, err)
					return
				}
				if index < 0 {
					cfg.Profiles = append(cfg.Profiles, profile)
				} else {
					cfg.Profiles[index] = profile
				}
				if input.MakeActive || cfg.ActiveID == "" {
					cfg.ActiveID = profile.ID
				}
			}
		}
		if s.store.saveOCRSettings(r.Context(), cfg) != nil {
			s.ocrError(w, 500, "ocr.error.settings_save")
			return
		}
		cfg, revision, err = s.store.readOCRSettings(r.Context())
		if err != nil {
			s.ocrError(w, 500, "ocr.error.settings_saved_read")
			return
		}
		if s.autoOCR != nil {
			s.autoOCR.refresh()
		}
		needsReset = false
	}
	profiles := []map[string]any{}
	if !needsReset {
		for _, p := range cfg.Profiles {
			profiles = append(profiles, map[string]any{"id": p.ID, "name": p.Name, "config": p.Config, "hasKey": p.ProtectedKey != "", "fingerprint": p.fingerprint()})
		}
	}
	s.writeJSON(w, map[string]any{"profiles": profiles, "activeId": cfg.ActiveID, "revision": revision, "needsReset": needsReset, "presets": ocrPresets()})
}

func makeOCRProfile(input ocrConfigInput, profiles []ocrProfile, index int) (ocrProfile, error) {
	if err := input.Config.Validate(); err != nil {
		return ocrProfile{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 128 {
		return ocrProfile{}, errors.New("ocr.error.name")
	}
	key := strings.TrimSpace(input.APIKey)
	if len(key) > 4096 || strings.ContainsAny(key, "\r\n") || (input.ClearKey && key != "") {
		return ocrProfile{}, errors.New("ocr.error.key_format")
	}
	p := ocrProfile{ID: input.ID, Name: name, Config: input.Config}
	if index < 0 {
		if input.ID != "" {
			return p, errors.New("ocr.error.connection_reload")
		}
		if len(profiles) >= 16 {
			return p, errors.New("ocr.error.connection_limit")
		}
		p.ID = newOCRID()
	} else {
		old := profiles[index]
		p.ProtectedKey = old.ProtectedKey
		targetChanged := old.Provider != p.Provider || old.Location != p.Location || old.BaseURL != p.BaseURL || old.RequestPath != p.RequestPath
		if targetChanged && old.ProtectedKey != "" && key == "" && !input.ClearKey {
			return p, errors.New("ocr.error.key_target_changed")
		}
	}
	if input.ClearKey {
		p.ProtectedKey = ""
	}
	if key != "" {
		protected, err := DPAPIProtect([]byte(key))
		if err != nil {
			return p, errors.New("ocr.error.key_protect")
		}
		p.ProtectedKey = base64.StdEncoding.EncodeToString(protected)
	}
	return p, nil
}

func (s *SearchServer) handleOCRConnections(w http.ResponseWriter, r *http.Request) {
	cfg, _, err := s.store.readOCRSettings(r.Context())
	if err != nil {
		s.ocrError(w, 409, "ocr.error.settings_required")
		return
	}
	profiles := []map[string]any{}
	for _, p := range cfg.Profiles {
		profiles = append(profiles, p.summary())
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, map[string]any{"profiles": profiles, "activeId": cfg.ActiveID})
}
