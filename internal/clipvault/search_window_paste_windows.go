package clipvault

import (
	"errors"
	"time"
)

const (
	pasteReleaseTimeout = 3 * time.Second
	pasteFocusTimeout   = 500 * time.Millisecond
)

// All transitions run on the popup's message thread. Keep the popup alive
// until the triggering keys are released, so held Enter cannot leak to the
// destination. A timer keeps painting, Escape and deactivation responsive.
type searchPaste struct {
	item       StoredItem
	popup      uintptr
	target     uintptr
	deadline   time.Time
	activating bool
}

// WM_ACTIVATE may omit the other HWND when crossing input queues. A null
// lParam is not cancellation: the timer verifies the actual foreground HWND.
func (p *searchPaste) acceptsDeactivation(other uintptr) bool {
	return p.activating && (other == 0 || other == p.target)
}

type pasteStep uint8

const (
	pasteWait pasteStep = iota
	pasteActivate
	pasteSend
)

func (p *searchPaste) next(now time.Time, foreground uintptr, valid, keysDown bool) (pasteStep, error) {
	if !valid || p.target == 0 {
		return pasteWait, errors.New("previous window is unavailable")
	}
	if !now.Before(p.deadline) {
		return pasteWait, errors.New("paste handoff timed out")
	}
	if !p.activating {
		if foreground != p.popup {
			return pasteWait, errors.New("popup lost focus before shortcut release")
		}
		if !keysDown {
			return pasteActivate, nil
		}
	} else {
		if foreground != 0 && foreground != p.popup && foreground != p.target {
			return pasteWait, errors.New("foreground changed during paste handoff")
		}
		if keysDown {
			return pasteWait, errors.New("new keyboard input during paste handoff")
		}
		if foreground == p.target {
			return pasteSend, nil
		}
	}
	return pasteWait, nil
}

func (sw *searchWindow) beginPaste(item StoredItem) {
	if sw.pendingPaste != nil {
		return
	}
	sw.pendingPaste = &searchPaste{
		item: item, popup: sw.hwnd, target: sw.prevHwnd,
		deadline: time.Now().Add(pasteReleaseTimeout),
	}
	if timer, _, err := procSetTimer.Call(sw.hwnd, searchPasteTimerID, 15, 0); timer == 0 {
		sw.failPaste(err, false)
	}
}

func (sw *searchWindow) advancePaste() {
	p := sw.pendingPaste
	if p == nil {
		return
	}
	foreground, _, _ := procGetForegroundWindow.Call()
	valid, _, _ := procPasteIsWindow.Call(p.target)
	step, err := p.next(time.Now(), foreground, valid != 0, pasteShortcutKeysDown())
	if err != nil {
		sw.failPaste(err, p.activating)
		return
	}
	switch step {
	case pasteActivate:
		if !sw.restoreClipboardItem(p.item) {
			procKillTimer.Call(sw.hwnd, searchPasteTimerID)
			sw.pendingPaste = nil
			return
		}
		// Set this before activation, which can synchronously send WM_ACTIVATE.
		p.activating = true
		p.deadline = time.Now().Add(pasteFocusTimeout)
		procSetForegroundWindow.Call(p.target)
	case pasteSend:
		sw.close()
		// Closing dispatches native messages. Recheck after it, immediately
		// before injection, and never retry a paste into a different window.
		foreground, _, _ := procGetForegroundWindow.Call()
		if foreground != p.target || pasteShortcutKeysDown() {
			sw.failPaste(errors.New("paste target changed before input injection"), true)
			return
		}
		if err := sendPasteInput(); err != nil {
			sw.failPaste(err, true)
		}
	}
}

func (sw *searchWindow) failPaste(err error, restored bool) {
	procKillTimer.Call(sw.hwnd, searchPasteTimerID)
	sw.pendingPaste = nil
	if sw.logger != nil {
		sw.logger.Warn("search_window_paste_failed", map[string]any{"error": err.Error(), "clipboard_restored": restored})
	}
	key := "swindow.paste_cancelled"
	if restored {
		key = "swindow.paste_failed"
	}
	message := sw.t(key, nil)
	if sw.hwnd != 0 {
		sw.showSearchStatus(message)
	} else if activeRuntime != nil {
		activeRuntime.showNotification("ClipVault", message)
	}
}
