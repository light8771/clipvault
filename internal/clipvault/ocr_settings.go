package clipvault

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"clipboard_ext/internal/clipvault/ocr"
)

type ocrProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	ocr.Config
	ProtectedKey string `json:"protectedKey"`
}
type ocrSettings struct {
	Version  int          `json:"version"`
	ActiveID string       `json:"activeId"`
	Profiles []ocrProfile `json:"profiles"`
}

var errOCRSettings = errors.New("ocr.error.settings_incompatible")

func ocrRevision(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func (s *Store) readOCRSettings(ctx context.Context) (ocrSettings, string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT CAST(value AS VARCHAR) FROM settings WHERE key='ocr'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ocrSettings{Version: 1, Profiles: []ocrProfile{}}, ocrRevision(""), nil
	}
	if err != nil {
		return ocrSettings{}, "", err
	}
	revision := ocrRevision(raw)
	var cfg ocrSettings
	if decodeStrictJSON([]byte(raw), &cfg) != nil || cfg.Version != 1 || len(cfg.Profiles) > 16 {
		return cfg, revision, errOCRSettings
	}
	seen := map[string]bool{}
	for _, p := range cfg.Profiles {
		if !validOCRID(p.ID) || seen[p.ID] || strings.TrimSpace(p.Name) == "" || len(p.Name) > 128 || p.Config.Validate() != nil {
			return cfg, revision, errOCRSettings
		}
		seen[p.ID] = true
	}
	if (len(cfg.Profiles) > 0 && !seen[cfg.ActiveID]) || (len(cfg.Profiles) == 0 && cfg.ActiveID != "") {
		return cfg, revision, errOCRSettings
	}
	return cfg, revision, nil
}
func validOCRID(id string) bool {
	raw, err := hex.DecodeString(id)
	return err == nil && len(raw) == 16 && id == strings.ToLower(id)
}
func newOCRID() string { var raw [16]byte; _, _ = rand.Read(raw[:]); return hex.EncodeToString(raw[:]) }
func (p ocrProfile) fingerprint() string {
	return ocrRevision(p.ID + "\x00" + p.Config.Fingerprint() + "\x00" + p.ProtectedKey)
}
func (p ocrProfile) cacheKey(hash string) string { return ocr.CacheKey(hash+"\x00"+p.ID, p.Config) }
func (p ocrProfile) summary() map[string]any {
	u, _ := url.Parse(p.BaseURL)
	return map[string]any{"id": p.ID, "name": p.Name, "provider": p.Provider, "location": p.Location, "host": u.Host, "fingerprint": p.fingerprint()}
}
func (s *Store) selectedOCR(ctx context.Context, id, fingerprint string) (ocrProfile, error) {
	cfg, _, err := s.readOCRSettings(ctx)
	if err != nil {
		return ocrProfile{}, err
	}
	for _, p := range cfg.Profiles {
		if p.ID == id {
			if p.fingerprint() != fingerprint {
				return ocrProfile{}, errors.New("ocr.error.connection_changed")
			}
			return p, nil
		}
	}
	return ocrProfile{}, errors.New("ocr.error.select_connection")
}
func ocrSecret(p ocrProfile) ([]byte, error) {
	if p.ProtectedKey == "" {
		return nil, nil
	}
	protected, err := base64.StdEncoding.DecodeString(p.ProtectedKey)
	if err == nil {
		var secret []byte
		secret, err = DPAPIUnprotect(protected)
		if err == nil {
			return secret, nil
		}
	}
	return nil, errors.New("ocr.error.key_decrypt")
}
func (s *SearchServer) recognizeOCR(ctx context.Context, p ocrProfile, png []byte) (ocr.Result, error) {
	if p.Provider == ocr.Umi {
		if !s.ocrLocalMu.TryLock() {
			return ocr.Result{}, errors.New("ocr.error.umi_busy")
		}
		defer s.ocrLocalMu.Unlock()
	}
	secret, err := ocrSecret(p)
	if err != nil {
		return ocr.Result{}, err
	}
	defer clear(secret)
	return ocr.Recognize(ctx, p.Config, string(secret), png)
}
func ocrPresets() map[string]ocr.Config {
	return map[string]ocr.Config{ocr.Bailian: ocr.DefaultConfig(ocr.Bailian), ocr.ChatCompletions: ocr.DefaultConfig(ocr.ChatCompletions), ocr.Ollama: ocr.DefaultConfig(ocr.Ollama), ocr.Umi: ocr.DefaultConfig(ocr.Umi)}
}
func (s *Store) saveOCRSettings(ctx context.Context, cfg ocrSettings) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	policy, _, err := s.readOCRAuto(ctx)
	if err != nil {
		return err
	}
	fingerprint := ""
	for _, p := range cfg.Profiles {
		if p.ID == cfg.ActiveID {
			fingerprint = p.fingerprint()
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES ('ocr',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw)); err != nil {
		return err
	}
	if policy.Enabled && policy.Fingerprint != fingerprint && !policy.Suspended {
		policy.Suspended = true
		autoRaw, _ := json.Marshal(policy)
		if _, err = tx.ExecContext(ctx, `UPDATE settings SET value=? WHERE key='ocr.auto'`, string(autoRaw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
