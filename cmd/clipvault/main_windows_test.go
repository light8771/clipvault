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
	lock, err := clipvault.AcquireInstanceLock()
	if err != nil {
		t.Skipf("ClipVault is already running: %v", err)
	}
	defer lock.Close()

	err = bootstrap.Search(clipvault.Environment{DataDir: t.TempDir()}, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "cannot search while ClipVault is running") {
		t.Fatalf("search error = %v, want instance-lock error", err)
	}
}
