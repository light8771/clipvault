//go:build windows

package main

import (
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault"
)

func TestSearchRejectsHeldInstanceLock(t *testing.T) {
	lock, err := clipvault.AcquireInstanceLock()
	if err != nil {
		t.Skipf("ClipVault is already running: %v", err)
	}
	defer lock.Close()

	err = search(clipvault.Environment{DataDir: t.TempDir()}, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot search while ClipVault is running") {
		t.Fatalf("search error = %v, want instance-lock error", err)
	}
}
