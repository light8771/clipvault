//go:build windows

package main

import (
	"io"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault"
	"clipboard_ext/internal/clipvault/bootstrap"
)

func TestSearchRejectsHeldInstanceLock(t *testing.T) {
	// Isolate the process-wide mutex namespace from a user's running app.
	// The search command must still observe the lock held by this test.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	lock, err := clipvault.AcquireInstanceLock()
	if err != nil {
		t.Fatalf("acquire isolated instance lock: %v", err)
	}
	defer lock.Close()

	err = bootstrap.Search(clipvault.Environment{DataDir: t.TempDir()}, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "cannot search while ClipVault is running") {
		t.Fatalf("search error = %v, want instance-lock error", err)
	}
}
