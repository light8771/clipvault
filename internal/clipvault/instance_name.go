package clipvault

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// singleInstanceName returns the mutex name used to enforce a single running
// ClipVault instance. All builds share the same name.
func singleInstanceName(controlDir string) string {
	normalized := strings.ToLower(filepath.Clean(controlDir))
	sum := sha256.Sum256([]byte(normalized))
	return "ClipVaultClipboardRecorder-" + hex.EncodeToString(sum[:8])
}
