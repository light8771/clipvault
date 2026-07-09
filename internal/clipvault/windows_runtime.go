//go:build windows

package clipvault

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	wmDestroy         = 0x0002
	wmCommand         = 0x0111
	wmClipboardUpdate = 0x031D
	wmTrayIcon        = 0x0400 + 1
	wmRButtonUp       = 0x0205
	wmLButtonDblClk   = 0x0203
	wmHotkey          = 0x0312
	wmReregisterHotkey = 0x0400 + 2

	idTrayIcon        = 100
	idMenuTogglePause = 101
	idMenuSearch      = 102
	idMenuDataDir     = 103
	idMenuAutostart   = 104
	idMenuExit        = 105
	idHotkeyToggle    = 1000

	modAlt     = 0x0001
	modControl = 0x0002
	modShift   = 0x0004
	modWin     = 0x0008

	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	notifyIconVersion4 = 4

	mfString       = 0x00000000
	mfSeparator    = 0x00000800
	mfChecked      = 0x00000008
	mfDisabled     = 0x00000002
	tpmRightButton = 0x0002

	swShowNormal = 1
)

type point struct {
	X int32
	Y int32
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type notifyIconData struct {
	Size            uint32
	Hwnd            uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUID            [16]byte
	BalloonIcon     uintptr
}

type WindowsRuntime struct {
	env       Environment
	cfgMgr    *ConfigManager
	logger    *DailyLogger
	state     *RuntimeState
	recorder  *Recorder
	SearchURL string
	hwnd      uintptr
	hIcon     uintptr

	hotkeyMu          sync.Mutex
	pendingHotkey     *HotkeyConfig
	pendingHotkeyResult chan error
}

// HotkeyError 表示全局快捷键注册失败。
type HotkeyError struct{ Msg string }

func (e *HotkeyError) Error() string { return e.Msg }

var activeRuntime *WindowsRuntime

var (
	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procCreateMutexW                  = kernel32.NewProc("CreateMutexW")
	procGetLastError                  = kernel32.NewProc("GetLastError")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procAddClipboardFormatListener    = user32.NewProc("AddClipboardFormatListener")
	procRemoveClipboardFormatListener = user32.NewProc("RemoveClipboardFormatListener")
	procLoadIconW                     = user32.NewProc("LoadIconW")
	procCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	procAppendMenuW                   = user32.NewProc("AppendMenuW")
	procTrackPopupMenu                = user32.NewProc("TrackPopupMenu")
	procDestroyMenu                   = user32.NewProc("DestroyMenu")
	procGetCursorPos                  = user32.NewProc("GetCursorPos")
	procSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	procShellNotifyIconW              = shell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW                 = shell32.NewProc("ShellExecuteW")
	procRegisterHotKey                = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey              = user32.NewProc("UnregisterHotKey")
	procPostMessageW                  = user32.NewProc("PostMessageW")
)

func NewWindowsRuntime(env Environment, cfgMgr *ConfigManager, logger *DailyLogger, state *RuntimeState, recorder *Recorder, searchURL string) *WindowsRuntime {
	return &WindowsRuntime{
		env:       env,
		cfgMgr:    cfgMgr,
		logger:    logger,
		state:     state,
		recorder:  recorder,
		SearchURL: searchURL,
	}
}

func (r *WindowsRuntime) Run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	activeRuntime = r
	instance, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("ClipVaultHiddenWindow")
	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   syscall.NewCallback(wndProc),
		Instance:  instance,
		ClassName: className,
	}
	atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 && err != syscall.Errno(0) {
		return fmt.Errorf("RegisterClassExW: %w", err)
	}

	windowName, _ := syscall.UTF16PtrFromString("ClipVault")
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0,
		0, 0, 0, 0,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		if err != syscall.Errno(0) {
			return fmt.Errorf("CreateWindowExW: %w", err)
		}
		return fmt.Errorf("CreateWindowExW failed")
	}
	r.hwnd = hwnd

	if ok, _, err := procAddClipboardFormatListener.Call(hwnd); ok == 0 {
		r.logger.Warn("clipboard_listener_failed", map[string]any{"error": err.Error()})
	}
	if err := r.addTrayIcon(); err != nil {
		r.logger.Warn("tray_add_failed", map[string]any{"error": err.Error()})
	}
	r.registerHotkey()
	r.state.OnPauseChange(func(bool) { r.updateTrayIcon() })
	go r.recorder.ProcessClipboardUpdate()

	var m msg
	for {
		ret, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(ret) {
		case -1:
			return fmt.Errorf("GetMessageW: %w", err)
		case 0:
			return errAppQuit
		default:
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}
}

func wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	r := activeRuntime
	switch message {
	case wmClipboardUpdate:
		if r != nil {
			go r.recorder.ProcessClipboardUpdate()
		}
		return 0
	case wmTrayIcon:
		if r != nil {
			// NOTIFYICON_VERSION_4: lParam 低16位=事件码，高16位=图标ID
			switch uint32(lParam) & 0xFFFF {
			case wmRButtonUp:
				r.showMenu()
			case wmLButtonDblClk:
				r.openSearch()
			}
		}
		return 0
	case wmCommand:
		if r != nil {
			r.handleCommand(uint16(wParam & 0xffff))
		}
		return 0
	case wmHotkey:
		if r != nil && wParam == idHotkeyToggle {
			paused := r.state.TogglePaused()
			r.logger.Info("pause_toggled", map[string]any{"paused": paused, "source": "hotkey"})
		}
		return 0
	case wmReregisterHotkey:
		if r != nil {
			r.hotkeyMu.Lock()
			hk := r.pendingHotkey
			resultCh := r.pendingHotkeyResult
			r.pendingHotkey = nil
			r.pendingHotkeyResult = nil
			r.hotkeyMu.Unlock()
			if hk != nil && resultCh != nil {
				r.unregisterHotkey()
				err := r.doRegisterHotkey(*hk)
				resultCh <- err
			}
		}
		return 0
	case wmDestroy:
		if r != nil {
			r.cleanup()
		}
		procPostQuitMessage.Call(0)
		return 0
	default:
		ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
		return ret
	}
}

func (r *WindowsRuntime) addTrayIcon() error {
	r.hIcon = createClipboardIcon()
	if r.hIcon == 0 {
		// 绘制失败时回退到系统默认图标
		r.hIcon, _, _ = procLoadIconW.Call(0, 32512)
	}
	nid := r.notifyIconData()
	ok, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if ok == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return fmt.Errorf("Shell_NotifyIconW(NIM_ADD) failed")
	}
	nid.Version = notifyIconVersion4
	procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&nid)))
	return nil
}

func (r *WindowsRuntime) updateTrayIcon() {
	if r.hwnd == 0 {
		return
	}
	nid := r.notifyIconData()
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (r *WindowsRuntime) notifyIconData() notifyIconData {
	nid := notifyIconData{
		Size:            uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd:            r.hwnd,
		ID:              idTrayIcon,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: wmTrayIcon,
		Icon:            r.hIcon,
	}
	tip := "ClipVault - 正在记录"
	if r.state.IsPaused() {
		tip = "ClipVault - 已暂停"
	}
	copy(nid.Tip[:], syscall.StringToUTF16(tip))
	return nid
}

func (r *WindowsRuntime) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	toggleText := "暂停录制"
	if r.state.IsPaused() {
		toggleText = "继续录制"
	}
	appendMenu(menu, mfString, idMenuTogglePause, toggleText)

	searchFlags := uint32(mfString)
	searchText := "搜索历史 / 设置"
	if r.SearchURL == "" {
		searchFlags |= mfDisabled
		searchText = "搜索历史 / 设置（端口不可用）"
	}
	appendMenu(menu, searchFlags, idMenuSearch, searchText)
	appendMenu(menu, mfString, idMenuDataDir, "打开数据目录")

	cfg := r.cfgMgr.Get()
	autostartFlags := uint32(mfString)
	if enabled, _, err := AutostartStatus(cfg.Autostart.AppName); err == nil && enabled {
		autostartFlags |= mfChecked
	}
	appendMenu(menu, autostartFlags, idMenuAutostart, "开机自启动")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, idMenuExit, "退出")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(r.hwnd)
	procTrackPopupMenu.Call(menu, tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, r.hwnd, 0)
}

func appendMenu(menu uintptr, flags uint32, id uint16, text string) {
	var ptr uintptr
	if text != "" {
		u16, _ := syscall.UTF16PtrFromString(text)
		ptr = uintptr(unsafe.Pointer(u16))
	}
	procAppendMenuW.Call(menu, uintptr(flags), uintptr(id), ptr)
}

func (r *WindowsRuntime) handleCommand(id uint16) {
	switch id {
	case idMenuTogglePause:
		paused := r.state.TogglePaused()
		r.logger.Info("pause_toggled", map[string]any{"paused": paused, "source": "tray"})
	case idMenuSearch:
		r.openSearch()
	case idMenuDataDir:
		r.openPath(r.env.DataDir)
	case idMenuAutostart:
		r.toggleAutostart()
	case idMenuExit:
		procDestroyWindow.Call(r.hwnd)
	}
}

