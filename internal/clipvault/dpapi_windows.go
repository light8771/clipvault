//go:build windows

package clipvault

import "clipboard_ext/internal/clipvault/security"

func DPAPIProtect(data []byte) ([]byte, error) { return security.DPAPIProtect(data) }

func DPAPIUnprotect(data []byte) ([]byte, error) { return security.DPAPIUnprotect(data) }
