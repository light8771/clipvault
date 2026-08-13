package clipvault

import (
	"fmt"
	"syscall"
	"unsafe"
)

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
	item := (*searchDrawItem)(pointerFromWin32(lParam))
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
