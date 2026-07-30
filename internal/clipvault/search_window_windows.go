//go:build windows

package clipvault

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"clipboard_ext/internal/licensing"
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

	searchDebounceTimerID  = 1
	searchDebounceDelay    = 80
	quickSearchResultLimit = 20

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

	hwnd    uintptr
	hEdit   uintptr
	hList   uintptr
	hHeader uintptr
	hEmpty  uintptr
	oldEdit uintptr      // 原始 edit 控件 wndproc
	items   []StoredItem // 当前 listbox 对应的搜索结果

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
func ShowSearchWindow(store *Store, searchURL string, logger *DailyLogger, license *licensing.Manager) {
	searchWindowMu.Lock()
	existing := activeSearchWindow
	if existing != nil {
		searchWindowMu.Unlock()
		// Window activation can synchronously dispatch messages. Never hold the
		// registry lock while calling into User32 or the window procedure deadlocks.
		existing.focus()
		return
	}
	sw := &searchWindow{
		store:          store,
		searchURL:      searchURL,
		logger:         logger,
		license:        license,
		pendingResults: make(map[uint64]searchResult),
	}
	activeSearchWindow = sw
	searchWindowMu.Unlock()

	// 在专用线程上运行窗口消息循环
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

	// Modern search surface, while keeping native controls for reliable IME and keyboard handling.
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
	// "搜索"按钮（回车同样触发）
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
	hintBase := fmt.Sprintf("最多显示最近 %d 条匹配记录   X 清空输入   Enter 打开   ↑↓ 选择   Esc 关闭", quickSearchResultLimit)
	if sw.license != nil && !sw.license.CanUsePaidFeatures() {
		hintBase = fmt.Sprintf("免费版仅检索最近 %d 条记录，激活解锁全部   Esc 关闭", FreeTierVisibleLimit)
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

	// 子类化 edit 控件以拦截 Up/Down/Return/Esc
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

func registerSearchWindowClass(instance uintptr, className *uint16) error {
	searchWindowClassOnce.Do(func() {
		wc := wndClassEx{
			Size:      uint32(unsafe.Sizeof(wndClassEx{})),
			WndProc:   searchWindowWndProcPtr,
			Instance:  instance,
			ClassName: className,
		}
		atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			if err != syscall.Errno(0) {
				searchWindowClassErr = fmt.Errorf("RegisterClassExW: %w", err)
			} else {
				searchWindowClassErr = fmt.Errorf("RegisterClassExW failed")
			}
		}
	})
	return searchWindowClassErr
}

func searchWindowWndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	searchWindowMu.Lock()
	sw := activeSearchWindow
	searchWindowMu.Unlock()
	if sw == nil {
		ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
		return ret
	}
	return sw.wndProc(hwnd, message, wParam, lParam)
}

func (sw *searchWindow) initModernPalette() {
	sw.backgroundBrush, _, _ = procSearchCreateBrush.Call(searchColorBackground)
	sw.surfaceBrush, _, _ = procSearchCreateBrush.Call(searchColorSurface)
	sw.accentBrush, _, _ = procSearchCreateBrush.Call(searchColorAccent)
	sw.closeBrush, _, _ = procSearchCreateBrush.Call(searchColorClose)
}

func (sw *searchWindow) initTextFont() {
	face, _ := syscall.UTF16PtrFromString("Segoe UI Emoji")
	sw.emojiFont, _, _ = procSearchCreateFontW.Call(
		uintptr(uint32(^uint32(17))), 0, 0, 0, 400,
		0, 0, 0, defaultCharSet, 0, 0, clearTypeQuality, 0,
		uintptr(unsafe.Pointer(face)),
	)
}

func (sw *searchWindow) applyTextFont(hwnd uintptr) {
	if hwnd == 0 || sw.emojiFont == 0 {
		return
	}
	procSendMessageW.Call(hwnd, wmSetFont, sw.emojiFont, 1)
}

func (sw *searchWindow) paintBackground(hdc uintptr) {
	if sw.backgroundBrush == 0 {
		return
	}
	var rect searchRect
	procGetClientRect.Call(sw.hwnd, uintptr(unsafe.Pointer(&rect)))
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rect)), sw.backgroundBrush)
}

func (sw *searchWindow) controlColor(hdc, child uintptr, brush, textColor uintptr) uintptr {
	if brush == 0 {
		return 0
	}
	if child == sw.hHeader {
		textColor = searchColorAccent
	}
	procSearchSetTextColor.Call(hdc, textColor)
	procSearchSetBkColor.Call(hdc, searchColorBackground)
	if brush == sw.surfaceBrush {
		procSearchSetBkColor.Call(hdc, searchColorSurface)
	}
	return brush
}

