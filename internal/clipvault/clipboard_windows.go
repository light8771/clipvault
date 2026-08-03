//go:build windows

package clipvault

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	cfUnicodeText = 13
	cfHDrop       = 15
)

var (
	user32                         = syscall.NewLazyDLL("user32.dll")
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	shell32                        = syscall.NewLazyDLL("shell32.dll")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
	procDragQueryFileW             = shell32.NewProc("DragQueryFileW")
)

// ReadClipboardSnapshot 读取当前剪贴板内容。
// 优先级：Unicode 文本 > 文件拖放路径清单。其他二进制格式忽略。
//
// Win32 剪贴板所有权是按线程记账的：OpenClipboard / CloseClipboard 必须在
// 同一线程上调用。Go 调度器可能在任意 syscall 返回点把 goroutine 迁移到别的
// OS 线程，因此这里锁定当前线程，避免 CloseClipboard 落到错误的线程上导致
// 剪贴板被永久占用（全系统粘贴失效）。
func ReadClipboardSnapshot(cfg Config) (*ClipboardSnapshot, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 剪贴板常被 Office/浏览器等短暂占用，OpenClipboard 立即失败是常态，
	// 用指数退避重试几次再放弃，避免瞬时占用导致这条记录永久丢失。
	if err := openClipboardWithRetry(5); err != nil {
		return nil, fmt.Errorf("OpenClipboard: %w", err)
	}
	defer procCloseClipboard.Call()

	if isClipboardFormatAvailable(cfUnicodeText) {
		return readUnicodeTextClipboard()
	}
	if isClipboardFormatAvailable(cfHDrop) {
		return readFileDropClipboard(cfg)
	}
	return nil, nil
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
	raw := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), u16len)
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
