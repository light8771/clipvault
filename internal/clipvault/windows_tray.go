package clipvault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

func (r *WindowsRuntime) addTrayIcon() error {
	icon := createClipboardIcon()
	if icon == 0 {
		icon, _, _ = procLoadIconW.Call(0, 32512)
	}
	r.hIcon.Store(icon)
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
	if r.hwnd.Load() == 0 {
		return
	}
	nid := r.notifyIconData()
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (r *WindowsRuntime) showPauseNotification(paused bool) {
	hwnd := r.hwnd.Load()
	if hwnd == 0 || !r.cfgMgr.Get().Notifications.PauseResume {
		return
	}

	// 标题/正文默认空 = 本地化默认值（标题为品牌名，正文取 i18n 词条）；
	// 用户显式填写的内容原样使用。
	notifications := r.cfgMgr.Get().Notifications
	title := notifications.ResumeTitle
	message := notifications.ResumeBody
	if paused {
		title = notifications.PauseTitle
		message = notifications.PauseBody
	}
	if title == "" {
		title = "ClipVault"
	}
	if message == "" {
		if paused {
			message = r.i18nT("notification.paused.body", nil)
		} else {
			message = r.i18nT("notification.resumed.body", nil)
		}
	}
	r.showNotification(title, message)
}

func (r *WindowsRuntime) showNotification(title, message string) {
	hwnd := r.hwnd.Load()
	if hwnd == 0 {
		return
	}
	nid := notifyIconData{
		Size:      uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd:      hwnd,
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
	clear(dst)
	value, _, _ = strings.Cut(value, "\x00")
	encoded := utf16.Encode([]rune(value))
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
		Hwnd:            r.hwnd.Load(),
		ID:              idTrayIcon,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: wmTrayIcon,
		Icon:            r.hIcon.Load(),
	}
	tip := r.i18nT("tray.tip_recording", nil)
	if r.license != nil && !r.license.CanUsePaidFeatures() {
		tip = r.i18nT("tray.tip_free_tier", map[string]any{"count": FreeTierVisibleLimit})
	} else if r.state.IsPaused() {
		tip = r.i18nT("tray.tip_paused", nil)
	}
	copyUTF16NULTerminated(nid.Tip[:], tip)
	return nid
}

func (r *WindowsRuntime) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	// 菜单每次弹出时重建，文案在弹出当下经 i18n 解析——切语言后无需
	// 主动重建菜单，下一次右键即生效。
	toggleText := r.i18nT("tray.pause_recording", nil)
	if r.state.IsPaused() {
		toggleText = r.i18nT("tray.resume_recording", nil)
	}
	appendMenu(menu, mfString, idMenuTogglePause, toggleText)

	searchFlags := uint32(mfString)
	searchText := r.i18nT("tray.open_search", nil)
	if r.SearchURL() == "" {
		searchFlags |= mfDisabled
		searchText = r.i18nT("tray.open_search_unavailable", nil)
	}
	appendMenu(menu, searchFlags, idMenuSearch, searchText)
	appendMenu(menu, mfString, idMenuDataDir, r.i18nT("tray.open_data_dir", nil))
	appendMenu(menu, mfString, idMenuLogDir, r.i18nT("tray.open_log_dir", nil))
	appendMenu(menu, mfString, idMenuAppDir, r.i18nT("tray.open_app_dir", nil))

	cfg := r.cfgMgr.Get()
	autostartFlags := uint32(mfString)
	if enabled, _, err := AutostartStatus(cfg.Autostart.AppName); err == nil && enabled {
		autostartFlags |= mfChecked
	}
	appendMenu(menu, autostartFlags, idMenuAutostart, r.i18nT("tray.autostart", nil))
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, idMenuExit, r.i18nT("tray.exit", nil))

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	hwnd := r.hwnd.Load()
	procSetForegroundWindow.Call(hwnd)
	procTrackPopupMenu.Call(menu, tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
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
	case idMenuLogDir:
		r.openPath(r.env.LogDirectory())
	case idMenuAppDir:
		exePath, err := os.Executable()
		if err != nil {
			r.logger.Warn("app_directory_open_failed", map[string]any{"error": err.Error()})
			return
		}
		r.openPath(filepath.Dir(exePath))
	case idMenuAutostart:
		r.toggleAutostart()
	case idMenuExit:
		procDestroyWindow.Call(r.hwnd.Load())
	}
}

func (r *WindowsRuntime) openSearch() {
	if strings.TrimSpace(r.SearchURL()) == "" {
		r.logger.Warn("search_open_skipped", map[string]any{"reason": "search_server_disabled"})
		return
	}
	r.openPath(r.SearchURL())
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
	before := r.cfgMgr.Get()
	cfg := cloneConfig(before)
	enabled, _, err := AutostartStatus(cfg.Autostart.AppName)
	if err != nil {
		r.logger.Warn("autostart_status_failed", map[string]any{"error": err.Error()})
		return
	}
	cfg.Autostart.Enabled = !enabled

	if err := r.cfgMgr.UpdateFrom(before, cfg); err != nil {
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
