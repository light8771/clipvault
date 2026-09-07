//go:build windows

package clipvault

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"clipboard_ext/internal/clipvault/domain"
)

const (
	cfBitmap      = 2
	cfDIB         = 8
	cfUnicodeText = 13
	cfHDrop       = 15
	cfDIBV5       = 17
)

var (
	user32                         = syscall.NewLazyDLL("user32.dll")
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	shell32                        = syscall.NewLazyDLL("shell32.dll")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procGetOpenClipboardWindow     = user32.NewProc("GetOpenClipboardWindow")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procEnumClipboardFormats       = user32.NewProc("EnumClipboardFormats")
	procGetClipboardFormatNameW    = user32.NewProc("GetClipboardFormatNameW")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
	procDragQueryFileW             = shell32.NewProc("DragQueryFileW")
)

var ErrClipboardSuperseded = errors.New("clipboard event was superseded")

const (
	clipboardOpenAttempts       = 8
	errorClipboardNotOpen       = syscall.Errno(1418)
	clipboardFormatUnknown      = "unknown"
	clipboardReadStageOpen      = "open"
	clipboardReadStageEnumerate = "enumerate"
	clipboardReadStageGetData   = "get_data"
	clipboardReadStageLockData  = "lock_data"
	clipboardReadStageDecode    = "decode"
	clipboardReadStageClose     = "close"
	maxClipboardFileDropPaths   = 1000
	maxFileDropPathUTF16Units   = 32767
	maxClipboardFormatNameChars = 255
)

var errClipboardDataUnavailable = errors.New("clipboard format is advertised but data is unavailable")

// clipboardOperationError records the exact Win32/data stage that failed.
// It stays internal; clipboardReadError below is the single error contract
// returned to Recorder and exported as privacy-safe structured diagnostics.
type clipboardOperationError struct {
	stage  string
	format string
	err    error
}

func (e *clipboardOperationError) Error() string {
	return fmt.Sprintf("%s(%s): %v", e.stage, e.format, e.err)
}

func (e *clipboardOperationError) Unwrap() error { return e.err }

type clipboardReadError struct {
	stage              string
	format             string
	err                error
	attempt            int
	openAttempts       int
	threadIDBefore     uint32
	threadIDAfter      uint32
	expectedSequence   uint32
	sequenceBeforeOpen uint32
	sequenceAfterOpen  uint32
	sequenceAfterRead  uint32
	clipboardOwnerPid  uint32
	clipboardOpenerPid uint32
	duration           time.Duration
	closeErr           error
}

func (e *clipboardReadError) Error() string {
	return fmt.Sprintf("clipboard %s failed for %s: %v", e.stage, e.format, e.err)
}

func (e *clipboardReadError) Unwrap() error { return e.err }

func (e *clipboardReadError) Is(target error) bool {
	return errors.Is(e.err, target) || errors.Is(e.closeErr, target)
}

func (e *clipboardReadError) ClipboardErrorCategory() string {
	if e.stage == clipboardReadStageOpen {
		return string(domain.EventClipboardBusy)
	}
	if errors.Is(e.err, errClipboardDataUnavailable) {
		return string(domain.EventFormatDataUnavailable)
	}
	if e.stage == clipboardReadStageDecode {
		return string(domain.EventDecodeFailed)
	}
	return string(domain.EventCaptureFailed)
}

func (e *clipboardReadError) ClipboardFailureDetail() clipboardFailureDetail {
	detail := clipboardFailureDetail{
		Stage:              e.stage,
		Format:             e.format,
		Attempt:            e.attempt,
		OpenAttempts:       e.openAttempts,
		ThreadIDBefore:     e.threadIDBefore,
		ThreadIDAfter:      e.threadIDAfter,
		ExpectedSequence:   e.expectedSequence,
		SequenceBeforeOpen: e.sequenceBeforeOpen,
		SequenceAfterOpen:  e.sequenceAfterOpen,
		SequenceAfterRead:  e.sequenceAfterRead,
		ClipboardOwnerPID:  e.clipboardOwnerPid,
		ClipboardOpenerPID: e.clipboardOpenerPid,
		DurationMS:         e.duration.Milliseconds(),
		Win32Error:         win32ErrorCode(e.err),
	}
	if e.closeErr != nil {
		detail.CloseWin32Error = win32ErrorCode(e.closeErr)
	}
	return detail
}

