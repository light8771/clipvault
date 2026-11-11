package clipvault

import (
	"testing"
	"time"
	"unsafe"
)

func TestSelectionCopyAndPasteInjectDifferentBalancedKeys(t *testing.T) {
	for _, key := range []uint16{'C', 'V'} {
		inputs := shortcutKeyboardInputs(key)
		if unsafe.Sizeof(inputs[0]) != 40 {
			t.Fatal("Windows x64 INPUT layout must be 40 bytes")
		}
		for i, want := range []uint16{0x11, key, key, 0x11} {
			if inputs[i].Type != inputKeyboard || inputs[i].Vk != want {
				t.Fatalf("key %c event %d = %+v", key, i, inputs[i])
			}
			wantFlags := uint32(0)
			if i >= 2 {
				wantFlags = keyEventFKeyUp
			}
			if inputs[i].Flags != wantFlags {
				t.Fatalf("key %c unbalanced event %d", key, i)
			}
		}
	}
}

func TestSelectionCopyDiscardDoesNotMarkNewerClipboard(t *testing.T) {
	mailbox := newClipMailbox()
	if mailbox.put(clipEvent{sequence: 10, selectionAction: "sensitive"}) {
		t.Fatal("first selection was discarded")
	}
	if !mailbox.put(clipEvent{sequence: 11}) {
		t.Fatal("superseded selection was not reported")
	}
	event, ok := mailbox.take()
	if !ok || event.sequence != 11 || event.selectionAction != "" {
		t.Fatalf("selection flag leaked: %+v", event)
	}
	mailbox.close()
	if !mailbox.put(clipEvent{sequence: 12, selectionAction: "favorite"}) {
		t.Fatal("closed mailbox silently dropped selection")
	}
}

func TestSelectionCopyOnlyAcceptsFreshTargetClipboard(t *testing.T) {
	now := time.Now()
	p := selectionCopyRequest{target: 123, pid: 42, sequence: 100, sent: true, deadline: now.Add(time.Second)}
	tests := []struct {
		name       string
		now        time.Time
		foreground uintptr
		pid, seq   uint32
		want       bool
	}{
		{"fresh", now, 123, 42, 101, true},
		{"old clipboard", now, 123, 42, 100, false},
		{"older clipboard", now, 123, 42, 99, false},
		{"unknown sequence", now, 123, 42, 0, false},
		{"other process", now, 123, 99, 101, false},
		{"unknown owner", now, 123, 0, 101, false},
		{"focus changed", now, 456, 42, 101, false},
		{"expired", now.Add(time.Second), 123, 42, 101, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.accepts(tt.now, tt.foreground, tt.pid, tt.seq); got != tt.want {
				t.Fatalf("accepts=%v", got)
			}
		})
	}
	p.sent = false
	if p.accepts(now, 123, 42, 101) {
		t.Fatal("clipboard accepted before copy injection")
	}
	p.sent = true
	p.sequence = 0xffffffff
	if !p.accepts(now, 123, 42, 1) {
		t.Fatal("sequence wraparound was rejected")
	}
}
