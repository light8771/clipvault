//go:build windows

package clipvault

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAcquireSingleInstanceRejectsDuplicate(t *testing.T) {
	name := fmt.Sprintf("ClipVaultTest-%d", time.Now().UnixNano())
	if err := acquireSingleInstance(name); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := acquireSingleInstance(name); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate acquire error = %v, want already running", err)
	}
}
