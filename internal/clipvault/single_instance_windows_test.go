//go:build windows

package clipvault

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAcquireInstanceLockRejectsDuplicateAndReleases(t *testing.T) {
	name := fmt.Sprintf("ClipVaultTest-%d", time.Now().UnixNano())
	first, err := acquireInstanceLock(name)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if _, err := acquireInstanceLock(name); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate acquire error = %v, want already running", err)
	}
	first.Close()

	second, err := acquireInstanceLock(name)
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	second.Close()
}
