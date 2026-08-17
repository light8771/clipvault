package clipvault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

// Recorder 是事件优先（event-first）的剪贴板录制状态机：
// 每一次 WM_CLIPBOARDUPDATE 都必然产生一条历史记录，区别只在内容状态——
// 正常内容、重复事件（is_duplicate）、或内容不可用（content_available=false +
// unavailable_reason）。任何分支都不允许静默丢事件。
type Recorder struct {
	cfgMgr    *ConfigManager
	store     *Store
	state     *RuntimeState
	logger    *DailyLogger
	imagesDir string // 托管图片根目录（<DataDir>/images）；空 = 不落盘图片

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
	// ImageBytes 是图片事件的 PNG 字节（仅内存态，由 Recorder 写入托管文件，
	// 不直接入库；入库的是 meta.imagePath 指向的托管文件路径）。
	ImageBytes []byte
}

// ErrImageLimitExceeded 表示剪贴板图片超过 image.maxBytes 限制。
// 平台层用它让 Recorder 落 content_available=false / image_limit_exceeded 记录。
var ErrImageLimitExceeded = errors.New("clipboard image exceeds configured size limit")

// internalRestoreFormatName 是搜索弹窗恢复内容时写入剪贴板的私有注册格式名。
// 录制链路发现该格式时跳过本次事件（恢复不是用户复制）。
const internalRestoreFormatName = "CLIPVAULT_INTERNAL_RESTORE"

// SourceInfo 记录一条剪贴板内容的来源进程信息（前台窗口法）。
// PID 与 hwnd 在 WM_CLIPBOARDUPDATE 派发现场同步捕获（captureForegroundSourceFast），
// exe 路径/窗口标题由 worker 异步惰性解析（resolveDetails）。
// Method 标记识别方法：实时事件为 "foreground"，启动补捕为
// "clipboard_owner"；空 Method 表示未捕获（配置关闭或非 Windows 平台）。
type SourceInfo struct {
	PID         uint32
	ExePath     string
	ExeName     string
	WindowTitle string
	Method      string

	// hwnd 仅用于 worker 解析窗口标题，不参与落库。
	hwnd uintptr
}

