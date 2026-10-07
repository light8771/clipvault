//go:build windows

package clipvault

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"clipboard_ext/internal/clipvault/domain"
)

var (
	procEmptyClipboard     = user32.NewProc("EmptyClipboard")
	procSetClipboardData   = user32.NewProc("SetClipboardData")
	procGlobalAlloc        = kernel32.NewProc("GlobalAlloc")
	procGlobalFree         = kernel32.NewProc("GlobalFree")
	procSendInput          = user32.NewProc("SendInput")
	procGetAsyncKeyState   = user32.NewProc("GetAsyncKeyState")
	procPasteIsWindow      = user32.NewProc("IsWindow")
	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
)

const (
	gmemMoveable = 0x0002

	inputKeyboard  = 1
	keyEventFKeyUp = 0x0002
	vkControl      = 0x11
	vkV            = 0x56
)

// errItemContentUnavailable 表示记录没有可恢复的内容（event-only 记录）。
var errItemContentUnavailable = errors.New(templateT("swindow.no_content", nil))

// restoreItemToClipboard 把一条历史记录写回 Windows 剪贴板。
// 返回写入后的剪贴板序列号，调用方应把它推进运行时的抑制水位，
// 使这次写回不会被录制链路当作新事件入库。
//
// 恢复映射：CF_UNICODETEXT 原样恢复文本；CF_HDROP 重建 DROPFILES 结构；
// CLIPVAULT_IMAGE_FILE 读取托管 PNG 并写入注册格式 "PNG"；
// event-only 记录不可恢复，返回 errItemContentUnavailable。
func restoreItemToClipboard(owner uintptr, item StoredItem, dataDir string) (uint32, error) {
	// EmptyClipboard requires a window owner for the subsequent write.
	if owner == 0 {
		return 0, errors.New("clipboard restore requires an owner window")
	}
	representations, err := prepareClipboardRestoreRepresentations(item, dataDir)
	if err != nil {
		return 0, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, err := openClipboardWithRetry(owner, clipboardOpenAttempts); err != nil {
		return 0, fmt.Errorf("OpenClipboard: %w", err)
	}
	needClose := true
	defer func() {
		if needClose {
			procCloseClipboard.Call()
		}
	}()
	if ok, _, err := procEmptyClipboard.Call(); ok == 0 {
		return 0, fmt.Errorf("EmptyClipboard: %w", err)
	}

	// 私有格式标记：录制链路据此识别「这是恢复写回，不是用户复制」。
	if markerID := internalRestoreFormat(); markerID != 0 {
		if err := setClipboardDataBytes(markerID, []byte{0x43, 0x56, 0x52, 0x31}); err != nil {
			return 0, err
		} // "CVR1"
	}
	for _, r := range representations {
		if err := setClipboardDataBytes(r.format, r.data); err != nil {
			return 0, err
		}
	}
	if err := closeClipboard(); err != nil {
		return 0, fmt.Errorf("CloseClipboard: %w", err)
	}
	needClose = false
	return clipboardSequenceNumber(), nil
}

// prepareClipboardRestorePayload performs every fallible validation and file
// read before EmptyClipboard. An invalid imported record or a missing managed
// image therefore cannot destroy the user's current clipboard contents.
func prepareClipboardRestorePayload(item StoredItem, dataDir string) (uint32, []byte, error) {
	if item.RecordType != domain.HistoryRecordContent {
		return 0, nil, errItemContentUnavailable
	}
	if strings.IndexByte(item.Text, 0) >= 0 {
		return 0, nil, errors.New("clipboard content contains an embedded NUL")
	}
	switch item.Format {
	case domain.FormatUnicodeText:
		data, err := prepareClipboardText(item.Text)
		return cfUnicodeText, data, err
	case domain.FormatFileDrop:
		paths := splitNonEmptyLines(item.Text)
		if len(paths) == 0 {
			return 0, nil, errItemContentUnavailable
		}
		if err := validateClipboardFileDropRestore(paths); err != nil {
			return 0, nil, err
		}
		return cfHDrop, encodeClipboardFileDrop(paths), nil
	case domain.FormatImageFile:
		full, err := resolveManagedImagePath(Environment{DataDir: dataDir}, item.Text)
		if err != nil {
			return 0, nil, fmt.Errorf("invalid managed image path: %w", err)
		}
		info, err := os.Lstat(full)
		if err != nil {
			return 0, nil, fmt.Errorf("stat managed image: %w", err)
		}
		if !info.Mode().IsRegular() || item.ByteLen <= 0 || item.ByteLen > maxImageMaxBytes || info.Size() != int64(item.ByteLen) {
			return 0, nil, errors.New("managed image file size mismatch")
		}
		pngBytes, err := os.ReadFile(full)
		if err != nil {
			return 0, nil, fmt.Errorf("read managed image: %w", err)
		}
		if err := validateClipboardPNG(pngBytes); err != nil {
			return 0, nil, fmt.Errorf("validate managed image: %w", err)
		}
		if item.ContentSHA256 == nil || hashBytes(pngBytes) != *item.ContentSHA256 {
			return 0, nil, errors.New("managed image content hash mismatch")
		}
		pngID := clipboardPNGFormat()
		if pngID == 0 {
			return 0, nil, errors.New("register PNG clipboard format failed")
		}
		return pngID, pngBytes, nil
	default:
		return 0, nil, errItemContentUnavailable
	}
}

var (
	internalRestoreOnce sync.Once
	internalRestoreID   uint32
)

func internalRestoreFormat() uint32 {
	internalRestoreOnce.Do(func() {
		internalRestoreID = registerClipboardFormat(internalRestoreFormatName)
	})
	return internalRestoreID
}

func encodeClipboardText(text string) []byte {
	u16 := utf16.Encode([]rune(text))
	buf := make([]byte, (len(u16)+1)*2)
	for i, c := range u16 {
		buf[i*2] = byte(c)
		buf[i*2+1] = byte(c >> 8)
	}
	return buf
}

func prepareClipboardText(text string) ([]byte, error) {
	if payloadBytes := clipboardUTF16PayloadByteLen(text); payloadBytes > maxClipboardTextPayloadBytes {
		return nil, &clipboardTextPayloadLimitError{ByteLen: payloadBytes, Limit: maxClipboardTextPayloadBytes}
	}
	return encodeClipboardText(text), nil
}

func clipboardUTF16CodeUnitLen(value string) int {
	units := 0
	for _, value := range value {
		units += utf16.RuneLen(value)
	}
	return units
}

func clipboardUTF16PayloadByteLen(value string) int {
	return (clipboardUTF16CodeUnitLen(value) + 1) * 2
}

func validateClipboardFileDropRestore(paths []string) error {
	if len(paths) > maxClipboardFileDropPaths {
		return fmt.Errorf("file drop contains %d paths; limit is %d", len(paths), maxClipboardFileDropPaths)
	}
	for index, path := range paths {
		if units := clipboardUTF16CodeUnitLen(path); units > maxFileDropPathUTF16Units {
			return fmt.Errorf("file drop path %d contains %d UTF-16 units; limit is %d", index, units, maxFileDropPathUTF16Units)
		}
	}
	return nil
}

// encodeClipboardFileDrop constructs a wide-character, double-NUL-terminated
// DROPFILES payload.
func encodeClipboardFileDrop(paths []string) []byte {
	var u16 []uint16
	for _, p := range paths {
		u16 = append(u16, utf16.Encode([]rune(p))...)
		u16 = append(u16, 0)
	}
	u16 = append(u16, 0)
	buf := make([]byte, 20+len(u16)*2)
	// DROPFILES.pFiles = 20（结构体大小），fWide = 1。
	buf[0] = 20
	buf[16] = 1
	for i, c := range u16 {
		buf[20+i*2] = byte(c)
		buf[20+i*2+1] = byte(c >> 8)
	}
	return buf
}

// setClipboardDataBytes 通过 GMEM_MOVEABLE 全局内存写入剪贴板。
// 成功后句柄所有权移交系统；失败时负责释放。
func setClipboardDataBytes(format uint32, data []byte) error {
	handle, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if handle == 0 {
		return fmt.Errorf("GlobalAlloc: %w", err)
	}
	ptr, _, _ := procGlobalLock.Call(handle)
	if ptr == 0 {
		procGlobalFree.Call(handle)
		return errors.New("GlobalLock failed")
	}
	copy(unsafe.Slice((*byte)(pointerFromWin32(ptr)), len(data)), data)
	procGlobalUnlock.Call(handle)
	ok, _, err := procSetClipboardData.Call(uintptr(format), handle)
	if ok == 0 {
		procGlobalFree.Call(handle)
		return fmt.Errorf("SetClipboardData(%d): %w", format, err)
	}
	return nil
}

func splitNonEmptyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimRight(line, "\r"); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// keybdInput 对应 x64 的 INPUT(Type=INPUT_KEYBOARD) 结构（40 字节）。
type keybdInput struct {
	Type      uint32
	_         uint32
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
	_         [8]byte // INPUT union extends to 32 bytes; total x64 size is 40, not 48.
}

func pasteShortcutKeysDown() bool {
	for _, key := range [...]uintptr{vkReturn, vkControlKey, vkShiftKey, vkMenuKey, 0x5B, 0x5C} {
		state, _, _ := procGetAsyncKeyState.Call(key)
		if state&0x8000 != 0 {
			return true
		}
	}
	return false
}

func sendPasteInput() error {
	inputs := [4]keybdInput{
		{Type: inputKeyboard, Vk: vkControl},
		{Type: inputKeyboard, Vk: vkV},
		{Type: inputKeyboard, Vk: vkV, Flags: keyEventFKeyUp},
		{Type: inputKeyboard, Vk: vkControl, Flags: keyEventFKeyUp},
	}
	sent, _, sendErr := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]),
	)
	if sent != uintptr(len(inputs)) {
		// A partial insertion can leave our Ctrl/V down. Release only keys
		// from the inserted prefix, without retrying the paste itself.
		if sent > 0 {
			release := inputs[2:]
			if sent == 1 {
				release = inputs[3:]
			}
			procSendInput.Call(uintptr(len(release)), uintptr(unsafe.Pointer(&release[0])), unsafe.Sizeof(release[0]))
		}
		return fmt.Errorf("SendInput delivered %d/%d key events: %v", sent, len(inputs), sendErr)
	}
	return nil
}