// DiagnosticFields returns only bounded operational values. Recorder writes
// these to the local log; diagnostics.go independently allowlists the subset
// that is safe for support archives.
func (e *clipboardReadError) DiagnosticFields() map[string]any {
	detail := e.ClipboardFailureDetail()
	fields := map[string]any{
		"stage":              detail.Stage,
		"format":             detail.Format,
		"attempt":            detail.Attempt,
		"openAttempts":       detail.OpenAttempts,
		"threadIdBefore":     detail.ThreadIDBefore,
		"threadIdAfter":      detail.ThreadIDAfter,
		"expectedSequence":   detail.ExpectedSequence,
		"sequenceBeforeOpen": detail.SequenceBeforeOpen,
		"sequenceAfterOpen":  detail.SequenceAfterOpen,
		"sequenceAfterRead":  detail.SequenceAfterRead,
		"clipboardOwnerPid":  detail.ClipboardOwnerPID,
		"clipboardOpenerPid": detail.ClipboardOpenerPID,
		"durationMs":         detail.DurationMS,
		"win32Error":         detail.Win32Error,
	}
	if detail.CloseWin32Error != 0 {
		fields["closeWin32Error"] = detail.CloseWin32Error
	}
	return fields
}

func win32ErrorCode(err error) int64 {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return int64(errno)
	}
	return 0
}

func currentClipboardThreadID() uint32 {
	tid, _, _ := procGetCurrentThreadId.Call()
	return uint32(tid)
}