func (r *WindowsRuntime) openSearch() {
	if strings.TrimSpace(r.SearchURL) == "" {
		r.logger.Warn("search_open_skipped", map[string]any{"reason": "search_server_disabled"})
		return
	}
	r.openPath(r.SearchURL)
}

func (r *WindowsRuntime) openPath(target string) {
	if target == "" {
		return
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		shellExecute("open", target, "")
		return
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(target), 0o700)
	}
	shellExecute("open", target, "")
}

func (r *WindowsRuntime) toggleAutostart() {
	cfg := r.cfgMgr.Get()
	enabled, _, err := AutostartStatus(cfg.Autostart.AppName)
	if err != nil {
		r.logger.Warn("autostart_status_failed", map[string]any{"error": err.Error()})
		return
	}
	cfg.Autostart.Enabled = !enabled
	// cfgMgr.Update 会持久化配置并触发 OnChange 回调（里面会调用 SetAutostart）
	if err := r.cfgMgr.Update(cfg); err != nil {
		r.logger.Warn("config_save_failed", map[string]any{"error": err.Error()})
		return
	}
	r.logger.Info("autostart_toggled", map[string]any{"enabled": cfg.Autostart.Enabled})
}

func shellExecute(verb, file, params string) {
	verbPtr, _ := syscall.UTF16PtrFromString(verb)
	filePtr, _ := syscall.UTF16PtrFromString(file)
	var paramsPtr *uint16
	if params != "" {
		paramsPtr, _ = syscall.UTF16PtrFromString(params)
	}
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verbPtr)), uintptr(unsafe.Pointer(filePtr)), uintptr(unsafe.Pointer(paramsPtr)), 0, swShowNormal)
}

func (r *WindowsRuntime) cleanup() {
	r.unregisterHotkey()
	procRemoveClipboardFormatListener.Call(r.hwnd)
	nid := r.notifyIconData()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	if r.hIcon != 0 {
		procDestroyIcon.Call(r.hIcon)
		r.hIcon = 0
	}
}

func (r *WindowsRuntime) doRegisterHotkey(hk HotkeyConfig) error {
	if !hk.Enabled || hk.Modifiers == 0 || hk.Key == 0 {
		r.logger.Info("hotkey_disabled", nil)
		return nil
	}
	ok, _, err := procRegisterHotKey.Call(r.hwnd, idHotkeyToggle, uintptr(hk.Modifiers), uintptr(hk.Key))
	if ok == 0 {
		r.logger.Warn("hotkey_register_failed", map[string]any{"error": err.Error()})
		return &HotkeyError{Msg: fmt.Sprintf("快捷键注册失败（可能与其他应用冲突）: %v", err)}
	}
	r.logger.Info("hotkey_registered", map[string]any{"modifiers": hk.Modifiers, "key": hk.Key})
	return nil
}

func (r *WindowsRuntime) registerHotkey() {
	_ = r.doRegisterHotkey(r.cfgMgr.Get().Hotkey)
}

func (r *WindowsRuntime) unregisterHotkey() {
	procUnregisterHotKey.Call(r.hwnd, idHotkeyToggle)
}

// ReregisterHotkey 从任意线程调用，通过 PostMessage 将重注册请求
// 转发到消息循环线程执行，确保 RegisterHotKey 在正确的线程上调用。
// 返回 nil 表示成功，返回 *HotkeyError 表示注册失败。
func (r *WindowsRuntime) ReregisterHotkey(cfg Config) error {
	r.hotkeyMu.Lock()
	r.pendingHotkey = &cfg.Hotkey
	r.pendingHotkeyResult = make(chan error, 1)
	r.hotkeyMu.Unlock()
	procPostMessageW.Call(r.hwnd, wmReregisterHotkey, 0, 0)
	select {
	case err := <-r.pendingHotkeyResult:
		return err
	case <-time.After(3 * time.Second):
		return &HotkeyError{Msg: "快捷键注册超时"}
	}
}

func acquireSingleInstance(name string) error {
	mutexName, _ := syscall.UTF16PtrFromString(`Local\` + name)
	handle, _, _ := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(mutexName)))
	if handle == 0 {
		return fmt.Errorf("CreateMutexW failed")
	}
	last, _, _ := procGetLastError.Call()
	if last == 183 {
		return fmt.Errorf("another ClipVault instance is already running")
	}
	return nil
}
