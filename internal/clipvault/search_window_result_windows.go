package clipvault

import (
	"fmt"
	"strings"
)

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