func clipboardWindowProcessID(hwnd uintptr) uint32 {
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

func currentClipboardOwnerPID() uint32 {
	hwnd, _, _ := procGetClipboardOwner.Call()
	return clipboardWindowProcessID(hwnd)
}

func currentClipboardOpenerPID() uint32 {
	hwnd, _, _ := procGetOpenClipboardWindow.Call()
	return clipboardWindowProcessID(hwnd)
}

type clipboardReadAttemptContext struct {
	attempt                  int
	openAttempts             int
	threadIDBefore           uint32
	expectedSequence         uint32
	sequenceBeforeOpen       uint32
	sequenceAfterOpen        uint32
	sequenceAfterRead        uint32
	clipboardOwnerPID        uint32
	clipboardOpenerPID       uint32
	clipboardWindowsCaptured bool
	started                  time.Time
}

func newClipboardReadAttemptContext(attempt int, expectedSequence uint32) *clipboardReadAttemptContext {
	return &clipboardReadAttemptContext{
		attempt:            attempt,
		threadIDBefore:     currentClipboardThreadID(),
		expectedSequence:   expectedSequence,
		sequenceBeforeOpen: clipboardSequenceNumber(),
		started:            time.Now(),
	}
}

func (c *clipboardReadAttemptContext) captureClipboardWindows() {
	c.clipboardOwnerPID = currentClipboardOwnerPID()
	c.clipboardOpenerPID = currentClipboardOpenerPID()
	c.clipboardWindowsCaptured = true
}

func (c *clipboardReadAttemptContext) failure(stage, format string, err, closeErr error) error {
	if c.sequenceAfterRead == 0 {
		c.sequenceAfterRead = clipboardSequenceNumber()
	}
	if !c.clipboardWindowsCaptured {
		c.captureClipboardWindows()
	}
	return &clipboardReadError{
		stage:              stage,
		format:             format,
		err:                err,
		attempt:            c.attempt,
		openAttempts:       c.openAttempts,
		threadIDBefore:     c.threadIDBefore,
		threadIDAfter:      currentClipboardThreadID(),
		expectedSequence:   c.expectedSequence,
		sequenceBeforeOpen: c.sequenceBeforeOpen,
		sequenceAfterOpen:  c.sequenceAfterOpen,
		sequenceAfterRead:  c.sequenceAfterRead,
		clipboardOwnerPid:  c.clipboardOwnerPID,
		clipboardOpenerPid: c.clipboardOpenerPID,
		duration:           time.Since(c.started),
		closeErr:           closeErr,
	}
}

func clipboardSequenceNumber() uint32 {
	seq, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(seq)
}

func clipboardSequenceSuperseded(expected, observed uint32) bool {
	return expected != 0 && observed != expected
}

func clipboardSequenceIsCurrent(expected uint32) bool {
	return !clipboardSequenceSuperseded(expected, clipboardSequenceNumber())
}

// readClipboardEventForSequence 读取一次剪贴板事件。
//
// 返回值语义：
//   - snapshot != nil：读到了可入库的内容（文本/文件/图片）。
//   - snapshot == nil 且 err == nil：未读到可保存内容。formats 为空表示
//     剪贴板已清空；非空表示只有不支持的格式。调用方应分类落 event-only 记录。
//   - err == ErrClipboardSuperseded：等待/读取期间序列已变化，事件被覆盖。
//
// Win32 剪贴板所有权是按线程记账的：OpenClipboard / CloseClipboard 必须在
// 同一线程上调用。Go 调度器可能在任意 syscall 返回点把 goroutine 迁移到别的
// OS 线程，因此这里锁定当前线程，避免 CloseClipboard 落到错误的线程上导致
// 剪贴板被永久占用（全系统粘贴失效）。
func readClipboardEventForSequence(ctx context.Context, cfg Config, expectedSequence uint32) (*ClipboardSnapshot, ClipboardFormatSet, int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var lastFormats ClipboardFormatSet
	started := time.Now()
	for attempt, scheduledAt := range clipboardCaptureSchedule {
		attempt++
		if wait := scheduledAt - time.Since(started); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return nil, lastFormats, attempt - 1, context.Cause(ctx)
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, lastFormats, attempt - 1, context.Cause(ctx)
		}
		snapshot, formats, err := readClipboardAttempt(cfg, expectedSequence, attempt)
		lastFormats = formats
		if errors.Is(err, ErrClipboardSuperseded) {
			return nil, formats, attempt, err
		}
		if err == nil {
			if snapshot != nil || len(formats.IDs) > 0 || len(formats.Names) > 0 || attempt >= 2 {
				return snapshot, formats, attempt, nil
			}
			// An empty clipboard gets one short confirmation only.
			continue
		}
		if !shouldRetryClipboardCapture(err, attempt) {
			return nil, formats, attempt, err
		}
		observed := clipboardSequenceNumber()
		if clipboardSequenceSuperseded(expectedSequence, observed) {
			return nil, formats, attempt, ErrClipboardSuperseded
		}
	}
	return nil, lastFormats, len(clipboardCaptureSchedule), errors.New("clipboard read attempts exhausted")
}