func (sw *searchWindow) drawSearchButton(lParam uintptr) bool {
	if lParam == 0 {
		return false
	}
	item := (*searchDrawItem)(unsafe.Pointer(lParam))
	brush := sw.accentBrush
	labelText := "搜索"
	textColor := uintptr(searchColorBackground)
	switch item.ctlID {
	case 2003:
	case idcSearchClear:
		brush = sw.surfaceBrush
		labelText = "X"
		textColor = searchColorSubtle
	case idcSearchClose:
		brush = sw.closeBrush
		labelText = "X"
		textColor = searchColorText
	default:
		return false
	}
	if brush == 0 {
		return false
	}
	procFillRect.Call(item.hdc, uintptr(unsafe.Pointer(&item.rect)), brush)
	procSearchSetTextColor.Call(item.hdc, textColor)
	procSearchSetBkMode.Call(item.hdc, bkModeTransparent)
	label, _ := syscall.UTF16PtrFromString(labelText)
	procDrawTextW.Call(
		item.hdc,
		uintptr(unsafe.Pointer(label)),
		^uintptr(0),
		uintptr(unsafe.Pointer(&item.rect)),
		dtCenter|dtVCenter|dtSingleLine,
	)
	return true
}

func (sw *searchWindow) releaseModernTheme() {
	if sw.backgroundBrush != 0 {
		procDeleteObject.Call(sw.backgroundBrush)
		sw.backgroundBrush = 0
	}
	if sw.surfaceBrush != 0 {
		procDeleteObject.Call(sw.surfaceBrush)
		sw.surfaceBrush = 0
	}
	if sw.accentBrush != 0 {
		procDeleteObject.Call(sw.accentBrush)
		sw.accentBrush = 0
	}
	if sw.closeBrush != 0 {
		procDeleteObject.Call(sw.closeBrush)
		sw.closeBrush = 0
	}
	if sw.emojiFont != 0 {
		procDeleteObject.Call(sw.emojiFont)
		sw.emojiFont = 0
	}
}

// wndProc 搜索窗口的主窗口过程
func (sw *searchWindow) wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCreate:
		return 0
	case wmLButtonDown:
		if int16(hiword(lParam)) < 54 {
			procReleaseCapture.Call()
			procSendMessageW.Call(hwnd, wmNCLButtonDown, 2, 0)
			return 0
		}
	case wmEraseBkgnd:
		sw.paintBackground(wParam)
		return 1
	case wmCtlColorEdit, wmCtlColorListbox:
		return sw.controlColor(wParam, lParam, sw.surfaceBrush, searchColorText)
	case wmCtlColorStatic:
		return sw.controlColor(wParam, lParam, sw.backgroundBrush, searchColorSubtle)
	case wmDrawItem:
		if sw.drawSearchButton(lParam) {
			return 1
		}
	case wmActivate:
		// 失去焦点时自动关闭（与 Spotlight 类似行为）
		if uint16(loword(wParam)) == waInactive {
			sw.close()
		}
		return 0
	case wmTimer:
		if wParam == searchDebounceTimerID {
			procKillTimer.Call(hwnd, searchDebounceTimerID)
			sw.startSearch(sw.getEdit_text())
		}
		return 0
	case wmSearchResult:
		sw.handleSearchResult(uint64(wParam))
		return 0
	case wmCommand:
		ctlID := uint16(loword(wParam))
		notify := uint16(hiword(wParam))
		switch ctlID {
		case idcSearchEdit:
			if notify == enChange {
				sw.onEditChange()
			}
		case idcSearchList:
			switch notify {
			case lbnDblClk:
				sw.confirmSelection()
			}
		case 2003: // 搜索按钮
			sw.confirmSelection()
		case idcSearchClose:
			sw.close()
		case idcSearchClear:
			sw.clearSearchInput()
		}
		return 0
	case wmClose:
		sw.close()
		return 0
	case wmDestroy:
		sw.onDestroy()
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func (sw *searchWindow) clearSearchInput() {
	if sw.hEdit == 0 {
		return
	}
	procSetWindowTextW.Call(sw.hEdit, 0)
	procSetFocus.Call(sw.hEdit)
	// WM_SETTEXT normally emits EN_CHANGE. Reset the debounce timer here as
	// well so native control variations cannot leave stale suggestions visible.
	sw.onEditChange()
}

// editProc 子类化的 edit 控件过程，拦截 Up/Down/Return/Esc
func (sw *searchWindow) editProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmKeydown:
		switch wParam {
		case vkReturn:
			sw.confirmSelection()
			return 0
		case vkEscape:
			sw.close()
			return 0
		case vkDown:
			sw.moveListSelection(1)
			return 0
		case vkUp:
			sw.moveListSelection(-1)
			return 0
		}
	}
	ret, _, _ := procCallWindowProcW.Call(sw.oldEdit, hwnd, uintptr(message), wParam, lParam)
	return ret
}

