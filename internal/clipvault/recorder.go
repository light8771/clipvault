package clipvault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

type Recorder struct {
	cfgMgr *ConfigManager
	store  *Store
	state  *RuntimeState
	logger *DailyLogger

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
	Source  SourceInfo
}

// SourceInfo 记录一条剪贴板内容的来源进程信息（前台窗口法）。
// PID 与 hwnd 在 WM_CLIPBOARDUPDATE 派发现场同步捕获（captureForegroundSourceFast），
// exe 路径/窗口标题由 worker 异步惰性解析（resolveDetails）。
// Method 标记识别方法，当前固定为 "foreground"；空 Method 表示未捕获
// （启动补捕、配置关闭、非 Windows 平台）。
type SourceInfo struct {
	PID         uint32
	ExePath     string
	ExeName     string
	WindowTitle string
	Method      string

	// hwnd 仅用于 worker 解析窗口标题，不参与落库。
	hwnd uintptr
}

func NewRecorder(cfgMgr *ConfigManager, store *Store, state *RuntimeState, logger *DailyLogger) *Recorder {
	filter, filterErr := NewFilterEngine(cfgMgr.Get().Sensitive)
	// NewFilterEngine 失败时仍返回非 nil 空引擎（CheckText 安全），
	// 此处记日志便于排查非法规则，不回退为 nil 避免 panic。
	if filterErr != nil {
		logger.Error("filter_init_failed", filterErr, nil)
	}
	r := &Recorder{
		cfgMgr: cfgMgr,
		store:  store,
		state:  state,
		logger: logger,
		filter: filter,
	}
	// 配置变更时重建 FilterEngine（立即生效）
	cfgMgr.OnChange(func(cfg Config) error {
		newFilter, err := NewFilterEngine(cfg.Sensitive)
		if err != nil {
			// 新规则编译失败：保留旧引擎继续工作，仅记日志，不清空 filter。
			r.logger.Error("filter_rebuild_failed", err, nil)
			return nil
		}
		r.mu.Lock()
		r.filter = newFilter
		r.mu.Unlock()
		return nil
	})
	return r
}

func (r *Recorder) ProcessClipboardUpdate(source SourceInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.IsPaused() {
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

	// 来源信息是增强元数据：解析失败静默降级，绝不影响内容落库。
	if source.PID != 0 {
		source.resolveDetails()
		snapshot.Source = source
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

	metaJSON := "{}"
	if snapshot.Meta != nil {
		if data, err := json.Marshal(snapshot.Meta); err == nil {
			metaJSON = string(data)
		}
	}

	if err := r.store.Save(context.Background(), ClipboardItem{
		Kind:              snapshot.Kind,
		Format:            snapshot.Format,
		Text:              snapshot.Text,
		ByteLen:           snapshot.ByteLen,
		CharLen:           snapshot.CharLen,
		SHA256:            snapshot.SHA256,
		MetaJSON:          metaJSON,
		Sensitive:         decision.Sensitive,
		SourceExeName:     snapshot.Source.ExeName,
		SourceExePath:     snapshot.Source.ExePath,
		SourceWindowTitle: snapshot.Source.WindowTitle,
		SourceMethod:      snapshot.Source.Method,
	}); err != nil {
		// 去重状态在此处刻意保持不变：Save 失败时若已推进 lastHash，
		// 同一份剪贴板内容会被后续事件判定为重复而永远不再尝试落库（静默丢失）。
		r.logger.Error("clipboard_save_failed", err, map[string]any{
			"kind":    snapshot.Kind,
			"charLen": snapshot.CharLen,
		})
		return
	}
	// 只有落库成功后才推进去重状态，保证失败内容在下次剪贴板事件中仍可重试。
	// 本函数全程持有 r.mu，lastHash/lastKind 也仅在此处读写，故无需额外同步。
	r.lastHash = snapshot.SHA256
	r.lastKind = snapshot.Kind

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