func readClipboardAttempt(cfg Config, expectedSequence uint32, attempt int) (*ClipboardSnapshot, ClipboardFormatSet, error) {
	ctx := newClipboardReadAttemptContext(attempt, expectedSequence)
	if clipboardSequenceSuperseded(expectedSequence, ctx.sequenceBeforeOpen) {
		return nil, ClipboardFormatSet{}, ErrClipboardSuperseded
	}

	// The scheduler owns retry timing. A capture attempt opens exactly once so
	// cancellation and the 600ms upper bound remain meaningful.
	openAttempts, err := openClipboardWithRetry(0, 1)
	ctx.openAttempts = openAttempts
	if err != nil {
		if clipboardSequenceSuperseded(expectedSequence, clipboardSequenceNumber()) {
			return nil, ClipboardFormatSet{}, ErrClipboardSuperseded
		}
		return nil, ClipboardFormatSet{}, ctx.failure(clipboardReadStageOpen, clipboardFormatUnknown, err, nil)
	}
	closed := false
	defer func() {
		// Last-resort release for every return path and future decoder changes.
		// The explicit closes below still report failures through diagnostics.
		if !closed {
			_ = closeClipboard()
		}
	}()

	ctx.sequenceAfterOpen = clipboardSequenceNumber()
	if clipboardSequenceSuperseded(expectedSequence, ctx.sequenceAfterOpen) {
		ctx.captureClipboardWindows()
		closeErr := closeClipboard()
		closed = closeErr == nil
		if closeErr != nil {
			return nil, ClipboardFormatSet{}, ctx.failure(clipboardReadStageClose, clipboardFormatUnknown, closeErr, nil)
		}
		return nil, ClipboardFormatSet{}, ErrClipboardSuperseded
	}

	formats, readErr := enumClipboardFormatNames()
	var captured openedClipboardAccess
	if readErr == nil {
		captured = captureOpenedClipboardAccess(cfg, formats)
	}
	ctx.sequenceAfterRead = clipboardSequenceNumber()
	ctx.captureClipboardWindows()
	closeErr := closeClipboard()
	closed = closeErr == nil

	// A producer may replace the clipboard while a delayed-rendering format is
	// being materialized. Even a successful decoder result belongs to the old
	// sequence and must never be persisted as the current clipboard content.
	if clipboardSequenceSuperseded(expectedSequence, ctx.sequenceAfterRead) {
		if closeErr != nil {
			return nil, formats, ctx.failure(clipboardReadStageClose, clipboardFormatUnknown, closeErr, nil)
		}
		return nil, formats, ErrClipboardSuperseded
	}
	if readErr == nil && closeErr == nil {
		// All HGLOBAL/GDI data needed by the resolver has been copied. Parsing,
		// validation and PNG encoding now happen without owning the clipboard.
		var snapshot *ClipboardSnapshot
		snapshot, readErr = readOpenedClipboardWith(cfg, captured)
		ctx.sequenceAfterRead = clipboardSequenceNumber()
		if clipboardSequenceSuperseded(expectedSequence, ctx.sequenceAfterRead) {
			return nil, formats, ErrClipboardSuperseded
		}
		if readErr == nil {
			return snapshot, formats, nil
		}
	}
	if readErr != nil {
		stage, format := clipboardReadStageDecode, clipboardFormatUnknown
		var operationErr *clipboardOperationError
		if errors.As(readErr, &operationErr) {
			stage, format = operationErr.stage, operationErr.format
		}
		return nil, formats, ctx.failure(stage, format, readErr, closeErr)
	}
	if closeErr != nil {
		return nil, formats, ctx.failure(clipboardReadStageClose, clipboardFormatUnknown, closeErr, nil)
	}
	return nil, formats, nil
}

type openedClipboardAccess struct {
	available        func(uint32) bool
	readUnicodeText  func(Config) (*ClipboardSnapshot, error)
	readTextFallback func() (*ClipboardSnapshot, bool, error)
	readImage        func(Config) (*ClipboardSnapshot, bool, error)
	readFileDrop     func(Config) (*ClipboardSnapshot, error)
	readURL          func() (*ClipboardSnapshot, bool, error)
}

// readOpenedClipboardWith treats every advertised representation as a
// candidate. Delayed-rendering producers can enumerate a format but fail to
// provide its handle; a broken higher-priority candidate must not hide a
// usable lower-priority representation of the same clipboard content.
func readOpenedClipboardWith(cfg Config, access openedClipboardAccess) (*ClipboardSnapshot, error) {
	var failures clipboardCandidateErrors
	// A physical file drop is the canonical representation even when Windows
	// also advertises text, a bitmap, or a thumbnail for the same files.
	if access.available(cfHDrop) {
		snapshot, err := access.readFileDrop(cfg)
		if err != nil {
			failures.add(err)
		} else if snapshot != nil {
			return snapshot, nil
		}
	}
	if access.available(cfUnicodeText) {
		snapshot, err := access.readUnicodeText(cfg)
		if err != nil {
			if errors.Is(err, ErrTextLimitExceeded) {
				return nil, err
			}
			failures.add(err)
		} else if snapshot != nil {
			return validateCapturedTextLimit(cfg, snapshot)
		}
	}
	if access.readTextFallback != nil {
		snapshot, ok, err := access.readTextFallback()
		if err != nil {
			failures.add(err)
		} else if ok && snapshot != nil {
			return validateCapturedTextLimit(cfg, snapshot)
		}
	}
	if cfg.Image.Enabled {
		snapshot, ok, err := access.readImage(cfg)
		if errors.Is(err, ErrImageLimitExceeded) {
			return nil, err
		}
		if err != nil {
			failures.add(err)
		} else if ok && snapshot != nil {
			return snapshot, nil
		}
	}
	if snapshot, ok, err := access.readURL(); err != nil {
		failures.add(err)
	} else if ok && snapshot != nil {
		return validateCapturedTextLimit(cfg, snapshot)
	}
	return nil, failures.err()
}

