//go:build windows

package clipvault

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
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
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
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
	clipboardReadAttempts       = 2
	clipboardReadRetryDelay     = 20 * time.Millisecond
	errorClipboardNotOpen       = syscall.Errno(1418)
	clipboardFormatUnknown      = "unknown"
	clipboardReadStageOpen      = "open"
	clipboardReadStageEnumerate = "enumerate"
	clipboardReadStageGetData   = "get_data"
	clipboardReadStageLockData  = "lock_data"
	clipboardReadStageDecode    = "decode"
	clipboardReadStageClose     = "close"
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
func readClipboardEventForSequence(cfg Config, expectedSequence uint32) (*ClipboardSnapshot, ClipboardFormatSet, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var lastFormats ClipboardFormatSet
	for attempt := 1; attempt <= clipboardReadAttempts; attempt++ {
		snapshot, formats, err := readClipboardAttempt(cfg, expectedSequence, attempt)
		lastFormats = formats
		if err == nil || errors.Is(err, ErrClipboardSuperseded) {
			return snapshot, formats, err
		}
		if !isRetryableClipboardReadError(err) || attempt == clipboardReadAttempts {
			return nil, formats, err
		}
		observed := clipboardSequenceNumber()
		if expectedSequence != 0 && observed != expectedSequence {
			return nil, formats, ErrClipboardSuperseded
		}
		time.Sleep(clipboardReadRetryDelay)
	}
	return nil, lastFormats, errors.New("clipboard read attempts exhausted")
}

func isRetryableClipboardReadError(err error) bool {
	return errors.Is(err, errorClipboardNotOpen) || errors.Is(err, errClipboardDataUnavailable)
}