func (sw *searchWindow) moveListSelection(delta int) {
	if sw.hList == 0 {
		return
	}
	count, _, _ := procSendMessageW.Call(sw.hList, lbGetCount, 0, 0)
	n := int(int32(count))
	if n == 0 {
		return
	}
	cur, _, _ := procSendMessageW.Call(sw.hList, lbGetCurSel, 0, 0)
	idx := int(int32(cur))
	if idx < 0 {
		idx = 0
	} else {
		idx += delta
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	procSendMessageW.Call(sw.hList, lbSetCurSel, uintptr(idx), 0)
}

func (sw *searchWindow) onEditChange() {
	if sw.hwnd != 0 {
		procSetTimer.Call(sw.hwnd, searchDebounceTimerID, searchDebounceDelay, 0)
	}
}

func (sw *searchWindow) getEdit_text() string {
	if sw.hEdit == 0 {
		return ""
	}
	length, _, _ := procGetWindowTextLength.Call(sw.hEdit)
	n := int(int32(length))
	if n <= 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(sw.hEdit, uintptr(unsafe.Pointer(&buf[0])), uintptr(n+1))
	return syscall.UTF16ToString(buf)
}

func (sw *searchWindow) startSearch(q string) {
	if sw.store == nil {
		return
	}

	sw.mu.Lock()
	if sw.closed || sw.hwnd == 0 {
		sw.mu.Unlock()
		return
	}
	if sw.searchCancel != nil {
		sw.searchCancel()
	}
	sw.searchSequence++
	sequence := sw.searchSequence
	hwnd := sw.hwnd
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	sw.searchCancel = cancel
	sw.mu.Unlock()
	sw.showSearchStatus("正在搜索...")

	go func() {
		opts := SearchOptions{
			Query:            q,
			Limit:            quickSearchResultLimit,
			IncludeSensitive: false,
			SkipTotal:        true,
		}
		if sw.license != nil && !sw.license.CanUsePaidFeatures() {
			opts.RestrictToLatestN = FreeTierVisibleLimit
		}
		items, _, err := sw.store.Search(ctx, opts)
		if ctx.Err() != nil {
			return
		}

		sw.mu.Lock()
		if sw.closed || sw.searchSequence != sequence || sw.hwnd != hwnd {
			sw.mu.Unlock()
			return
		}
		sw.pendingResults[sequence] = searchResult{items: items, err: err, query: q}
		sw.mu.Unlock()

		if ok, _, _ := procPostMessageW.Call(hwnd, wmSearchResult, uintptr(sequence), 0); ok == 0 {
			sw.mu.Lock()
			delete(sw.pendingResults, sequence)
			sw.mu.Unlock()
		}
	}()
}

func (sw *searchWindow) handleSearchResult(sequence uint64) {
	sw.mu.Lock()
	result, ok := sw.pendingResults[sequence]
	delete(sw.pendingResults, sequence)
	current := !sw.closed && sw.searchSequence == sequence
	sw.mu.Unlock()
	if !ok || !current {
		return
	}
	if result.err != nil {
		if sw.logger != nil {
			sw.logger.Warn("search_window_query_failed", map[string]any{"error": result.err.Error()})
		}
		sw.showSearchStatus("搜索失败，请重试")
		return
	}
	sw.refreshList(result.items, result.query)
}

func (sw *searchWindow) refreshList(items []StoredItem, query string) {
	if sw.hList == 0 {
		return
	}
	procSendMessageW.Call(sw.hList, lbResetContent, 0, 0)
	sw.items = items
	if len(items) == 0 {
		message := "没有找到匹配的剪贴板记录"
		if strings.TrimSpace(query) == "" {
			message = "暂无可搜索的剪贴板记录"
		}
		sw.showSearchStatus(message)
		return
	}
	sw.hideSearchStatus()
	for i, it := range items {
		label := formatSuggestion(it)
		text, _ := syscall.UTF16PtrFromString(label)
		procSendMessageW.Call(sw.hList, lbAddString, 0, uintptr(unsafe.Pointer(text)))
		procSendMessageW.Call(sw.hList, lbSetItemData, uintptr(i), uintptr(it.ID))
	}
}

func (sw *searchWindow) showSearchStatus(message string) {
	if sw.hEmpty == 0 {
		return
	}
	text, _ := syscall.UTF16PtrFromString(message)
	procSetWindowTextW.Call(sw.hEmpty, uintptr(unsafe.Pointer(text)))
	if sw.hList != 0 {
		procShowWindow.Call(sw.hList, swHide)
	}
	procShowWindow.Call(sw.hEmpty, swShow)
}

func (sw *searchWindow) hideSearchStatus() {
	if sw.hEmpty != 0 {
		procShowWindow.Call(sw.hEmpty, swHide)
	}
	if sw.hList != 0 {
		procShowWindow.Call(sw.hList, swShow)
	}
}

func formatSuggestion(it StoredItem) string {
	ts := it.CreatedAt.Local().Format("01-02 15:04")
	text := strings.ReplaceAll(it.Text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\t", " ")
	text = strings.TrimSpace(text)
	kind := it.Kind
	if kind == "" {
		kind = "text"
	}
	displayKind := quickSearchKindLabel(kind)
	if text == "" {
		text = "[" + displayKind + "]"
	}
	// 截断
	maxRunes := 80
	runes := []rune(text)
	if len(runes) > maxRunes {
		text = string(runes[:maxRunes]) + "…"
	}
	return fmt.Sprintf("%s  %s  %s", ts, displayKind, text)
}

func quickSearchKindLabel(kind string) string {
	switch kind {
	case "text":
		return "文本"
	case "image":
		return "图片"
	case "video":
		return "视频"
	case "archive":
		return "压缩包"
	case "office":
		return "Office"
	case "files":
		return "其他文件"
	default:
		return kind
	}
}

// confirmSelection 当用户按下回车或双击列表项时触发。
// 若列表中有选中项，跳转到 ?locate=<id>；否则跳转到 ?q=<文本>。
func (sw *searchWindow) confirmSelection() {
	q := sw.getEdit_text()
	var selectedID int64
	if sw.hList != 0 {
		idx, _, _ := procSendMessageW.Call(sw.hList, lbGetCurSel, 0, 0)
		if int32(idx) >= 0 {
			data, _, _ := procSendMessageW.Call(sw.hList, lbGetItemData, idx, 0)
			selectedID = int64(data)
		}
	}
	url := sw.buildJumpURL(q, selectedID)
	if url != "" {
		shellExecute("open", url, "")
	}
	sw.close()
}

func (sw *searchWindow) buildJumpURL(q string, locateID int64) string {
	base := strings.TrimRight(sw.searchURL, "/")
	if base == "" {
		return ""
	}
	if locateID > 0 {
		return base + "/?locate=" + fmt.Sprintf("%d", locateID)
	}
	if strings.TrimSpace(q) != "" {
		return base + "/?q=" + urlQueryEscape(q)
	}
	return base + "/"
}

// urlQueryEscape 简单的 URL 查询参数转义（仅处理搜索框文本中可能出现的字符）。
func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '~':
			b.WriteRune(r)
		default:
			// UTF-8 编码后按字节 %XX 转义
			bs := []byte(string(r))
			for _, c := range bs {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
	}
	return b.String()
}

func (sw *searchWindow) close() {
	sw.mu.Lock()
	if sw.closed || sw.hwnd == 0 {
		sw.mu.Unlock()
		return
	}
	sw.closed = true
	hwnd := sw.hwnd
	sw.hwnd = 0
	if sw.searchCancel != nil {
		sw.searchCancel()
		sw.searchCancel = nil
	}
	sw.pendingResults = nil
	sw.mu.Unlock()

	procKillTimer.Call(hwnd, searchDebounceTimerID)
	procShowWindow.Call(hwnd, swHide)
	destroyed, _, _ := procDestroyWindow.Call(hwnd)
	// 退出本线程消息循环
	if destroyed == 0 {
		sw.clearActiveWindow()
		procPostQuitMessage.Call(0)
	}
}

func (sw *searchWindow) onDestroy() {
	sw.mu.Lock()
	sw.closed = true
	sw.hwnd = 0
	sw.hEdit = 0
	sw.hList = 0
	if sw.searchCancel != nil {
		sw.searchCancel()
		sw.searchCancel = nil
	}
	sw.pendingResults = nil
	sw.mu.Unlock()

	sw.releaseModernTheme()
	sw.clearActiveWindow()
	procPostQuitMessage.Call(0)
}

func (sw *searchWindow) focus() {
	sw.mu.Lock()
	hwnd := sw.hwnd
	closed := sw.closed
	sw.mu.Unlock()
	if hwnd == 0 || closed {
		return
	}
	procSetForegroundWindow.Call(hwnd)
	procBringWindowToTop.Call(hwnd)
}

func (sw *searchWindow) clearActiveWindow() {
	searchWindowMu.Lock()
	if activeSearchWindow == sw {
		activeSearchWindow = nil
	}
	searchWindowMu.Unlock()
}

func loword(w uintptr) uint16 { return uint16(w & 0xFFFF) }
func hiword(w uintptr) uint16 { return uint16((w >> 16) & 0xFFFF) }
