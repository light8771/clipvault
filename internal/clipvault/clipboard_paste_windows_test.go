//go:build windows

package clipvault

import (
	"testing"
	"time"
	"unsafe"
)

func TestPasteInputWindowsAMD64Layout(t *testing.T) {
	var input keybdInput
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("application targets windows/amd64")
	}
	if unsafe.Sizeof(input) != 40 || unsafe.Offsetof(input.Vk) != 8 || unsafe.Offsetof(input.Flags) != 12 || unsafe.Offsetof(input.ExtraInfo) != 24 {
		t.Fatalf("invalid INPUT ABI: size=%d vk=%d flags=%d extra=%d", unsafe.Sizeof(input), unsafe.Offsetof(input.Vk), unsafe.Offsetof(input.Flags), unsafe.Offsetof(input.ExtraInfo))
	}
}

func TestPasteHandoffTransitions(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name                    string
		activating, valid, held bool
		foreground              uintptr
		elapsed                 time.Duration
		want                    pasteStep
		wantErr                 bool
	}{
		{"held shortcut stays in popup", false, true, true, 10, 0, pasteWait, false},
		{"release permits restore and activation", false, true, false, 10, 0, pasteActivate, false},
		{"missing target", false, false, false, 10, 0, pasteWait, true},
		{"user switched away before release", false, true, true, 30, 0, pasteWait, true},
		{"release timeout", false, true, true, 10, 3 * time.Second, pasteWait, true},
		{"activation pending", true, true, false, 10, 0, pasteWait, false},
		{"transient null foreground", true, true, false, 0, 0, pasteWait, false},
		{"destination active", true, true, false, 20, 0, pasteSend, false},
		{"target destroyed", true, false, false, 20, 0, pasteWait, true},
		{"focus stolen", true, true, false, 30, 0, pasteWait, true},
		{"new input cancels paste", true, true, true, 20, 0, pasteWait, true},
		{"activation timeout", true, true, false, 10, 500 * time.Millisecond, pasteWait, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			timeout := pasteReleaseTimeout
			if tt.activating {
				timeout = pasteFocusTimeout
			}
			p := searchPaste{popup: 10, target: 20, activating: tt.activating, deadline: now.Add(timeout)}
			got, err := p.next(now.Add(tt.elapsed), tt.foreground, tt.valid, tt.held)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Fatalf("step=%v err=%v, want step=%v error=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestPopupActionKeysBeforeControlDispatch(t *testing.T) {
	sw := &searchWindow{keyOpenMods: -1, keyRestoreMods: 0, keyPasteMods: -2}
	for _, message := range []uint32{wmKeydown, wmSysKeydown} {
		for _, key := range []uintptr{vkReturn, vkEscape} {
			if !sw.handleActionKey(message, key, 0) {
				t.Fatalf("unhandled message=%x key=%x", message, key)
			}
			if !sw.handleActionKey(message, key, 1<<30) {
				t.Fatal("repeat leaked to control")
			}
		}
	}
	if sw.handleActionKey(wmChar, vkReturn, 0) || sw.handleActionKey(wmKeydown, vkDown, 0) {
		t.Fatal("consumed ordinary control input")
	}
}

func TestPopupPendingPasteCancelledOnClose(t *testing.T) {
	sw := &searchWindow{pendingPaste: &searchPaste{target: 20}}
	sw.close()
	if sw.pendingPaste != nil {
		t.Fatal("closed popup retained pending paste")
	}
}

func TestPasteCrossThreadActivationMayOmitDestination(t *testing.T) {
	p := searchPaste{target: 20}
	if p.acceptsDeactivation(0) || p.acceptsDeactivation(20) {
		t.Fatal("deactivation before shortcut release must cancel")
	}
	p.activating = true
	if !p.acceptsDeactivation(0) || !p.acceptsDeactivation(20) || p.acceptsDeactivation(30) {
		t.Fatal("cross-thread handoff must tolerate missing HWND but reject another window")
	}
}

func TestPopupCloseButtonCancelsPendingPaste(t *testing.T) {
	sw := &searchWindow{pendingPaste: &searchPaste{target: 20}}
	sw.wndProc(0, wmCommand, idcSearchClose, 0)
	if sw.pendingPaste != nil {
		t.Fatal("close button ignored while waiting for shortcut release")
	}
}
