package clipvault

import (
	"clipboard_ext/internal/licensing"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const (
	wmDestroy          = 0x0002
	wmCommand          = 0x0111
	wmRuntimeTimer     = 0x0113
	wmClipboardUpdate  = 0x031D
	wmTrayIcon         = 0x0400 + 1
	wmRButtonUp        = 0x0205
	wmLButtonDblClk    = 0x0203
	wmHotkey           = 0x0312
	wmReregisterHotkey = 0x0400 + 2

	idTrayIcon        = 100
	idMenuTogglePause = 101
	idMenuSearch      = 102
	idMenuDataDir     = 103
	idMenuAutostart   = 104
	idMenuExit        = 105
	idMenuLogDir      = 106
	idMenuAppDir      = 107
	idHotkeyToggle    = 1000
	idHotkeySearch    = 1001
	idClipboardPoll   = 2001

	clipboardPollIntervalMS = 250

	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	niifInfo = 0x00000001

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
	env      Environment
	cfgMgr   *ConfigManager
	logger   *DailyLogger
	state    *RuntimeState
	recorder *Recorder
	license  *licensing.Manager
	store    *Store
	// i18n 提供托盘菜单/tooltip/通知的当前语言文案；由应用启动时注入
	// （SetI18nService），未注入时（部分测试）回退官方 zh-CN 词条。
	i18n atomic.Pointer[I18nService]
	// searchURL 由主 goroutine（Web 服务重启）写入、消息循环线程（托盘/
	// 快捷搜索）读取，使用 atomic.Value 避免数据竞争。
	searchURL atomic.Value
	hwnd      uintptr
	hIcon     uintptr

	hotkeyMu            sync.Mutex
	pendingHotkey       *HotkeyConfig
	pendingSearchHotkey *HotkeyConfig
	pendingHotkeyResult chan error

	// 剪贴板事件由单个 worker goroutine 串行消费；邮箱只保留尚未读取的
	// 最新 sequence，因为旧 sequence 的内容在系统剪贴板中已不可恢复。
	clipMailbox   *clipMailbox
	clipDone      chan struct{}
	clipCloseOnce sync.Once
	// lastClipboardSequence merges the normal WM_CLIPBOARDUPDATE path with the
	// sequence-number polling fallback. Both producers run on the message-loop
	// thread, so no synchronization is needed.
	lastClipboardSequence uint32

	// suppressUntilSeq 是内部恢复（搜索弹窗写回剪贴板）的序列抑制水位：
	// sequence <= 水位的事件视为应用自身写回，不进入录制队列。
	suppressUntilSeq atomic.Uint32
}

// clipEvent 是一次剪贴板变化信号。source 与 capturedAtMS 在消息派发现场捕获
// （此时前台窗口还是复制动作的来源进程，事件时间也不会被 worker 拥塞污染）；
// 启动补捕事件使用剪贴板所有者进程。
type clipEvent struct {
	source       SourceInfo
	sequence     uint32
	capturedAtMS int64
}

// suppressClipboardSequencesThrough 把内部恢复产生的剪贴板序列纳入抑制水位。
func (r *WindowsRuntime) suppressClipboardSequencesThrough(sequence uint32) {
	if sequence == 0 {
		return
	}
	for {
		cur := r.suppressUntilSeq.Load()
		if cur >= sequence || r.suppressUntilSeq.CompareAndSwap(cur, sequence) {
			return
		}
	}
}

// isSequenceSuppressed 判定事件序列是否被内部恢复抑制。
func (r *WindowsRuntime) isSequenceSuppressed(sequence uint32) bool {
	return sequence <= r.suppressUntilSeq.Load()
}

// HotkeyError 表示全局快捷键注册失败。
type HotkeyError struct{ Msg string }

func (e *HotkeyError) Error() string { return e.Msg }

var activeRuntime *WindowsRuntime

var (
	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procCreateMutexW                  = kernel32.NewProc("CreateMutexW")
	procCloseHandle                   = kernel32.NewProc("CloseHandle")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procClipboardSetTimer             = user32.NewProc("SetTimer")
	procClipboardKillTimer            = user32.NewProc("KillTimer")
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

func NewWindowsRuntime(env Environment, cfgMgr *ConfigManager, logger *DailyLogger, state *RuntimeState, recorder *Recorder, store *Store, searchURL string, license *licensing.Manager) *WindowsRuntime {
	r := &WindowsRuntime{
		env:      env,
		cfgMgr:   cfgMgr,
		logger:   logger,
		state:    state,
		recorder: recorder,
		license:  license,
		store:    store,
	}
	r.SetSearchURL(searchURL)
	if license != nil {
		license.OnChange(func(licensing.Context) { r.updateTrayIcon() })
	}
	return r
}

// SetI18nService 注入运行时翻译服务并注册语言切换钩子：
// 托盘 tooltip 经 NIM_MODIFY 即时刷新；菜单在下次弹出时自然取新文案。
func (r *WindowsRuntime) SetI18nService(svc *I18nService) {
	r.i18n.Store(svc)
	if svc != nil {
		svc.OnLanguageChanged(r.updateTrayIcon)
	}
}

// i18nT 翻译原生 UI 文案；服务未注入时回退官方 zh-CN 词条。
func (r *WindowsRuntime) i18nT(key string, params map[string]any) string {
	if svc := r.i18n.Load(); svc != nil {
		return svc.T(key, params)
	}
	return renderI18nParams(key, builtinZhCNText(key), params, nil)
}

// builtinZhCNOnce 惰性构建官方 zh-CN 词条内存表（服务未注入时的兜底）。
var builtinZhCNOnce sync.Once

var builtinZhCNTable map[string]string

func builtinZhCNText(key string) string {
	builtinZhCNOnce.Do(func() {
		builtinZhCNTable = make(map[string]string, len(BuiltinTranslations))
		for _, entry := range BuiltinTranslations {
			builtinZhCNTable[entry.Key] = entry.ZhCN
		}
	})
	if text, ok := builtinZhCNTable[key]; ok {
		return text
	}
	return key
}

// SearchURL 返回当前 Web 管理界面地址，可从任意线程调用。
func (r *WindowsRuntime) SearchURL() string {
	if v := r.searchURL.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// SetSearchURL 更新 Web 管理界面地址，可从任意线程调用。
func (r *WindowsRuntime) SetSearchURL(url string) {
	r.searchURL.Store(url)
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

	r.startClipWorker()
	defer r.stopClipWorker()

	if ok, _, err := procAddClipboardFormatListener.Call(hwnd); ok == 0 {
		r.logger.Warn("clipboard_listener_failed", map[string]any{"error": err.Error()})
	}
	// A small number of producers (notably some scrolling-screenshot flows) update
	// the clipboard sequence without delivering WM_CLIPBOARDUPDATE to every
	// listener. Polling the cheap sequence counter closes that gap; the shared
	// lastClipboardSequence watermark prevents duplicate recording.
	if timerID, _, err := procClipboardSetTimer.Call(hwnd, idClipboardPoll, clipboardPollIntervalMS, 0); timerID == 0 {
		r.logger.Warn("clipboard_poll_timer_failed", map[string]any{"error": err.Error()})
	}
	if err := r.addTrayIcon(); err != nil {
		r.logger.Warn("tray_add_failed", map[string]any{"error": err.Error()})
	}
	r.registerHotkey()
	r.registerSearchHotkey()
	r.state.OnPauseChange(func(paused bool) {
		r.updateTrayIcon()
		r.showPauseNotification(paused)
	})
	// 启动时补捕一次当前剪贴板内容，交由 worker 串行处理。首次安全设置期间
	// 监听器尚未启动，因此不能依赖 WM_CLIPBOARDUPDATE 的前台窗口快照；改从
	// Windows 保留的剪贴板所有者窗口解析最后一次写入内容的应用。
	var startupSource SourceInfo
	if r.cfgMgr.Get().RecordSourceInfo {
		startupSource = captureClipboardOwnerSourceFast()
	}
	r.notifyClipboardUpdate(startupSource)

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
			// 来源信息必须在派发现场同步捕获：worker 异步处理时前台窗口
			// 可能已经不是复制动作的来源进程。captureForegroundSourceFast
			// 只做纯句柄查询，不阻塞消息泵。
			var source SourceInfo
			if r.cfgMgr.Get().RecordSourceInfo {
				source = captureForegroundSourceFast()
			}
			r.notifyClipboardUpdate(source)
		}
		return 0
	case wmRuntimeTimer:
		if r != nil && wParam == idClipboardPoll {
			r.pollClipboardUpdate()
			return 0
		}
		ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
		return ret
	case wmTrayIcon:
		if r != nil {

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
		if r != nil {
			switch wParam {
			case idHotkeyToggle:
				paused := r.state.TogglePaused()
				r.logger.Info("pause_toggled", map[string]any{"paused": paused, "source": "hotkey"})
			case idHotkeySearch:
				r.openQuickSearch()
			}
		}
		return 0
	case wmReregisterHotkey:
		if r != nil {
			r.hotkeyMu.Lock()
			hk := r.pendingHotkey
			shk := r.pendingSearchHotkey
			resultCh := r.pendingHotkeyResult
			r.pendingHotkey = nil
			r.pendingSearchHotkey = nil
			r.pendingHotkeyResult = nil
			r.hotkeyMu.Unlock()
			if resultCh != nil {
				resultCh <- r.applyHotkeyUpdate(hk, shk)
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
