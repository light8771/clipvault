package clipvault

import (
	"clipboard_ext/internal/licensing"
	"context"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// 搜索弹窗相关 Win32 常量
// 注意：wmCommand、swShowNormal 已在 windows_runtime.go 中定义，此处不重复声明。
const (
	swHide            = 0
	swShow            = 5
	wsPopup           = 0x80000000
	wsVisible         = 0x10000000
	wsBorder          = 0x00800000
	wsCaption         = 0x00C00000
	wsSysMenu         = 0x00080000
	wsChild           = 0x40000000
	wsVscroll         = 0x00200000
	wsHscroll         = 0x00100000
	wsExTopmost       = 0x00000008
	wsExToolWindow    = 0x00000080
	wsExDlgModalFrame = 0x00000001

	esAutoHscroll = 0x0080
	bsOwnerDraw   = 0x000B

	lbsNotify       = 0x0001
	lbsSort         = 0x0002
	lbsHasStrings   = 0x0040
	lbsWantKeyboard = 0x0400

	wmCreate          = 0x0001
	wmEraseBkgnd      = 0x0014
	wmClose           = 0x0010
	wmActivate        = 0x0006
	wmKeydown         = 0x0100
	wmChar            = 0x0102
	wmTimer           = 0x0113
	wmDrawItem        = 0x002B
	wmLButtonDown     = 0x0201
	wmNCLButtonDown   = 0x00A1
	wmCtlColorEdit    = 0x0133
	wmCtlColorListbox = 0x0134
	wmCtlColorStatic  = 0x0138
	wmApp             = 0x8000

	wmSearchResult = wmApp + 1

	waInactive = 0

	enChange = 0x0300

	lbnSelChange = 1
	lbnDblClk    = 2

	lbResetContent = 0x0184
	lbAddString    = 0x0180
	lbGetCurSel    = 0x0188
	lbSetCurSel    = 0x0186
	lbGetText      = 0x0189
	lbGetTextLen   = 0x018A
	lbGetCount     = 0x018B
	lbSetItemData  = 0x019A
	lbGetItemData  = 0x0199

	wmSetText    = 0x000C
	wmGetText    = 0x000D
	wmGetTextLen = 0x000E
	wmSetFocus   = 0x0007
	wmSetFont    = 0x0030

	vkReturn = 0x0D
	vkEscape = 0x1B
	vkUp     = 0x26
	vkDown   = 0x28

	gwlpWndProc = uintptr(^uintptr(0) - 3) // -4 as uintptr (two's complement)

	idcSearchEdit  = 2001
	idcSearchList  = 2002
	idcSearchClose = 2004
	idcSearchClear = 2005

	idcSearchFilterDup     = 2006
	idcSearchFilterUnavail = 2007

	searchDebounceTimerID = 1
	searchDebounceDelay   = 80
	// quickSearchResultLimit 是弹窗展示条数上限。
	// 免费版：先在最新 100 条原始记录内过滤（不回溯补数量）；激活版：过滤后取前 100 条。
	quickSearchResultLimit = 100

	dtCenter          = 0x0001
	dtVCenter         = 0x0004
	dtSingleLine      = 0x0020
	bkModeTransparent = 1
	ssCenter          = 0x0001
	defaultCharSet    = 1
	clearTypeQuality  = 5
)

var (
	procShowWindow          = user32.NewProc("ShowWindow")
	procUpdateWindow        = user32.NewProc("UpdateWindow")
	procSetFocus            = user32.NewProc("SetFocus")
	procGetWindowLongPtrW   = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW     = user32.NewProc("CallWindowProcW")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procSetWindowTextW      = user32.NewProc("SetWindowTextW")
	procGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	procMoveWindow          = user32.NewProc("MoveWindow")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procSetCapture          = user32.NewProc("SetCapture")
	procReleaseCapture      = user32.NewProc("ReleaseCapture")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procBringWindowToTop    = user32.NewProc("BringWindowToTop")
	procSetTimer            = user32.NewProc("SetTimer")
	procKillTimer           = user32.NewProc("KillTimer")
	procGetClientRect       = user32.NewProc("GetClientRect")
	procFillRect            = user32.NewProc("FillRect")
	procDrawTextW           = user32.NewProc("DrawTextW")
	procSearchSetTextColor  = gdi32.NewProc("SetTextColor")
	procSearchSetBkColor    = gdi32.NewProc("SetBkColor")
	procSearchSetBkMode     = gdi32.NewProc("SetBkMode")
	procSearchCreateBrush   = gdi32.NewProc("CreateSolidBrush")
	procSearchCreateFontW   = gdi32.NewProc("CreateFontW")
)

const (
	smCxScreen = 0
	smCyScreen = 1
)

// searchWindow 持有桌面搜索弹窗的状态。同一时间只允许存在一个实例。
type searchWindow struct {
	store     *Store
	searchURL string
	logger    *DailyLogger
	license   *licensing.Manager
	cfgMgr    *ConfigManager
	dataDir   string

	hwnd       uintptr
	hEdit      uintptr
	hList      uintptr
	hHeader    uintptr
	hEmpty     uintptr
	hFilterDup uintptr
	hFilterUn  uintptr
	oldEdit    uintptr      // 原始 edit 控件 wndproc
	items      []StoredItem // 当前 listbox 对应的搜索结果
	prevHwnd   uintptr      // 弹窗打开前的前台窗口（粘贴目标）

	// 弹窗内显示过滤与三动作快捷键（来自系统配置 popup.*）。
	hideDuplicates  bool
	hideUnavailable bool
	keyOpenMods     int
	keyRestoreMods  int
	keyPasteMods    int
	keyOpenLabel    string
	keyRestoreLabel string
	keyPasteLabel   string

	mu              sync.Mutex
	closed          bool
	searchSequence  uint64
	searchCancel    context.CancelFunc
	pendingResults  map[uint64]searchResult
	backgroundBrush uintptr
	surfaceBrush    uintptr
	accentBrush     uintptr
	closeBrush      uintptr
	emojiFont       uintptr
}

type searchResult struct {
	items []StoredItem
	err   error
	query string
}

// filterToggleLabel 生成过滤开关按钮文本（开启时带 ● 前缀）。
func (sw *searchWindow) filterToggleLabel(name string, on bool) string {
	if on {
		return "● " + name
	}
	return "○ " + name
}

type searchRect struct {
	left, top, right, bottom int32
}

type searchDrawItem struct {
	ctlType    uint32
	ctlID      uint32
	itemID     uint32
	itemAction uint32
	itemState  uint32
	hwndItem   uintptr
	hdc        uintptr
	rect       searchRect
	itemData   uintptr
}

const (
	searchColorBackground = 0x140C07 // #070C14
	searchColorSurface    = 0x201810 // #101820
	searchColorText       = 0xF3F6E8 // #E8F6F3
	searchColorSubtle     = 0xBFC48F // #8FC4BF
	searchColorAccent     = 0xA6B814 // #14B8A6
	searchColorClose      = 0x635CD9 // #D95C63
)

var (
	activeSearchWindow     *searchWindow
	searchWindowMu         sync.Mutex
	searchWindowClassOnce  sync.Once
	searchWindowClassErr   error
	searchWindowWndProcPtr = syscall.NewCallback(searchWindowWndProc)
)

// ShowSearchWindow 在新线程上启动搜索弹窗。如果已有弹窗则将其前置。
// prevHwnd（弹窗打开前的前台窗口）在创建前捕获，作为 Ctrl+Enter 的粘贴目标。
func ShowSearchWindow(store *Store, searchURL string, logger *DailyLogger, license *licensing.Manager, cfgMgr *ConfigManager, env Environment) {
	searchWindowMu.Lock()
	existing := activeSearchWindow
	if existing != nil {
		searchWindowMu.Unlock()

		existing.focus()
		return
	}
	prevHwnd, _, _ := procGetForegroundWindow.Call()
	sw := &searchWindow{
		store:          store,
		searchURL:      searchURL,
		logger:         logger,
		license:        license,
		cfgMgr:         cfgMgr,
		dataDir:        env.DataDir,
		prevHwnd:       prevHwnd,
		pendingResults: make(map[uint64]searchResult),
	}
	if cfgMgr != nil {
		popup := cfgMgr.Get().Popup
		sw.hideDuplicates = popup.HideDuplicates
		sw.hideUnavailable = popup.HideUnavailable
		sw.keyOpenMods, _ = parsePopupKeyBinding(popup.KeyOpen)
		sw.keyRestoreMods, _ = parsePopupKeyBinding(popup.KeyRestore)
		sw.keyPasteMods, _ = parsePopupKeyBinding(popup.KeyPaste)
		sw.keyOpenLabel = popup.KeyOpen
		sw.keyRestoreLabel = popup.KeyRestore
		sw.keyPasteLabel = popup.KeyPaste
	}
	activeSearchWindow = sw
	searchWindowMu.Unlock()

	go func() {
		runtime.LockOSThread()
		sw.run()
		searchWindowMu.Lock()
		if activeSearchWindow == sw {
			activeSearchWindow = nil
		}
		searchWindowMu.Unlock()
	}()
}

func (sw *searchWindow) run() {
	instance, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("ClipVaultSearchWindow")
	if err := registerSearchWindowClass(instance, className); err != nil {
		if sw.logger != nil {
			sw.logger.Warn("search_window_class_register_failed", map[string]any{"error": err.Error()})
		}
		return
	}

	title, _ := syscall.UTF16PtrFromString("ClipVault 快速搜索")
	cx := 680
	cy := 500
	screenCx, _, _ := procGetSystemMetrics.Call(smCxScreen)
	screenCy, _, _ := procGetSystemMetrics.Call(smCyScreen)
	x := (int(screenCx) - cx) / 2
	y := (int(screenCy) - cy) / 3

	hwnd, _, _ := procCreateWindowExW.Call(
		uintptr(wsExTopmost|wsExToolWindow),
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		uintptr(wsPopup|wsVisible),
		uintptr(x), uintptr(y), uintptr(cx), uintptr(cy),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return
	}
	sw.mu.Lock()
	sw.hwnd = hwnd
	sw.mu.Unlock()
	sw.initModernPalette()

	editCls, _ := syscall.UTF16PtrFromString("EDIT")
	listCls, _ := syscall.UTF16PtrFromString("LISTBOX")
	staticCls, _ := syscall.UTF16PtrFromString("STATIC")
	headerText, _ := syscall.UTF16PtrFromString("CLIPVAULT")
	sw.hHeader, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(staticCls)),
		uintptr(unsafe.Pointer(headerText)),
		uintptr(wsChild|wsVisible),
		20, 14, 150, 18,
		hwnd, 0, instance, 0,
	)
	subtitleText, _ := syscall.UTF16PtrFromString("快速搜索")
	procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(staticCls)),
		uintptr(unsafe.Pointer(subtitleText)),
		uintptr(wsChild|wsVisible),
		20, 32, 180, 20,
		hwnd, 0, instance, 0,
	)
	sw.hEdit, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(editCls)),
		0,
		uintptr(wsChild|wsVisible|wsBorder|esAutoHscroll),
		20, 64, 502, 34,
		hwnd, uintptr(idcSearchEdit), instance, 0,
	)

	btnCls, _ := syscall.UTF16PtrFromString("BUTTON")
	btnText, _ := syscall.UTF16PtrFromString("搜索")
	closeText, _ := syscall.UTF16PtrFromString("X")
	procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(btnCls)),
		uintptr(unsafe.Pointer(closeText)),
		uintptr(wsChild|wsVisible|bsOwnerDraw),
		636, 12, 24, 24,
		hwnd, uintptr(idcSearchClose), instance, 0,
	)
	clearText, _ := syscall.UTF16PtrFromString("X")
	procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(btnCls)),
		uintptr(unsafe.Pointer(clearText)),
		uintptr(wsChild|wsVisible|bsOwnerDraw),
		528, 69, 24, 24,
		hwnd, uintptr(idcSearchClear), instance, 0,
	)
	procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(btnCls)),
		uintptr(unsafe.Pointer(btnText)),
		uintptr(wsChild|wsVisible|bsOwnerDraw),
		568, 64, 92, 34,
		hwnd, 2003, instance, 0,
	)
	// 显示过滤开关（重复事件 / 无内容记录），初始状态来自系统配置 popup.*。
	dupLabel, _ := syscall.UTF16PtrFromString(sw.filterToggleLabel("滤重", sw.hideDuplicates))
	sw.hFilterDup, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(btnCls)),
		uintptr(unsafe.Pointer(dupLabel)),
		uintptr(wsChild|wsVisible|bsOwnerDraw),
		420, 12, 100, 24,
		hwnd, uintptr(idcSearchFilterDup), instance, 0,
	)
	unLabel, _ := syscall.UTF16PtrFromString(sw.filterToggleLabel("滤无效", sw.hideUnavailable))
	sw.hFilterUn, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(btnCls)),
		uintptr(unsafe.Pointer(unLabel)),
		uintptr(wsChild|wsVisible|bsOwnerDraw),
		528, 12, 100, 24,
		hwnd, uintptr(idcSearchFilterUnavail), instance, 0,
	)
	sw.hList, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(listCls)),
		0,
		uintptr(wsChild|wsVisible|wsBorder|lbsNotify|lbsHasStrings|wsVscroll),
		20, 112, 640, 308,
		hwnd, uintptr(idcSearchList), instance, 0,
	)
	emptyText, _ := syscall.UTF16PtrFromString("")
	sw.hEmpty, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(staticCls)),
		uintptr(unsafe.Pointer(emptyText)),
		uintptr(wsChild|ssCenter),
		20, 250, 640, 28,
		hwnd, 0, instance, 0,
	)
	sw.initTextFont()
	sw.applyTextFont(sw.hEdit)
	sw.applyTextFont(sw.hList)
	sw.applyTextFont(sw.hEmpty)
	hintBase := fmt.Sprintf("最近 %d 条   %s 打开   双击/%s 恢复   %s 粘贴   Esc 关闭",
		quickSearchResultLimit, sw.keyOpenLabel, sw.keyRestoreLabel, sw.keyPasteLabel)
	if sw.license != nil && !sw.license.CanUsePaidFeatures() {
		hintBase = fmt.Sprintf("免费版仅在最近 %d 条内筛选，激活解锁全部   Esc 关闭", FreeTierVisibleLimit)
	}
	hintText, _ := syscall.UTF16PtrFromString(hintBase)
	procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(staticCls)),
		uintptr(unsafe.Pointer(hintText)),
		uintptr(wsChild|wsVisible),
		20, 442, 640, 18,
		hwnd, 0, instance, 0,
	)

	if sw.hEdit != 0 {
		orig, _, _ := procGetWindowLongPtrW.Call(sw.hEdit, gwlpWndProc)
		sw.oldEdit = orig
		procSetWindowLongPtrW.Call(sw.hEdit, gwlpWndProc, syscall.NewCallback(sw.editProc))
	}

	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
	procSetForegroundWindow.Call(hwnd)
	procBringWindowToTop.Call(hwnd)
	if sw.hEdit != 0 {
		procSetFocus.Call(sw.hEdit)
	}
	// 弹窗打开即展示最近记录（空查询 = 最近 N 条）。
	sw.startSearch("")

	// 本地消息循环
	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(ret) {
		case -1:
			return
		case 0:
			return
		default:
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}
}
