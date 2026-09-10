package clipvault

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

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

		if uint16(loword(wParam)) == waInactive {
			// Only our requested handoff may keep the popup alive while
			// waiting for foreground activation. Any other switch cancels it.
			if p := sw.pendingPaste; p != nil && p.acceptsDeactivation(lParam) {
				return 0
			}
			sw.close()
		}
		return 0
	case wmTimer:
		if wParam == searchPasteTimerID {
			sw.advancePaste()
			return 0
		}
		if wParam == searchDebounceTimerID {
			procKillTimer.Call(hwnd, searchDebounceTimerID)
			sw.startSearch(sw.getEdit_text())
		}
		return 0
	case wmSearchResult:
		sw.handleSearchResult(uint64(wParam))
		return 0
	case wmCommand:
		if sw.pendingPaste != nil && loword(wParam) != idcSearchClose {
			return 0
		}
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
				// 双击只恢复到 Windows 剪贴板，不打开浏览器。
				sw.restoreSelection(false)
			}
		case 2003:
			sw.confirmSelection()
		case idcSearchClose:
			sw.close()
		case idcSearchClear:
			sw.clearSearchInput()
		case idcSearchFilterConsecutiveDuplicates:
			sw.toggleConsecutiveDuplicateFilter()
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

	sw.onEditChange()
}

// handleActionKey runs before TranslateMessage for every popup control, so
// clicking the list does not disable shortcuts. Alt+Enter uses WM_SYSKEYDOWN.
func (sw *searchWindow) handleActionKey(message uint32, key, flags uintptr) bool {
	if message != wmKeydown && message != wmSysKeydown {
		return false
	}
	if key != vkReturn && key != vkEscape {
		return false
	}
	// Consume auto-repeat without executing the action again.
	if flags&(1<<30) == 0 {
		if key == vkEscape {
			sw.close()
		} else if sw.pendingPaste == nil {
			sw.dispatchEnterAction()
		}
	}
	return true
}

// Search arrows share the pre-translation path with action shortcuts; no
// per-window subclass callback is allocated or retained after the popup closes.
func (sw *searchWindow) handleSearchArrow(message uint32, key uintptr) bool {
	switch message {
	case wmKeydown:
		switch key {
		case vkDown:
			sw.moveListSelection(1)
			return true
		case vkUp:
			sw.moveListSelection(-1)
			return true
		}
	}
	return false
}

var procGetKeyState = user32.NewProc("GetKeyState")

const (
	vkShiftKey   = 0x10
	vkControlKey = 0x11
	vkMenuKey    = 0x12
)

func keyStateDown(vk int) bool {
	state, _, _ := procGetKeyState.Call(uintptr(vk))
	return state&0x8000 != 0
}

// dispatchEnterAction 按系统配置的三动作快捷键分派 Enter 组合键。
// 未绑定的修饰键组合不触发任何动作。
func (sw *searchWindow) dispatchEnterAction() {
	mods := 0
	if keyStateDown(vkControlKey) {
		mods |= popupModCtrl
	}
	if keyStateDown(vkShiftKey) {
		mods |= popupModShift
	}
	if keyStateDown(vkMenuKey) {
		mods |= popupModAlt
	}
	switch mods {
	case sw.keyOpenMods:
		sw.confirmSelection()
	case sw.keyRestoreMods:
		sw.restoreSelection(false)
	case sw.keyPasteMods:
		sw.restoreSelection(true)
	}
}

func (sw *searchWindow) toggleConsecutiveDuplicateFilter() {
	sw.hideConsecutiveDuplicates = !sw.hideConsecutiveDuplicates
	sw.refreshConsecutiveDuplicateFilterButton()
	if sw.cfgMgr != nil {
		before := sw.cfgMgr.Get()
		cfg := cloneConfig(before)
		cfg.Popup.HideConsecutiveDuplicates = sw.hideConsecutiveDuplicates
		if err := sw.cfgMgr.UpdateFrom(before, cfg); err != nil {
			if sw.logger != nil {
				sw.logger.Warn("search_window_filter_persist_failed", map[string]any{"error": err.Error()})
			}
			var runtimeErr *ConfigRuntimeApplyError
			if !errors.As(err, &runtimeErr) {
				sw.hideConsecutiveDuplicates = sw.cfgMgr.Get().Popup.HideConsecutiveDuplicates
				sw.refreshConsecutiveDuplicateFilterButton()
			}
		}
	}
	sw.startSearch(sw.getEdit_text())
}

func (sw *searchWindow) refreshConsecutiveDuplicateFilterButton() {
	if sw.hFilterConsecutiveDuplicates == 0 {
		return
	}
	label, _ := syscall.UTF16PtrFromString(filterToggleLabel(
		sw.t("swindow.filter_consecutive_duplicates", nil), sw.hideConsecutiveDuplicates,
	))
	procSetWindowTextW.Call(sw.hFilterConsecutiveDuplicates, uintptr(unsafe.Pointer(label)))
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
	hideConsecutiveDuplicates := sw.hideConsecutiveDuplicates
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	sw.searchCancel = cancel
	sw.mu.Unlock()
	sw.showSearchStatus(sw.t("swindow.searching", nil))

	go func() {
		opts := SearchOptions{
			Query:                     q,
			Limit:                     quickSearchResultLimit,
			IncludeSensitive:          false,
			SkipTotal:                 true,
			HideConsecutiveDuplicates: hideConsecutiveDuplicates,
		}
		if sw.license != nil && !sw.license.CanUsePaidFeatures() {
			// 免费版：先在最新 100 条原始记录内过滤，不向更早的记录补数量。
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
		sw.showSearchStatus(sw.t("swindow.search_failed", nil))
		return
	}
	sw.recordSearchHistory(result.query, int64(len(result.items)))
	sw.refreshList(result.items, result.query)
}

// recordSearchHistory 把弹窗中的非空搜索写入搜索历史，与 Web 端 handleSearch
// 的 LogSearchQuery 行为保持一致。弹窗搜索使用 SkipTotal，结果数取实际返回条数。
// 在后台 goroutine 中落库，避免阻塞弹窗的 GUI 线程。
func (sw *searchWindow) recordSearchHistory(query string, resultCount int64) {
	if strings.TrimSpace(query) == "" || sw.store == nil {
		return
	}
	// filters 持久化词条 key（与 Web 端 searchHistoryFilters 同一约定）。
	filters := map[string]string{}
	if sw.hideConsecutiveDuplicates {
		filters["shlog.filter.consecutive_duplicates"] = "shlog.filter.hidden"
	}
	go func() {
		if err := sw.store.LogSearchQuery(context.Background(), query, filters, resultCount); err != nil && sw.logger != nil {
			sw.logger.Warn("search_window_search_history_log_failed", map[string]any{"error": err.Error()})
		}
	}()
}

func (sw *searchWindow) refreshList(items []StoredItem, query string) {
	if sw.hList == 0 {
		return
	}
	procSendMessageW.Call(sw.hList, lbResetContent, 0, 0)
	sw.items = items
	if len(items) == 0 {
		message := sw.t("swindow.no_match", nil)
		if strings.TrimSpace(query) == "" {
			message = sw.t("swindow.empty", nil)
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
	// 有结果时默认选中第一项。
	procSendMessageW.Call(sw.hList, lbSetCurSel, 0, 0)
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
