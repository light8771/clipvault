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

	lbsNotify       = 0x0001
	lbsSort         = 0x0002
	lbsHasStrings   = 0x0040
	lbsWantKeyboard = 0x0400

	wmCreate   = 0x0001
	wmClose    = 0x0010
	wmActivate = 0x0006
	wmKeydown  = 0x0100
	wmChar     = 0x0102

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

	vkReturn = 0x0D
	vkEscape = 0x1B
	vkUp     = 0x26
	vkDown   = 0x28

	gwlpWndProc = uintptr(^uintptr(0) - 3) // -4 as uintptr (two's complement)

	idcSearchEdit = 2001
	idcSearchList = 2002
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

	hwnd     uintptr
	hEdit    uintptr
	hList    uintptr
	oldEdit  uintptr // 原始 edit 控件 wndproc
	debounce uint32  // 简易防抖计数（用 PostMessage 自定义 WM_TIMER 替代）
	timerId  uint32
	items    []StoredItem // 当前 listbox 对应的搜索结果

	mu      sync.Mutex
	visible bool
}

var (
	activeSearchWindow *searchWindow
	searchWindowMu     sync.Mutex
)

// ShowSearchWindow 在新线程上启动搜索弹窗。如果已有弹窗则将其前置。
func ShowSearchWindow(store *Store, searchURL string, logger *DailyLogger) {
	searchWindowMu.Lock()
	if activeSearchWindow != nil {
		// 已存在：仅前置并聚焦
		if activeSearchWindow.hwnd != 0 {
			procSetForegroundWindow.Call(activeSearchWindow.hwnd)
			procBringWindowToTop.Call(activeSearchWindow.hwnd)
			if activeSearchWindow.hEdit != 0 {
				procSetFocus.Call(activeSearchWindow.hEdit)
			}
		}
		searchWindowMu.Unlock()
		return
	}
	sw := &searchWindow{store: store, searchURL: searchURL, logger: logger}
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
	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   syscall.NewCallback(sw.wndProc),
		Instance:  instance,
		ClassName: className,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	title, _ := syscall.UTF16PtrFromString("ClipVault 快速搜索")
	cx := 620
	cy := 420
	screenCx, _, _ := procGetSystemMetrics.Call(smCxScreen)
	screenCy, _, _ := procGetSystemMetrics.Call(smCyScreen)
	x := (int(screenCx) - cx) / 2
	y := (int(screenCy) - cy) / 3

	hwnd, _, _ := procCreateWindowExW.Call(
		uintptr(wsExTopmost|wsExToolWindow),
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		uintptr(wsPopup|wsCaption|wsSysMenu|wsVisible),
		uintptr(x), uintptr(y), uintptr(cx), uintptr(cy),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return
	}
	sw.hwnd = hwnd

	// 子控件
	editCls, _ := syscall.UTF16PtrFromString("EDIT")
	listCls, _ := syscall.UTF16PtrFromString("LISTBOX")
	sw.hEdit, _, _ = procCreateWindowExW.Call(
		uintptr(wsExDlgModalFrame),
		uintptr(unsafe.Pointer(editCls)),
		0,
		uintptr(wsChild|wsVisible|esAutoHscroll),
		12, 12, uintptr(cx-24-90), 24,
		hwnd, uintptr(idcSearchEdit), instance, 0,
	)
	// "搜索"按钮（仅装饰，回车即触发）
	btnCls, _ := syscall.UTF16PtrFromString("BUTTON")
	btnText, _ := syscall.UTF16PtrFromString("搜索")
	procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(btnCls)),
		uintptr(unsafe.Pointer(btnText)),
		uintptr(wsChild|wsVisible),
		uintptr(cx-24-78+10), 12, 78, 24,
		hwnd, 2003, instance, 0,
	)
	sw.hList, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(listCls)),
		0,
		uintptr(wsChild|wsVisible|lbsNotify|lbsHasStrings|wsVscroll),
		12, 44, uintptr(cx-24), uintptr(cy-44-12),
		hwnd, uintptr(idcSearchList), instance, 0,
	)

	// 子类化 edit 控件以拦截 Up/Down/Return
	if sw.hEdit != 0 {
		orig, _, _ := procGetWindowLongPtrW.Call(sw.hEdit, gwlpWndProc)
		sw.oldEdit = orig
		procSetWindowLongPtrW.Call(sw.hEdit, gwlpWndProc, syscall.NewCallback(sw.editProc))
	}

	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
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

// wndProc 搜索窗口的主窗口过程
func (sw *searchWindow) wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCreate:
		return 0
	case wmActivate:
		// 失去焦点时自动关闭（与 Spotlight 类似行为）
		if uint16(loword(wParam)) == waInactive {
			sw.close()
		}
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
		}
		return 0
	case wmClose:
		sw.close()
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
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
	q := sw.getEdit_text()
	sw.runSearch(q)
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

func (sw *searchWindow) runSearch(q string) {
	if sw.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	items, _, err := sw.store.Search(ctx, SearchOptions{
		Query:            q,
		Limit:            8,
		IncludeSensitive: false,
	})
	if err != nil {
		if sw.logger != nil {
			sw.logger.Warn("search_window_query_failed", map[string]any{"error": err.Error()})
		}
		return
	}
	sw.refreshList(items)
}

func (sw *searchWindow) refreshList(items []StoredItem) {
	if sw.hList == 0 {
		return
	}
	procSendMessageW.Call(sw.hList, lbResetContent, 0, 0)
	sw.items = items
	for i, it := range items {
		label := formatSuggestion(it)
		text, _ := syscall.UTF16PtrFromString(label)
		procSendMessageW.Call(sw.hList, lbAddString, 0, uintptr(unsafe.Pointer(text)))
		procSendMessageW.Call(sw.hList, lbSetItemData, uintptr(i), uintptr(it.ID))
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
	if text == "" {
		text = "[" + kind + "]"
	}
	// 截断
	maxRunes := 80
	runes := []rune(text)
	if len(runes) > maxRunes {
		text = string(runes[:maxRunes]) + "…"
	}
	return fmt.Sprintf("%s  %s  %s", ts, kind, text)
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
	if sw.hwnd == 0 {
		return
	}
	procDestroyWindow.Call(sw.hwnd)
	sw.hwnd = 0
	sw.hEdit = 0
	sw.hList = 0
	// 退出本线程消息循环
	procPostQuitMessage.Call(0)
}

func loword(w uintptr) uint16 { return uint16(w & 0xFFFF) }
func hiword(w uintptr) uint16 { return uint16((w >> 16) & 0xFFFF) }
