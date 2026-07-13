//go:build !release

package clipvault

import (
	"os"
	"strings"
)

func singleInstanceName() string {
	if value := strings.TrimSpace(os.Getenv("CLIPVAULT_INSTANCE_NAME")); value != "" {
		return value
	}
	return "ClipVaultClipboardRecorder"
}
