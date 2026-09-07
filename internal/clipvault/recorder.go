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

// Recorder 保存可被检查的剪贴板状态。已经被新 sequence 覆盖的瞬态通知
// 没有可恢复的内容，因此不会被伪装成用户可见的失败记录。
type Recorder struct {
	cfgMgr    *ConfigManager
	store     *Store
	state     *RuntimeState
	logger    *DailyLogger
	imagesDir string // 托管图片根目录（<DataDir>/images）；空 = 不落盘图片

	mu     sync.Mutex
	filter *FilterEngine
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

// ClipboardFormatSet carries both human-readable names for the local database
// and numeric Win32 IDs for privacy-safe diagnostics. Registered format names
// are controlled by third-party applications and are therefore never copied
// into exported support archives.
type ClipboardFormatSet struct {
	Names []string
	IDs   []uint32
}

// ErrImageLimitExceeded 表示剪贴板图片超过 image.maxBytes 限制。
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

// ProcessClipboardUpdate handles one cancellable capture task. The timestamp,
// source and sequence were bound on the message thread; waits happen only here.
func (r *Recorder) ProcessClipboardUpdate(ctx context.Context, source SourceInfo, sequence uint32, capturedAtMS int64) {
	if capturedAtMS <= 0 {
		capturedAtMS = time.Now().UnixMilli()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.IsPaused() {
		return
	}

	cfg := r.cfgMgr.Get()
	snapshot, formats, attempts, err := readClipboardEventForSequence(ctx, cfg, sequence)
	sourceResolved := false
	resolveSource := func() {
		if !sourceResolved && source.PID != 0 {
			source.resolveDetails()
		}
		sourceResolved = true
	}
	isSuperseded := func() bool {
		return errors.Is(context.Cause(ctx), ErrClipboardSuperseded) || !clipboardSequenceIsCurrent(sequence)
	}
	recordSuperseded := func() {
		// Clipboard access has already ended. Resolve the context captured with
		// the old sequence before persisting the skip event so attribution and
		// fingerprinting do not collapse unrelated applications together.
		resolveSource()
		r.saveCaptureEvent(source, capturedAtMS, sequence, domain.EventClipboardChanged,
			"sequence", attempts, formats, "", nil)
	}
	if errors.Is(err, ErrClipboardSuperseded) || isSuperseded() {
		recordSuperseded()
		return
	}
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return
	}
	if containsString(formats.Names, internalRestoreFormatName) {
		// Internal restore writes are never capture events. Check the marker
		// before resolving source details or handling typed read failures because
		// an oversized restored value can be rejected directly by the reader.
		return
	}
	// Resolve process metadata only after the clipboard has been closed.
	resolveSource()
	if isSuperseded() {
		recordSuperseded()
		return
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		if errors.Is(err, ErrTextLimitExceeded) {
			meta := map[string]any{"limit": cfg.MaxTextChars}
			var limitErr *clipboardTextLimitError
			if errors.As(err, &limitErr) {
				meta["charLen"] = limitErr.CharLen
				meta["limit"] = limitErr.Limit
			}
			var payloadLimitErr *clipboardTextPayloadLimitError
			if errors.As(err, &payloadLimitErr) {
				meta["byteLen"] = payloadLimitErr.ByteLen
				meta["byteLimit"] = payloadLimitErr.Limit
			}
			r.saveCaptureEventWithContext(ctx, source, capturedAtMS, sequence, domain.EventTextLimitExceeded,
				"validate", attempts, formats, string(domain.EventTextLimitExceeded), meta)
			return
		}
		if errors.Is(err, ErrImageLimitExceeded) {
			r.saveCaptureEventWithContext(ctx, source, capturedAtMS, sequence, domain.EventImageLimitExceeded,
				"read", attempts, formats, string(domain.EventImageLimitExceeded), nil)
			return
		}
		fields := clipboardReadFailureFields(err, source, formats)
		r.logger.Error("clipboard_read_failed", err, fields)
		detail := clipboardFailureDetailFor(err)
		code := classifyCaptureError(err)
		r.saveCaptureEventWithContext(ctx, source, capturedAtMS, sequence, code, detail.Stage, attempts,
			formats, string(code), nil)
		return
	}
	if snapshot == nil {
		code := classifyEmptyCapture(formats)
		r.saveCaptureEventWithContext(ctx, source, capturedAtMS, sequence, code, "classify", attempts,
			formats, "", nil)
		return
	}
	// 来源信息是增强元数据：解析失败静默降级，绝不影响内容落库。
	snapshot.Source = source

	// 图片：PNG 字节写入托管目录，入库记录引用托管文件路径。
	managedImageFullPath := ""
	contentPersisted := false
	defer func() {
		if managedImageFullPath != "" && !contentPersisted {
			if cleanupErr := os.Remove(managedImageFullPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				r.logger.Warn("clipboard_image_cleanup_failed", map[string]any{"error": cleanupErr.Error()})
			}
		}
	}()
	if len(snapshot.ImageBytes) > 0 {
		relPath, fullPath, err := r.saveManagedImage(snapshot.ImageBytes, capturedAtMS, snapshot.SHA256)
		if err != nil {
			r.logger.Error("clipboard_image_save_failed", err, nil)
			r.saveCaptureEventWithContext(ctx, source, capturedAtMS, sequence, domain.EventCaptureFailed,
				"persist", attempts, formats, "persist_failed", nil)
			return
		}
		managedImageFullPath = fullPath
		snapshot.Text = filepath.ToSlash(relPath)
		if snapshot.Meta == nil {
			snapshot.Meta = map[string]any{}
		}
		snapshot.Meta["imagePath"] = snapshot.Text
	}

	var sensitive bool
	if snapshot.Text != "" {
		sensitive = r.filter.CheckText(snapshot.Text).Sensitive
	}

	metaJSON := "{}"
	if snapshot.Meta != nil {
		if data, err := json.Marshal(snapshot.Meta); err == nil {
			metaJSON = string(data)
		}
	}
	if isSuperseded() {
		recordSuperseded()
		return
	}
	if ctx.Err() != nil {
		return
	}

	if err := r.store.Save(ctx, ClipboardItem{
		CapturedAtMS:      capturedAtMS,
		RecordType:        domain.HistoryRecordContent,
		Kind:              snapshot.Kind,
		Format:            snapshot.Format,
		Text:              snapshot.Text,
		ByteLen:           snapshot.ByteLen,
		CharLen:           snapshot.CharLen,
		ContentSHA256:     snapshot.SHA256,
		MetaJSON:          metaJSON,
		Sensitive:         sensitive,
		SourceExeName:     snapshot.Source.ExeName,
		SourceExePath:     snapshot.Source.ExePath,
		SourceWindowTitle: snapshot.Source.WindowTitle,
		SourceAttribution: normalizedSourceAttribution(snapshot.Source.Method),
	}); err != nil {
		if isSuperseded() {
			recordSuperseded()
			return
		}
		r.logger.Error("clipboard_save_failed", err, map[string]any{
			"kind":    snapshot.Kind,
			"charLen": snapshot.CharLen,
		})
		return
	}
	contentPersisted = true

	r.logger.Info("clipboard_saved", map[string]any{
		"kind":      snapshot.Kind,
		"format":    snapshot.Format,
		"charLen":   snapshot.CharLen,
		"byteLen":   snapshot.ByteLen,
		"sensitive": sensitive,
	})
}

type diagnosticFieldProvider interface {
	DiagnosticFields() map[string]any
}

func clipboardReadFailureFields(err error, source SourceInfo, formats ClipboardFormatSet) map[string]any {
	fields := map[string]any{
		"sourcePid": source.PID,
		"formats":   formats.Names,
		"formatIds": formats.IDs,
	}
	if source.ExeName != "" {
		fields["sourceExeName"] = source.ExeName
	}
	var provider diagnosticFieldProvider
	if errors.As(err, &provider) {
		for key, value := range provider.DiagnosticFields() {
			fields[key] = value
		}
	}
	return fields
}

type clipboardFailureDetail struct {
	Stage              string `json:"stage,omitempty"`
	Format             string `json:"format,omitempty"`
	Attempt            int    `json:"attempt,omitempty"`
	OpenAttempts       int    `json:"openAttempts,omitempty"`
	ThreadIDBefore     uint32 `json:"threadIdBefore,omitempty"`
	ThreadIDAfter      uint32 `json:"threadIdAfter,omitempty"`
	ExpectedSequence   uint32 `json:"expectedSequence,omitempty"`
	SequenceBeforeOpen uint32 `json:"sequenceBeforeOpen,omitempty"`
	SequenceAfterOpen  uint32 `json:"sequenceAfterOpen,omitempty"`
	SequenceAfterRead  uint32 `json:"sequenceAfterRead,omitempty"`
	ClipboardOwnerPID  uint32 `json:"clipboardOwnerPid,omitempty"`
	ClipboardOpenerPID uint32 `json:"clipboardOpenerPid,omitempty"`
	DurationMS         int64  `json:"durationMs,omitempty"`
	Win32Error         int64  `json:"win32Error,omitempty"`
	CloseWin32Error    int64  `json:"closeWin32Error,omitempty"`
}

type clipboardFailureDetailProvider interface {
	ClipboardFailureDetail() clipboardFailureDetail
}

func clipboardFailureDetailFor(err error) clipboardFailureDetail {
	var provider clipboardFailureDetailProvider
	if errors.As(err, &provider) {
		return provider.ClipboardFailureDetail()
	}
	return clipboardFailureDetail{}
}

func (r *Recorder) saveCaptureEvent(source SourceInfo, capturedAtMS int64, sequence uint32,
	code domain.ClipboardEventCode, stage string, attempts int, formats ClipboardFormatSet,
	errorCategory string, extra map[string]any,
) {
	r.saveCaptureEventWithContext(context.Background(), source, capturedAtMS, sequence, code, stage, attempts, formats, errorCategory, extra)
}

func (r *Recorder) saveCaptureEventWithContext(ctx context.Context, source SourceInfo, capturedAtMS int64, sequence uint32,
	code domain.ClipboardEventCode, stage string, attempts int, formats ClipboardFormatSet,
	errorCategory string, extra map[string]any,
) {
	meta := map[string]any{}
	for k, v := range extra {
		meta[k] = v
	}
	metaJSON := "{}"
	if len(meta) > 0 {
		if data, err := json.Marshal(meta); err == nil {
			metaJSON = string(data)
		}
	}
	attribution := normalizedSourceAttribution(source.Method)
	if err := r.store.Save(ctx, ClipboardItem{
		CapturedAtMS:      capturedAtMS,
		RecordType:        domain.HistoryRecordEvent,
		MetaJSON:          metaJSON,
		EventCode:         code,
		CaptureSequence:   sequence,
		CaptureStage:      stage,
		CaptureAttempts:   attempts,
		EventFormats:      formats.Info(),
		ErrorCategory:     errorCategory,
		SourceExeName:     source.ExeName,
		SourceExePath:     source.ExePath,
		SourceWindowTitle: source.WindowTitle,
		SourceAttribution: attribution,
	}); err != nil {
		if ctx.Err() != nil {
			return
		}
		r.logger.Error("clipboard_event_save_failed", err, map[string]any{"eventCode": code})
		return
	}
	r.logger.Info("clipboard_event_saved", map[string]any{
		"eventCode": code, "stage": stage, "attempts": attempts,
		"formats": formats.Names, "formatIds": formats.IDs,
	})
}

func normalizedSourceAttribution(method string) string {
	switch method {
	case "foreground", "clipboard_owner":
		return method
	default:
		return "unknown"
	}
}

// saveManagedImage 把 PNG 字节写入托管目录（UTC 年/月两级），
// 返回相对 DataDir 的路径（images/2006/01/<unix_ms>_<hash8>_<unique>.png）。
func (r *Recorder) saveManagedImage(pngBytes []byte, capturedAtMS int64, contentHash string) (string, string, error) {
	if r.imagesDir == "" {
		return "", "", errors.New("image directory is not configured")
	}
	t := time.UnixMilli(capturedAtMS).UTC()
	dir := filepath.Join(r.imagesDir, t.Format("2006"), t.Format("01"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("mkdir image dir: %w", err)
	}
	hash8 := contentHash
	if len(hash8) > 8 {
		hash8 = hash8[:8]
	}
	if hash8 == "" {
		hash8 = "00000000"
	}
	prefix := fmt.Sprintf("%d_%s_*.png", capturedAtMS, hash8)
	file, err := os.CreateTemp(dir, prefix)
	if err != nil {
		return "", "", fmt.Errorf("create image file: %w", err)
	}
	full := file.Name()
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(full)
		}
	}()
	if _, err := file.Write(pngBytes); err != nil {
		return "", "", fmt.Errorf("write image file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", "", fmt.Errorf("close image file: %w", err)
	}
	written = true
	name := filepath.Base(full)
	return buildManagedImageStoredPath(t.Format("2006"), t.Format("01"), name), full, nil
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
