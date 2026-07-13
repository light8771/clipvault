package clipvault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"clipboard_ext/internal/licensing"
)

type Recorder struct {
	cfgMgr  *ConfigManager
	store   *Store
	state   *RuntimeState
	logger  *DailyLogger
	license *licensing.Manager

	mu       sync.Mutex
	filter   *FilterEngine
	lastHash string
	lastKind string
}

type ClipboardSnapshot struct {
	Kind    string
	Format  string
	Text    string
	ByteLen int
	CharLen int
	SHA256  string
	Meta    map[string]any
}

func NewRecorder(cfgMgr *ConfigManager, store *Store, state *RuntimeState, logger *DailyLogger, license *licensing.Manager) *Recorder {
	filter, _ := NewFilterEngine(cfgMgr.Get().Sensitive)
	r := &Recorder{
		cfgMgr:  cfgMgr,
		store:   store,
		state:   state,
		logger:  logger,
		license: license,
		filter:  filter,
	}
	// 配置变更时重建 FilterEngine（立即生效）
	cfgMgr.OnChange(func(cfg Config) error {
		if newFilter, err := NewFilterEngine(cfg.Sensitive); err == nil {
			r.mu.Lock()
			r.filter = newFilter
			r.mu.Unlock()
		}
		return nil
	})
	return r
}

func (r *Recorder) ProcessClipboardUpdate() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.IsPaused() {
		return
	}
	if r.license != nil && !r.license.CanUsePaidFeatures() {
		return
	}

	cfg := r.cfgMgr.Get()
	snapshot, err := ReadClipboardSnapshot(cfg)
	if err != nil {
		r.logger.Error("clipboard_read_failed", err, nil)
		return
	}
	if snapshot == nil {
		return
	}

	if cfg.MaxTextChars > 0 && snapshot.CharLen > cfg.MaxTextChars {
		r.logger.Info("clipboard_skipped", map[string]any{
			"reason":  "max_text_chars",
			"kind":    snapshot.Kind,
			"charLen": snapshot.CharLen,
		})
		return
	}

	decision := r.filter.CheckText(snapshot.Text)

	if snapshot.SHA256 == "" {
		snapshot.SHA256 = hashString(snapshot.Kind + "\x00" + snapshot.Format + "\x00" + snapshot.Text)
	}
	if r.lastHash == snapshot.SHA256 && r.lastKind == snapshot.Kind {
		return
	}
	r.lastHash = snapshot.SHA256
	r.lastKind = snapshot.Kind

	metaJSON := "{}"
	if snapshot.Meta != nil {
		if data, err := json.Marshal(snapshot.Meta); err == nil {
			metaJSON = string(data)
		}
	}

	if err := r.store.Save(context.Background(), ClipboardItem{
		Kind:      snapshot.Kind,
		Format:    snapshot.Format,
		Text:      snapshot.Text,
		ByteLen:   snapshot.ByteLen,
		CharLen:   snapshot.CharLen,
		SHA256:    snapshot.SHA256,
		MetaJSON:  metaJSON,
		Sensitive: decision.Sensitive,
	}); err != nil {
		r.logger.Error("clipboard_save_failed", err, map[string]any{"kind": snapshot.Kind})
		return
	}
	r.logger.Info("clipboard_saved", map[string]any{
		"kind":      snapshot.Kind,
		"format":    snapshot.Format,
		"charLen":   snapshot.CharLen,
		"byteLen":   snapshot.ByteLen,
		"sensitive": decision.Sensitive,
	})
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