// enumClipboardFormatNames 枚举当前剪贴板的全部格式并返回可读名称。
// 剪贴板必须已由调用方打开。标准格式返回常量名，注册格式（>=0xC000）
// 返回 RegisterClipboardFormat 注册名，其余返回十六进制 id。
func enumClipboardFormatNames() (ClipboardFormatSet, error) {
	formats := ClipboardFormatSet{}
	format := uintptr(0)
	for {
		next, _, err := procEnumClipboardFormats.Call(format)
		if next == 0 {
			if err != syscall.Errno(0) {
				return formats, &clipboardOperationError{stage: clipboardReadStageEnumerate, format: clipboardFormatUnknown, err: err}
			}
			return formats, nil
		}
		format = next
		formats.IDs = append(formats.IDs, uint32(format))
		formats.Names = append(formats.Names, clipboardFormatName(uint32(format)))
	}
}

func clipboardFormatName(format uint32) string {
	switch format {
	case cfUnicodeText:
		return "CF_UNICODETEXT"
	case cfText:
		return "CF_TEXT"
	case cfLocale:
		return "CF_LOCALE"
	case cfHDrop:
		return "CF_HDROP"
	case cfDIB:
		return "CF_DIB"
	case cfDIBV5:
		return "CF_DIBV5"
	case cfBitmap:
		return "CF_BITMAP"
	}
	if format >= 0xC000 {
		// RegisterClipboardFormat accepts names up to 255 characters. Reserve
		// one additional UTF-16 unit so event metadata and fingerprints never
		// silently collapse distinct long registered-format names.
		buf := make([]uint16, maxClipboardFormatNameChars+1)
		n, _, _ := procGetClipboardFormatNameW.Call(uintptr(format), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n > 0 {
			return syscall.UTF16ToString(buf[:n])
		}
	}
	return fmt.Sprintf("CF_%04X", format)
}

func registerClipboardFormat(name string) uint32 {
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	id, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(ptr)))
	return uint32(id)
}

// openClipboardWithRetry 以指数退避重试打开剪贴板，最多 attempts 次。
func openClipboardWithRetry(owner uintptr, attempts int) (int, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		ok, _, err := procOpenClipboard.Call(owner)
		if ok != 0 {
			return i + 1, nil
		}
		if err != syscall.Errno(0) {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("OpenClipboard failed")
		}
		if i < attempts-1 {
			time.Sleep(time.Duration(10*(1<<i)) * time.Millisecond)
		}
	}
	return attempts, lastErr
}

func closeClipboard() error {
	ok, _, err := procCloseClipboard.Call()
	if ok != 0 {
		return nil
	}
	if err != syscall.Errno(0) {
		return err
	}
	return errors.New("CloseClipboard failed")
}

func newUnicodeTextClipboardSnapshot(cfg Config, raw []uint16) (*ClipboardSnapshot, error) {
	charLen, end, terminated := inspectClipboardUTF16(raw)
	if !terminated {
		return nil, &clipboardOperationError{
			stage: clipboardReadStageDecode, format: "CF_UNICODETEXT",
			err: errors.New("unicode clipboard payload is not NUL-terminated"),
		}
	}
	if charLen == 0 {
		return nil, &clipboardOperationError{
			stage: clipboardReadStageDecode, format: "CF_UNICODETEXT",
			err: errors.New("unicode clipboard payload is empty"),
		}
	}
	if cfg.MaxTextChars > 0 && charLen > cfg.MaxTextChars {
		return nil, &clipboardOperationError{
			stage:  clipboardReadStageDecode,
			format: "CF_UNICODETEXT",
			err:    &clipboardTextLimitError{CharLen: charLen, Limit: cfg.MaxTextChars},
		}
	}
	text := string(utf16.Decode(raw[:end]))
	byteLen := len([]byte(text))
	return &ClipboardSnapshot{
		Kind:    domain.KindText,
		Format:  domain.FormatUnicodeText,
		Text:    text,
		ByteLen: byteLen,
		CharLen: charLen,
		SHA256:  hashString(text),
		Meta: map[string]any{
			"formatId": cfUnicodeText,
		},
	}, nil
}

