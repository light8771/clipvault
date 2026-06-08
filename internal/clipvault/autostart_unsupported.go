//go:build !windows

package clipvault

func SetAutostart(appName string, enabled bool) error {
	return nil
}

func AutostartStatus(appName string) (bool, string, error) {
	return false, "", nil
}
