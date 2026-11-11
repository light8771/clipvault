package clipvault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"clipboard_ext/internal/clipvault/ocr"
)

// 自动策略独立存储，不改变连接格式，不随记录导出。
type ocrAutoPolicy struct {
	Version     int    `json:"version"`
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
	Suspended   bool   `json:"suspended"`
}

func (s *Store) readOCRAuto(ctx context.Context) (ocrAutoPolicy, string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT CAST(value AS VARCHAR) FROM settings WHERE key='ocr.auto'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ocrAutoPolicy{Version: 1}, ocrRevision(""), nil
	}
	if err != nil {
		return ocrAutoPolicy{}, "", err
	}
	var p ocrAutoPolicy
	if decodeStrictJSON([]byte(raw), &p) != nil || p.Version != 1 || (p.Enabled && !validContentSHA256(p.Fingerprint)) {
		return p, ocrRevision(raw), errors.New("ocr.error.auto_settings")
	}
	return p, ocrRevision(raw), nil
}

func (s *Store) defaultOCR(ctx context.Context) (ocrProfile, error) {
	cfg, _, err := s.readOCRSettings(ctx)
	if err != nil {
		return ocrProfile{}, err
	}
	for _, p := range cfg.Profiles {
		if p.ID == cfg.ActiveID {
			return p, nil
		}
	}
	return ocrProfile{}, errors.New("ocr.error.select_connection")
}

func validateAutoProfile(p ocrProfile) error {
	if err := p.Config.Validate(); err != nil {
		return err
	}
	secret, err := ocrSecret(p)
	if err != nil {
		return err
	}
	defer clear(secret)
	if p.Provider == ocr.Bailian && strings.TrimSpace(string(secret)) == "" {
		return errors.New("ocr.error.bailian_key")
	}
	return nil
}

func (s *SearchServer) handleOCRAuto(w http.ResponseWriter, r *http.Request) {
	s.ocrConfigMu.Lock()
	defer s.ocrConfigMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	policy, revision, err := s.store.readOCRAuto(r.Context())
	if err != nil {
		s.ocrError(w, 409, err)
		return
	}
	p, profileErr := s.store.defaultOCR(r.Context())
	if r.Method == http.MethodPut {
		var input struct {
			Enabled     *bool  `json:"enabled"`
			Consent     bool   `json:"consent"`
			Fingerprint string `json:"fingerprint"`
			Revision    string `json:"revision"`
		}
		if decodeJSONRequest(w, r, &input, 2048) != nil || input.Enabled == nil {
			s.ocrError(w, 400, "ocr.error.request_format")
			return
		}
		if input.Revision != revision {
			s.ocrError(w, 409, "ocr.error.revision")
			return
		}
		if *input.Enabled {
			if profileErr != nil {
				s.ocrError(w, 400, profileErr)
				return
			}
			if input.Fingerprint != p.fingerprint() {
				s.ocrError(w, 409, "ocr.error.connection_changed")
				return
			}
			if err := validateAutoProfile(p); err != nil {
				s.ocrError(w, 400, err)
				return
			}
			if p.Location == "cloud" && !input.Consent {
				s.ocrError(w, 400, "ocr.error.auto_consent")
				return
			}
		}
		policy = ocrAutoPolicy{Version: 1, Enabled: *input.Enabled}
		if *input.Enabled {
			policy.Fingerprint = p.fingerprint()
		}
		raw, _ := json.Marshal(policy)
		if _, err := s.store.db.ExecContext(r.Context(), `INSERT INTO settings(key,value) VALUES ('ocr.auto',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw)); err != nil {
			s.ocrError(w, 500, "ocr.error.settings_save")
			return
		}
		revision = ocrRevision(string(raw))
		if s.autoOCR != nil {
			s.autoOCR.refresh()
		}
	}
	var connection any
	if profileErr == nil {
		connection = p.summary()
	}
	effective := policy.Enabled && !policy.Suspended && profileErr == nil && policy.Fingerprint == p.fingerprint() && validateAutoProfile(p) == nil
	s.writeJSON(w, map[string]any{"enabled": policy.Enabled, "effective": effective, "revision": revision, "connection": connection})
}