func captureFileDropClipboardPaths(cfg Config) ([]string, int, error) {
	handle, _, err := procGetClipboardData.Call(cfHDrop)
	if handle == 0 {
		if err != syscall.Errno(0) {
			return nil, 0, &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_HDROP", err: err}
		}
		return nil, 0, &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_HDROP", err: errClipboardDataUnavailable}
	}

	count, _, _ := procDragQueryFileW.Call(handle, 0xFFFFFFFF, 0, 0)
	if count == 0 {
		return nil, 0, nil
	}

	limit := clipboardFileDropLimit(uint32(count), cfg.MaxFileDropPaths)
	paths := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		path, err := readFileDropPath(uint32(i), func(index uint32, dst []uint16) (uint32, error) {
			var ptr uintptr
			if len(dst) > 0 {
				ptr = uintptr(unsafe.Pointer(&dst[0]))
			}
			n, _, callErr := procDragQueryFileW.Call(handle, uintptr(index), ptr, uintptr(len(dst)))
			if callErr == syscall.Errno(0) {
				callErr = nil
			}
			return uint32(n), callErr
		})
		if err != nil {
			return nil, 0, &clipboardOperationError{stage: clipboardReadStageDecode, format: "CF_HDROP", err: err}
		}
		paths = append(paths, path)
	}
	return paths, int(count), nil
}

func clipboardFileDropLimit(total uint32, configured int) int {
	limit := min(int(total), maxClipboardFileDropPaths)
	if configured > 0 {
		limit = min(limit, configured)
	}
	return limit
}

type dragQueryFileFunc func(index uint32, dst []uint16) (uint32, error)

func readFileDropPath(index uint32, query dragQueryFileFunc) (string, error) {
	length, err := query(index, nil)
	if length == 0 {
		if err != nil {
			return "", fmt.Errorf("query dropped file %d length: %w", index, err)
		}
		return "", fmt.Errorf("dropped file %d has an empty path", index)
	}
	if length > maxFileDropPathUTF16Units {
		return "", fmt.Errorf("dropped file %d path is too long", index)
	}
	buf := make([]uint16, int(length)+1)
	copied, err := query(index, buf)
	if copied == 0 {
		if err != nil {
			return "", fmt.Errorf("read dropped file %d path: %w", index, err)
		}
		return "", fmt.Errorf("read dropped file %d path failed", index)
	}
	if copied != length || copied >= uint32(len(buf)) {
		return "", fmt.Errorf("dropped file %d path length changed from %d to %d", index, length, copied)
	}
	return syscall.UTF16ToString(buf[:copied]), nil
}

func newFileDropSnapshot(paths []string, totalPaths int) (*ClipboardSnapshot, error) {
	if len(paths) == 0 {
		return nil, &clipboardOperationError{
			stage: clipboardReadStageDecode, format: "CF_HDROP",
			err: errors.New("file drop did not contain any readable paths"),
		}
	}
	text := strings.Join(paths, "\n")
	meta := map[string]any{
		"formatId":    cfHDrop,
		"totalPaths":  totalPaths,
		"storedPaths": len(paths),
		"truncated":   totalPaths > len(paths),
	}
	return &ClipboardSnapshot{
		Kind:    classifyFileDropKind(paths),
		Format:  "CF_HDROP",
		Text:    text,
		ByteLen: len([]byte(text)),
		CharLen: textRuneLen(text),
		SHA256:  hashString(text),
		Meta:    meta,
	}, nil
}
