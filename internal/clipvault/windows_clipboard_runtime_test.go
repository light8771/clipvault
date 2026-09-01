package clipvault

import "testing"

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
	if !runtime.claimClipboardSequence(0) || !runtime.claimClipboardSequence(0) {
		t.Fatal("unknown sequence should remain processable")
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
