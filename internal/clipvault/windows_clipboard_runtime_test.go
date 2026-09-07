package clipvault

import (
	"context"
	"testing"
	"time"
)

func TestClipMailboxKeepsOnlyNewestPendingState(t *testing.T) {
	mailbox := newClipMailbox()
	mailbox.put(clipEvent{sequence: 10, capturedAtMS: 100})
	mailbox.put(clipEvent{sequence: 11, capturedAtMS: 101})

	event, ok := mailbox.take()
	if !ok {
		t.Fatal("mailbox unexpectedly empty")
	}
	if event.sequence != 11 || event.capturedAtMS != 101 {
		t.Fatalf("mailbox returned stale event: %+v", event)
	}
	if _, ok := mailbox.take(); ok {
		t.Fatal("mailbox retained more than one pending state")
	}
}

func TestClipMailboxCloseStillDrainsNewestState(t *testing.T) {
	mailbox := newClipMailbox()
	mailbox.put(clipEvent{sequence: 12})
	mailbox.close()

	if mailbox.isClosedAndEmpty() {
		t.Fatal("closed mailbox reported empty before pending state was drained")
	}
	if event, ok := mailbox.take(); !ok || event.sequence != 12 {
		t.Fatalf("pending state was lost during close: %+v, ok=%v", event, ok)
	}
	if !mailbox.isClosedAndEmpty() {
		t.Fatal("closed mailbox did not become empty after drain")
	}
}

func TestClipMailboxNewSequenceCancelsActiveCapture(t *testing.T) {
	mailbox := newClipMailbox()
	mailbox.put(clipEvent{sequence: 20})
	if _, ok := mailbox.take(); !ok {
		t.Fatal("initial event missing")
	}
	ctx, finish := mailbox.beginCapture()
	defer finish()

	mailbox.put(clipEvent{sequence: 21})
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("new sequence did not cancel the active capture")
	}
	if context.Cause(ctx) != ErrClipboardSuperseded {
		t.Fatalf("cancellation cause = %v", context.Cause(ctx))
	}
	if event, ok := mailbox.take(); !ok || event.sequence != 21 {
		t.Fatalf("newest event was not retained: %+v, ok=%v", event, ok)
	}
}

func TestClipMailboxAllowsQueuedCaptureDuringCloseDrain(t *testing.T) {
	mailbox := newClipMailbox()
	mailbox.put(clipEvent{sequence: 30})
	if _, ok := mailbox.take(); !ok {
		t.Fatal("queued event missing")
	}
	mailbox.close()
	ctx, finish := mailbox.beginCapture()
	defer finish()
	select {
	case <-ctx.Done():
		t.Fatal("close canceled an event already dequeued for shutdown drain")
	default:
	}
}

func TestClipboardSequenceClaimDeduplicatesMessageAndPollingPaths(t *testing.T) {
	runtime := &WindowsRuntime{}
	if !runtime.claimClipboardSequence(41) {
		t.Fatal("first observation did not claim the sequence")
	}
	if runtime.claimClipboardSequence(41) {
		t.Fatal("duplicate sequence was claimed twice")
	}
	if !runtime.claimClipboardSequence(42) {
		t.Fatal("new sequence was rejected")
	}
}

func TestClipboardSequenceClaimAllowsUnknownSequence(t *testing.T) {
	runtime := &WindowsRuntime{}
	for attempt := 0; attempt < 2; attempt++ {
		if !runtime.claimClipboardSequence(0) {
			t.Fatalf("unknown sequence was rejected on attempt %d", attempt+1)
		}
	}
}

func TestSuppressedClipboardSequenceIsClaimedWithoutBeingQueued(t *testing.T) {
	runtime := &WindowsRuntime{clipMailbox: newClipMailbox()}
	runtime.suppressClipboardSequencesThrough(50)
	runtime.notifyClipboardSequence(SourceInfo{PID: 123}, 50, 1000)
	if _, ok := runtime.clipMailbox.take(); ok {
		t.Fatal("suppressed clipboard sequence was queued")
	}
	if runtime.claimClipboardSequence(50) {
		t.Fatal("polling could reclaim an already suppressed sequence")
	}
}

func TestClipboardSequenceSuppressionHandlesUint32Wraparound(t *testing.T) {
	runtime := &WindowsRuntime{}
	runtime.suppressClipboardSequencesThrough(^uint32(0) - 1)
	if !runtime.isSequenceSuppressed(^uint32(0)-2) || !runtime.isSequenceSuppressed(^uint32(0)-1) {
		t.Fatal("initial high sequence suppression was not recorded")
	}
	if runtime.isSequenceSuppressed(^uint32(0)) {
		t.Fatal("sequence after the suppression watermark was hidden")
	}

	runtime.suppressClipboardSequencesThrough(1)
	if !runtime.isSequenceSuppressed(^uint32(0)) || !runtime.isSequenceSuppressed(1) {
		t.Fatal("suppression watermark did not advance across uint32 wraparound")
	}
	if runtime.isSequenceSuppressed(2) {
		t.Fatal("post-wrap user sequence was hidden")
	}
}

func TestClearPendingHotkeyUpdateDoesNotEraseNewerRequest(t *testing.T) {
	runtime := &WindowsRuntime{}
	oldResult := make(chan error, 1)
	newResult := make(chan error, 1)
	runtime.pendingHotkey = &HotkeyConfig{Enabled: true, Key: 65}
	runtime.pendingSearchHotkey = &HotkeyConfig{Enabled: true, Key: 66}
	runtime.pendingHotkeyResult = newResult

	runtime.clearPendingHotkeyUpdate(oldResult)
	if runtime.pendingHotkeyResult != newResult || runtime.pendingHotkey == nil || runtime.pendingSearchHotkey == nil {
		t.Fatal("stale cleanup erased a newer hotkey request")
	}

	runtime.clearPendingHotkeyUpdate(newResult)
	if runtime.pendingHotkeyResult != nil || runtime.pendingHotkey != nil || runtime.pendingSearchHotkey != nil {
		t.Fatal("matching pending hotkey request was not cleared")
	}
}
