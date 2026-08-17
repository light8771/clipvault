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

func clipboardSequenceNumber() uint32 {
	seq, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(seq)
}

// ReadClipboardSnapshot 读取当前剪贴板内容（诊断/调试入口）。
// 录制链路使用 readClipboardEventForSequence。
func ReadClipboardSnapshot(cfg Config) (*ClipboardSnapshot, error) {
	snapshot, _, err := readClipboardEventForSequence(cfg, 0)
	return snapshot, err
}

// readClipboardEventForSequence 读取一次剪贴板事件。
//
// 返回值语义（事件优先）：
//   - snapshot != nil：读到了可入库的内容（文本/文件/图片）。
//   - snapshot == nil 且 err == nil：剪贴板只有不支持的格式，formats 为
//     打开期间枚举到的全部格式名，调用方应落 event-only 记录。
//   - err == ErrClipboardSuperseded：等待/读取期间序列已变化，事件被覆盖。
//
// Win32 剪贴板所有权是按线程记账的：OpenClipboard / CloseClipboard 必须在
// 同一线程上调用。Go 调度器可能在任意 syscall 返回点把 goroutine 迁移到别的
// OS 线程，因此这里锁定当前线程，避免 CloseClipboard 落到错误的线程上导致
// 剪贴板被永久占用（全系统粘贴失效）。
func readClipboardEventForSequence(cfg Config, expectedSequence uint32) (*ClipboardSnapshot, []string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if expectedSequence != 0 && clipboardSequenceNumber() != expectedSequence {
		return nil, nil, ErrClipboardSuperseded
	}

	// 剪贴板常被 Office/浏览器等短暂占用，OpenClipboard 立即失败是常态，
	// 用指数退避重试几次再放弃，避免瞬时占用导致这条记录永久丢失。
	// 8 次退避总窗口约 1.3s（10ms 起倍增），覆盖 Electron 等长占用场景；
	// 重试发生在 worker goroutine 而非消息泵线程，拉长不会卡住 UI。
	if err := openClipboardWithRetry(8); err != nil {
		return nil, nil, fmt.Errorf("OpenClipboard: %w", err)
	}
	defer procCloseClipboard.Call()
	if expectedSequence != 0 && clipboardSequenceNumber() != expectedSequence {
		return nil, nil, ErrClipboardSuperseded
	}

	formats := enumClipboardFormatNames()

	if isClipboardFormatAvailable(cfUnicodeText) {
		snapshot, err := readUnicodeTextClipboard()
		return snapshot, formats, err
	}
	if cfg.Image.Enabled {
		if snapshot, ok, err := readClipboardImage(cfg); err != nil {
			return nil, formats, err
		} else if ok {
			return snapshot, formats, nil
		}
	}
	if isClipboardFormatAvailable(cfHDrop) {
		snapshot, err := readFileDropClipboard(cfg)
		return snapshot, formats, err
	}
	return nil, formats, nil
}

// enumClipboardFormatNames 枚举当前剪贴板的全部格式并返回可读名称。
// 剪贴板必须已由调用方打开。标准格式返回常量名，注册格式（>=0xC000）
// 返回 RegisterClipboardFormat 注册名，其余返回十六进制 id。
func enumClipboardFormatNames() []string {
	var names []string
	format := uintptr(0)
	for {
		next, _, _ := procEnumClipboardFormats.Call(format)
		if next == 0 {
			break
		}
		format = next
		names = append(names, clipboardFormatName(uint32(format)))
	}
	return names
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

// openClipboardWithRetry 以指数退避重试打开剪贴板，最多 attempts 次。
func openClipboardWithRetry(attempts int) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		ok, _, err := procOpenClipboard.Call(0)
		if ok != 0 {
			return nil
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
	return lastErr
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
			return nil, fmt.Errorf("GetClipboardData(%d): %w", format, err)
		}
		return nil, nil
	}
	ptr, _, err := procGlobalLock.Call(handle)
	if ptr == 0 {
		if err != syscall.Errno(0) {
			return nil, fmt.Errorf("GlobalLock(%d): %w", format, err)
		}
		return nil, nil
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
			return nil, fmt.Errorf("GetClipboardData(CF_UNICODETEXT): %w", err)
		}
		return nil, nil
	}
	ptr, _, err := procGlobalLock.Call(handle)
	if ptr == 0 {
		if err != syscall.Errno(0) {
			return nil, fmt.Errorf("GlobalLock(CF_UNICODETEXT): %w", err)
		}
		return nil, nil
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
			return nil, fmt.Errorf("GetClipboardData(CF_HDROP): %w", err)
		}
		return nil, nil
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
