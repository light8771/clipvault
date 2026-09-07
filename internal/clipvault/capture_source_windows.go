//go:build windows

package clipvault

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	procGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	procGetClipboardOwner          = user32.NewProc("GetClipboardOwner")
	procGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	procSendMessageTimeoutW        = user32.NewProc("SendMessageTimeoutW")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	// procCloseHandle 复用 windows_runtime.go 的既有声明。
)

const (
	processQueryLimitedInformation = 0x1000
	smtoBlock                      = 0x0001
	smtoAbortIfHung                = 0x0002
	smtoErrorOnExit                = 0x0020
	sourceWindowTitleMaxUTF16      = 512
	sourceWindowTitleTimeoutMS     = 100
)

// captureForegroundSourceFast 在 WM_CLIPBOARDUPDATE 派发现场（消息循环线程）
// 同步捕获前台窗口句柄与所属 PID。这两个调用都是纯句柄查询，不与目标进程
// 通信，耗时在纳秒级，绝不会阻塞消息泵。
//
// 必须在此处捕获的原因：worker 是异步消费 clipCh 的，等它真正处理时用户
// 可能已经切换窗口，届时再取前台窗口就不是复制动作的来源了。
//
// 窗口标题、exe 路径等需要跨进程调用的信息一律不在这里取，留给
// resolveDetails 在 worker 里惰性解析。
func captureForegroundSourceFast() SourceInfo {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return SourceInfo{}
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return SourceInfo{}
	}
	return SourceInfo{PID: pid, hwnd: hwnd, Method: "foreground"}
}

// captureClipboardOwnerSourceFast 用于启动时补捕监听器启动前已存在的剪贴板内容。
// 此时前台窗口通常已经是安全设置页，无法代表最初执行复制的应用；Windows
// 保存的剪贴板所有者窗口则仍指向最后一次成功写入剪贴板的进程。
func captureClipboardOwnerSourceFast() SourceInfo {
	hwnd, _, _ := procGetClipboardOwner.Call()
	if hwnd == 0 {
		return SourceInfo{}
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return SourceInfo{}
	}
	return SourceInfo{PID: pid, hwnd: hwnd, Method: "clipboard_owner"}
}

// resolveDetails 在 worker goroutine 里把 captureForegroundSourceFast 捕获的
// PID/句柄解析成可展示的来源信息。所有失败都是静默降级（字段留空）：
// 来源信息是增强元数据，绝不因为它的失败影响剪贴板内容本身的落库。
func (s *SourceInfo) resolveDetails() {
	if s.PID == 0 {
		return
	}
	// exe 路径走 PID 查询，不需要与窗口通信，即使源进程已退出也只是查询失败。
	if path := queryProcessImagePath(s.PID); path != "" {
		s.ExePath = path
		s.ExeName = filepath.Base(path)
	}
	// 窗口标题读取必须自带硬超时。先检查 IsHungAppWindow 再调用
	// GetWindowTextW 仍有检查后挂起的竞态，会永久卡住串行剪贴板 worker。
	if s.hwnd != 0 {
		s.WindowTitle = getWindowTextSafe(s.hwnd)
	}
}

func queryProcessImagePath(pid uint32) string {
	handle, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 {
		return ""
	}
	defer procCloseHandle.Call(handle)

	buf := make([]uint16, 512)
	size := uint32(len(buf))
	ok, _, _ := procQueryFullProcessImageNameW.Call(
		handle, 0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ok == 0 || size == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}

func getWindowTextSafe(hwnd uintptr) string {
	buf := make([]uint16, sourceWindowTitleMaxUTF16+1)
	var copied uintptr
	ok, _, _ := procSendMessageTimeoutW.Call(
		hwnd,
		wmGetText,
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&buf[0])),
		smtoBlock|smtoAbortIfHung|smtoErrorOnExit,
		sourceWindowTitleTimeoutMS,
		uintptr(unsafe.Pointer(&copied)),
	)
	if ok == 0 || copied == 0 {
		return ""
	}
	if copied > sourceWindowTitleMaxUTF16 {
		copied = sourceWindowTitleMaxUTF16
	}
	return syscall.UTF16ToString(buf[:copied])
}