func readClipboardAttempt(cfg Config, expectedSequence uint32, attempt int) (*ClipboardSnapshot, ClipboardFormatSet, error) {
	ctx := newClipboardReadAttemptContext(attempt, expectedSequence)
	if expectedSequence != 0 && ctx.sequenceBeforeOpen != expectedSequence {
		return nil, ClipboardFormatSet{}, ErrClipboardSuperseded
	}

	openAttempts, err := openClipboardWithRetry(clipboardOpenAttempts)
	ctx.openAttempts = openAttempts
	if err != nil {
		if expectedSequence != 0 && clipboardSequenceNumber() != expectedSequence {
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
	if expectedSequence != 0 && ctx.sequenceAfterOpen != expectedSequence {
		ctx.captureClipboardWindows()
		closeErr := closeClipboard()
		closed = closeErr == nil
		if closeErr != nil {
			return nil, ClipboardFormatSet{}, ctx.failure(clipboardReadStageClose, clipboardFormatUnknown, closeErr, nil)
		}
		return nil, ClipboardFormatSet{}, ErrClipboardSuperseded
	}

	formats, readErr := enumClipboardFormatNames()
	var snapshot *ClipboardSnapshot
	if readErr == nil {
		snapshot, readErr = readOpenedClipboard(cfg)
	}
	ctx.sequenceAfterRead = clipboardSequenceNumber()
	ctx.captureClipboardWindows()
	closeErr := closeClipboard()
	closed = closeErr == nil

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
	return snapshot, formats, nil
}

type openedClipboardAccess struct {
	available       func(uint32) bool
	readUnicodeText func() (*ClipboardSnapshot, error)
	readImage       func(Config) (*ClipboardSnapshot, bool, error)
	readFileDrop    func(Config) (*ClipboardSnapshot, error)
	readURL         func() (*ClipboardSnapshot, bool, error)
}

func readOpenedClipboard(cfg Config) (*ClipboardSnapshot, error) {
	return readOpenedClipboardWith(cfg, openedClipboardAccess{
		available:       isClipboardFormatAvailable,
		readUnicodeText: readUnicodeTextClipboard,
		readImage:       readClipboardImage,
		readFileDrop:    readFileDropClipboard,
		readURL:         readClipboardURL,
	})
}

// readOpenedClipboardWith treats every advertised representation as a
// candidate. Delayed-rendering producers can enumerate a format but fail to
// provide its handle; a broken higher-priority candidate must not hide a
// usable lower-priority representation of the same clipboard content.
func readOpenedClipboardWith(cfg Config, access openedClipboardAccess) (*ClipboardSnapshot, error) {
	var lastErr error
	if access.available(cfUnicodeText) {
		snapshot, err := access.readUnicodeText()
		if err != nil {
			lastErr = err
		} else if snapshot != nil {
			return snapshot, nil
		}
	}
	if cfg.Image.Enabled {
		snapshot, ok, err := access.readImage(cfg)
		if errors.Is(err, ErrImageLimitExceeded) {
			return nil, err
		}
		if err != nil {
			lastErr = err
		} else if ok && snapshot != nil {
			return snapshot, nil
		}
	}
	if access.available(cfHDrop) {
		snapshot, err := access.readFileDrop(cfg)
		if err != nil {
			lastErr = err
		} else if snapshot != nil {
			return snapshot, nil
		}
	}
	if snapshot, ok, err := access.readURL(); err != nil {
		lastErr = err
	} else if ok && snapshot != nil {
		return snapshot, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
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
		buf := make([]uint16, 128)
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
func openClipboardWithRetry(attempts int) (int, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		ok, _, err := procOpenClipboard.Call(0)
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

func isClipboardFormatAvailable(format uint32) bool {
	ok, _, _ := procIsClipboardFormatAvailable.Call(uintptr(format))
	return ok != 0
}

// readClipboardBytes 拷贝指定格式的剪贴板数据。剪贴板必须已由调用方打开。
func readClipboardBytes(format uint32) ([]byte, error) {
	handle, _, err := procGetClipboardData.Call(uintptr(format))
	if handle == 0 {
		if err != syscall.Errno(0) {
			return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: clipboardFormatName(format), err: err}
		}
		return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: clipboardFormatName(format), err: errClipboardDataUnavailable}
	}
	ptr, _, err := procGlobalLock.Call(handle)
	if ptr == 0 {
		if err != syscall.Errno(0) {
			return nil, &clipboardOperationError{stage: clipboardReadStageLockData, format: clipboardFormatName(format), err: err}
		}
		return nil, &clipboardOperationError{stage: clipboardReadStageLockData, format: clipboardFormatName(format), err: errors.New("GlobalLock failed")}
	}
	defer procGlobalUnlock.Call(handle)
	size, _, _ := procGlobalSize.Call(handle)
	if size == 0 {
		return nil, nil
	}
	raw := unsafe.Slice((*byte)(pointerFromWin32(ptr)), int(size))
	return append([]byte(nil), raw...), nil
}

func readUnicodeTextClipboard() (*ClipboardSnapshot, error) {
	handle, _, err := procGetClipboardData.Call(cfUnicodeText)
	if handle == 0 {
		if err != syscall.Errno(0) {
			return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_UNICODETEXT", err: err}
		}
		return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_UNICODETEXT", err: errClipboardDataUnavailable}
	}
	ptr, _, err := procGlobalLock.Call(handle)
	if ptr == 0 {
		if err != syscall.Errno(0) {
			return nil, &clipboardOperationError{stage: clipboardReadStageLockData, format: "CF_UNICODETEXT", err: err}
		}
		return nil, &clipboardOperationError{stage: clipboardReadStageLockData, format: "CF_UNICODETEXT", err: errors.New("GlobalLock failed")}
	}
	defer procGlobalUnlock.Call(handle)

	size, _, _ := procGlobalSize.Call(handle)
	u16len := int(size / 2)
	raw := unsafe.Slice((*uint16)(pointerFromWin32(ptr)), u16len)
	text := utf16PtrToString(raw)
	byteLen := len([]byte(text))
	return &ClipboardSnapshot{
		Kind:    "text",
		Format:  "CF_UNICODETEXT",
		Text:    text,
		ByteLen: byteLen,
		CharLen: textRuneLen(text),
		SHA256:  hashString(text),
		Meta: map[string]any{
			"formatId": cfUnicodeText,
		},
	}, nil
}

// readFileDropClipboard 读取文件拖放事件的路径清单，以换行符分隔存储。
func readFileDropClipboard(cfg Config) (*ClipboardSnapshot, error) {
	handle, _, err := procGetClipboardData.Call(cfHDrop)
	if handle == 0 {
		if err != syscall.Errno(0) {
			return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_HDROP", err: err}
		}
		return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_HDROP", err: errClipboardDataUnavailable}
	}

	count, _, _ := procDragQueryFileW.Call(handle, 0xFFFFFFFF, 0, 0)
	if count == 0 {
		return nil, nil
	}

	limit := int(count)
	if cfg.MaxFileDropPaths > 0 && limit > cfg.MaxFileDropPaths {
		limit = cfg.MaxFileDropPaths
	}
	paths := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		n, _, _ := procDragQueryFileW.Call(handle, uintptr(i), 0, 0)
		if n == 0 {
			continue
		}
		buf := make([]uint16, int(n)+1)
		procDragQueryFileW.Call(handle, uintptr(i), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		paths = append(paths, syscall.UTF16ToString(buf))
	}
	if len(paths) == 0 {
		return nil, nil
	}

	text := strings.Join(paths, "\n")
	meta := map[string]any{
		"formatId":    cfHDrop,
		"totalPaths":  int(count),
		"storedPaths": len(paths),
		"truncated":   int(count) > len(paths),
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

// classifyFileDropKind 根据文件扩展名决定内容类型标签。
func classifyFileDropKind(paths []string) string {
	counts := make(map[string]int, len(paths))
	for _, p := range paths {
		counts[fileExtKind(p)]++
	}
	if len(counts) == 1 {
		for k := range counts {
			return k
		}
	}
	best, max := "files", 0
	for k, n := range counts {
		if n > max {
			best, max = k, n
		}
	}
	return best
}

// fileExtKind 将文件扩展名映射到内容类型标签。
func fileExtKind(path string) string {
	dot := strings.LastIndex(path, ".")
	if dot < 0 {
		return "files"
	}
	ext := strings.ToLower(path[dot:])
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp",
		".svg", ".ico", ".tiff", ".tif", ".heic", ".heif",
		".raw", ".cr2", ".nef", ".psd", ".ai", ".avif":
		return "image"
	case ".mp4", ".avi", ".mov", ".mkv", ".wmv", ".flv",
		".webm", ".m4v", ".mpeg", ".mpg", ".3gp", ".ts",
		".rmvb", ".rm", ".asf", ".f4v", ".vob":
		return "video"
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2",
		".xz", ".iso", ".cab", ".lzh", ".z", ".tgz",
		".tbz2", ".ace", ".arj", ".zst", ".lz4":
		return "archive"
	case ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".odt", ".ods", ".odp", ".pdf", ".wps", ".et",
		".dps", ".csv", ".rtf", ".pages", ".numbers", ".key":
		return "office"
	default:
		return "files"
	}
}

func utf16PtrToString(raw []uint16) string {
	for i, value := range raw {
		if value == 0 {
			raw = raw[:i]
			break
		}
	}
	return string(utf16.Decode(raw))
}