func NewRecorder(cfgMgr *ConfigManager, store *Store, state *RuntimeState, logger *DailyLogger, imagesDir string) *Recorder {
	filter, filterErr := NewFilterEngine(cfgMgr.Get().Sensitive)
	// NewFilterEngine 失败时仍返回非 nil 空引擎（CheckText 安全），
	// 此处记日志便于排查非法规则，不回退为 nil 避免 panic。
	if filterErr != nil {
		logger.Error("filter_init_failed", filterErr, nil)
	}
	r := &Recorder{
		cfgMgr:    cfgMgr,
		store:     store,
		state:     state,
		logger:    logger,
		imagesDir: imagesDir,
		filter:    filter,
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

// ProcessClipboardUpdate 处理一次剪贴板变化事件。
// capturedAtMS 是事件在消息泵派发现场的时间（Unix 毫秒），它穿过队列直达
// 数据库，worker 拥塞多久都不会污染历史时间。
func (r *Recorder) ProcessClipboardUpdate(source SourceInfo, sequence uint32, capturedAtMS int64) {
	if capturedAtMS <= 0 {
		capturedAtMS = time.Now().UnixMilli()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.IsPaused() {
		return
	}

	cfg := r.cfgMgr.Get()
	snapshot, formats, err := readClipboardEventForSequence(cfg, sequence)
	if err != nil {
		if errors.Is(err, ErrClipboardSuperseded) {
			// 事件在读取前被更新的复制覆盖：内容已不可得，但事件必须落库。
			r.saveEventOnly(source, capturedAtMS, domain.ReasonSuperseded, formats)
			return
		}
		if errors.Is(err, ErrImageLimitExceeded) {
			// 图片超过配置大小：事件落库，内容不可用。
			r.saveUnavailable(&ClipboardSnapshot{
				Kind:   domain.KindImage,
				Format: domain.FormatEventOnly,
				Source: source,
			}, capturedAtMS, domain.ReasonImageLimitExceeded, nil)
			return
		}
		r.logger.Error("clipboard_read_failed", err, nil)
		r.saveEventOnly(source, capturedAtMS, domain.ReasonReadFailed, formats)
		return
	}
	if snapshot == nil {
		// 不支持的格式：事件落库，格式清单写入元数据。
		r.saveEventOnly(source, capturedAtMS, domain.ReasonUnsupportedFormat, formats)
		return
	}
	if containsString(formats, internalRestoreFormatName) {
		// 内部恢复（搜索弹窗双击/Ctrl+Enter）写回的剪贴板内容不再录制。
		return
	}

	// 来源信息是增强元数据：解析失败静默降级，绝不影响内容落库。
	if source.PID != 0 {
		source.resolveDetails()
		snapshot.Source = source
	}

	// 图片：PNG 字节写入托管目录，入库记录引用托管文件路径。
	if len(snapshot.ImageBytes) > 0 {
		relPath, err := r.saveManagedImage(snapshot.ImageBytes, capturedAtMS, snapshot.SHA256)
		if err != nil {
			r.logger.Error("clipboard_image_save_failed", err, nil)
			r.saveEventOnly(source, capturedAtMS, domain.ReasonReadFailed, formats)
			return
		}
		snapshot.Text = filepath.ToSlash(relPath)
		if snapshot.Meta == nil {
			snapshot.Meta = map[string]any{}
		}
		snapshot.Meta["imagePath"] = snapshot.Text
	}

	// 文本长度超限：事件落库，内容不可用（不截断保存，避免半个敏感串落盘）。
	if cfg.MaxTextChars > 0 && snapshot.CharLen > cfg.MaxTextChars && len(snapshot.ImageBytes) == 0 {
		meta := map[string]any{
			"charLen": snapshot.CharLen,
			"limit":   cfg.MaxTextChars,
		}
		r.saveUnavailable(snapshot, capturedAtMS, domain.ReasonTextLimitExceeded, meta)
		return
	}

	var sensitive bool
	if snapshot.Text != "" {
		sensitive = r.filter.CheckText(snapshot.Text).Sensitive
	}

	dedupeHash := snapshot.SHA256
	if dedupeHash == "" {
		dedupeHash = hashString(snapshot.Kind + "\x00" + snapshot.Format + "\x00" + snapshot.Text)
	}
	isDuplicate := r.lastHash == dedupeHash && r.lastKind == snapshot.Kind

	metaJSON := "{}"
	if snapshot.Meta != nil {
		if data, err := json.Marshal(snapshot.Meta); err == nil {
			metaJSON = string(data)
		}
	}

	if err := r.store.Save(context.Background(), ClipboardItem{
		CapturedAtMS:      capturedAtMS,
		Kind:              snapshot.Kind,
		Format:            snapshot.Format,
		Text:              snapshot.Text,
		MetaJSON:          metaJSON,
		Sensitive:         sensitive,
		ContentAvailable:  true,
		IsDuplicate:       isDuplicate,
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
	r.lastHash = dedupeHash
	r.lastKind = snapshot.Kind

	r.logger.Info("clipboard_saved", map[string]any{
		"kind":        snapshot.Kind,
		"format":      snapshot.Format,
		"charLen":     snapshot.CharLen,
		"byteLen":     snapshot.ByteLen,
		"sensitive":   sensitive,
		"isDuplicate": isDuplicate,
	})
}

// saveEventOnly 保存一条「只发生过事件、没有内容」的记录。
// 用于 superseded / unsupported_format / read_failed 等分支。
func (r *Recorder) saveEventOnly(source SourceInfo, capturedAtMS int64, reason string, formats []string) {
	meta := map[string]any{}
	if len(formats) > 0 {
		meta["formats"] = formats
	}
	r.saveUnavailable(&ClipboardSnapshot{
		Kind:   domain.KindOther,
		Format: domain.FormatEventOnly,
		Source: source,
		Meta:   meta,
	}, capturedAtMS, reason, nil)
}

// saveUnavailable 以 content_available=false 保存事件记录。
// snapshot.Meta 与 extra 合并后入库（extra 优先）。
func (r *Recorder) saveUnavailable(snapshot *ClipboardSnapshot, capturedAtMS int64, reason string, extra map[string]any) {
	meta := map[string]any{}
	for k, v := range snapshot.Meta {
		meta[k] = v
	}
	for k, v := range extra {
		meta[k] = v
	}
	metaJSON := "{}"
	if len(meta) > 0 {
		if data, err := json.Marshal(meta); err == nil {
			metaJSON = string(data)
		}
	}
	// event-only / 不可用记录的去重键包含原因：同一原因连续发生才标记重复。
	dedupeHash := hashString(snapshot.Kind + "\x00" + snapshot.Format + "\x00" + reason)
	isDuplicate := r.lastHash == dedupeHash && r.lastKind == snapshot.Kind

	if err := r.store.Save(context.Background(), ClipboardItem{
		CapturedAtMS:      capturedAtMS,
		Kind:              snapshot.Kind,
		Format:            snapshot.Format,
		Text:              "",
		MetaJSON:          metaJSON,
		ContentAvailable:  false,
		IsDuplicate:       isDuplicate,
		UnavailableReason: reason,
		SourceExeName:     snapshot.Source.ExeName,
		SourceExePath:     snapshot.Source.ExePath,
		SourceWindowTitle: snapshot.Source.WindowTitle,
		SourceMethod:      snapshot.Source.Method,
	}); err != nil {
		r.logger.Error("clipboard_event_save_failed", err, map[string]any{"reason": reason})
		return
	}
	r.lastHash = dedupeHash
	r.lastKind = snapshot.Kind
	r.logger.Info("clipboard_event_saved", map[string]any{
		"kind":        snapshot.Kind,
		"reason":      reason,
		"isDuplicate": isDuplicate,
	})
}

// saveManagedImage 把 PNG 字节写入托管目录（UTC 年/月两级），
// 返回相对 DataDir 的路径（images/2006/01/<unix_ms>_<hash8>.png）。
func (r *Recorder) saveManagedImage(pngBytes []byte, capturedAtMS int64, contentHash string) (string, error) {
	if r.imagesDir == "" {
		return "", errors.New("image directory is not configured")
	}
	t := time.UnixMilli(capturedAtMS).UTC()
	dir := filepath.Join(r.imagesDir, t.Format("2006"), t.Format("01"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir image dir: %w", err)
	}
	hash8 := contentHash
	if len(hash8) > 8 {
		hash8 = hash8[:8]
	}
	if hash8 == "" {
		hash8 = "00000000"
	}
	name := fmt.Sprintf("%d_%s.png", capturedAtMS, hash8)
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, pngBytes, 0o600); err != nil {
		return "", fmt.Errorf("write image file: %w", err)
	}
	return filepath.Join(filepath.Base(r.imagesDir), t.Format("2006"), t.Format("01"), name), nil
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
