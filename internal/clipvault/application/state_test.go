package application

import "testing"

func TestRuntimeStatePublishesChangesOnly(t *testing.T) {
	state := NewRuntimeState(false)
	var changes []bool
	state.OnPauseChange(func(paused bool) { changes = append(changes, paused) })

	state.SetPaused(false)
	state.SetPaused(true)
	if got := state.TogglePaused(); got {
		t.Fatal("TogglePaused() = true, want false")
	}
	if state.IsPaused() {
		t.Fatal("state remains paused")
	}
	if len(changes) != 2 || !changes[0] || changes[1] {
		t.Fatalf("changes = %v, want [true false]", changes)
	}
}
