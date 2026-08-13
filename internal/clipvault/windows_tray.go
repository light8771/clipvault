package clipvault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func (r *WindowsRuntime) addTrayIcon() error {
	r.hIcon = createClipboardIcon()
	if r.hIcon == 0 {

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

func (r *WindowsRuntime) showPauseNotification(paused bool) {
	if r.hwnd == 0 || !r.cfgMgr.Get().Notifications.PauseResume {
		return
	}

	notifications := r.cfgMgr.Get().Notifications
	title := notifications.ResumeTitle
	message := notifications.ResumeBody
	if paused {
		title = notifications.PauseTitle
		message = notifications.PauseBody
	}
	defaults := DefaultConfig().Notifications
	if title == "" {
		if paused {
			title = defaults.PauseTitle
		} else {
			title = defaults.ResumeTitle
		}
	}
	if message == "" {
		if paused {
			message = defaults.PauseBody
		} else {
			message = defaults.ResumeBody
		}
	}
	nid := notifyIconData{
		Size:      uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd:      r.hwnd,
		ID:        idTrayIcon,
		Flags:     nifInfo,
		InfoFlags: niifInfo,
	}
	copyUTF16NULTerminated(nid.InfoTitle[:], title)
	copyUTF16NULTerminated(nid.Info[:], message)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// copyUTF16NULTerminated reserves the final code unit for the terminator
// required by NOTIFYICONDATA. It also avoids leaving a dangling high surrogate
// when a title or body is truncated at the native buffer boundary.
func copyUTF16NULTerminated(dst []uint16, value string) {
	if len(dst) == 0 {
		return
	}
	encoded := syscall.StringToUTF16(value)
	limit := len(dst) - 1
	if len(encoded) > limit {
		encoded = encoded[:limit]
		if len(encoded) > 0 && encoded[len(encoded)-1] >= 0xD800 && encoded[len(encoded)-1] <= 0xDBFF {
			encoded = encoded[:len(encoded)-1]
		}
	}
	copy(dst, encoded)
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
	if r.license != nil && !r.license.CanUsePaidFeatures() {
		tip = fmt.Sprintf("ClipVault - 免费版：仅可查看最近 %d 条", FreeTierVisibleLimit)
	} else if r.state.IsPaused() {
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
