package clipvault

import (
	"errors"
	"time"
)

const (
	idSelectionHotkeyBase = 1100
	idSelectionCopyTimer  = 2002
)

// 仅在消息线程访问；先等用户松键，再注入一次 Ctrl+C。
type selectionCopyRequest struct {
	target   uintptr
	pid      uint32
	key      int
	action   string
	deadline time.Time
	sequence uint32
	sent     bool
}

func (p *selectionCopyRequest) accepts(now time.Time, foreground uintptr, ownerPID, sequence uint32) bool {
	return p.sent && now.Before(p.deadline) && foreground == p.target &&
		ownerPID != 0 && ownerPID == p.pid && sequence != 0 && clipboardSequenceAfter(sequence, p.sequence)
}

func (r *WindowsRuntime) registerSelectionHotkeys(bindings []SelectionHotkey) error {
	var failures []error
	for i, h := range bindings {
		if err := r.doRegisterHotkey(idSelectionHotkeyBase+i, HotkeyConfig{Enabled: h.Enabled, Modifiers: h.Modifiers, Key: h.Key}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (r *WindowsRuntime) unregisterSelectionHotkeys() {
	for i := 0; i < maxSelectionHotkeys; i++ {
		procUnregisterHotKey.Call(r.hwnd.Load(), uintptr(idSelectionHotkeyBase+i))
	}
}

func (r *WindowsRuntime) beginSelectionCopy(index int) {
	if r.selectionCopy != nil {
		return
	}
	bindings := r.cfgMgr.Get().SelectionHotkeys
	if index < 0 || index >= len(bindings) || !bindings[index].Enabled {
		return
	}
	h := bindings[index]
	if h.Action != "copy" && (r.state.IsPaused() || r.license == nil || !r.license.CanUsePaidFeatures()) {
		r.showNotification("ClipVault", r.i18nT("selection.unavailable", nil))
		return
	}
	target, _, _ := procGetForegroundWindow.Call()
	source := captureForegroundSourceFast()
	if target == 0 || source.PID == 0 {
		return
	}
	r.selectionCopy = &selectionCopyRequest{target: target, pid: source.PID, key: h.Key, action: h.Action, deadline: time.Now().Add(3 * time.Second)}
	if timer, _, _ := procClipboardSetTimer.Call(r.hwnd.Load(), idSelectionCopyTimer, 15, 0); timer == 0 {
		r.failSelectionCopy()
	}
}

func (r *WindowsRuntime) cancelSelectionCopy() {
	procClipboardKillTimer.Call(r.hwnd.Load(), idSelectionCopyTimer)
	r.selectionCopy = nil
}

func (r *WindowsRuntime) failSelectionCopy() {
	r.cancelSelectionCopy()
	r.showNotification("ClipVault", r.i18nT("selection.failed", nil))
}

func (r *WindowsRuntime) advanceSelectionCopy() {
	p := r.selectionCopy
	if p == nil {
		return
	}
	foreground, _, _ := procGetForegroundWindow.Call()
	if foreground != p.target || !time.Now().Before(p.deadline) {
		r.failSelectionCopy()
		return
	}
	if p.sent {
		return
	}
	state, _, _ := procGetAsyncKeyState.Call(uintptr(p.key))
	if state&0x8000 != 0 || pasteShortcutKeysDown() {
		return
	}
	p.sequence = clipboardSequenceNumber()
	p.sent = true
	p.deadline = time.Now().Add(2 * time.Second)
	if err := sendShortcutInput('C'); err != nil {
		r.failSelectionCopy()
	}
}

func (r *WindowsRuntime) selectionActionForSequence(sequence uint32) string {
	p := r.selectionCopy
	if p == nil || !p.sent {
		return ""
	}
	foreground, _, _ := procGetForegroundWindow.Call()
	owner := captureClipboardOwnerSourceFast()
	if !p.accepts(time.Now(), foreground, owner.PID, sequence) {
		r.failSelectionCopy()
		return ""
	}
	r.cancelSelectionCopy()
	if p.action == "copy" {
		return ""
	}
	return p.action
}
