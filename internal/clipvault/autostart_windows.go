//go:build windows

package clipvault

import platformwindows "clipboard_ext/internal/clipvault/platform/windows"

func SetAutostart(appName string, enabled bool) error {
	return platformwindows.SetAutostart(appName, enabled)
}

func AutostartStatus(appName string) (bool, string, error) {
	return platformwindows.AutostartStatus(appName)
}
