//go:build windows

package windows

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

func SetAutostart(appName string, enabled bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if appName == "" {
		appName = "ClipVault"
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if !enabled {
		err := key.DeleteValue(appName)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}

	command := fmt.Sprintf(`"%s" run`, exe)
	return key.SetStringValue(appName, command)
}

func AutostartStatus(appName string) (bool, string, error) {
	if appName == "" {
		appName = "ClipVault"
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return false, "", nil
		}
		return false, "", err
	}
	defer key.Close()

	value, _, err := key.GetStringValue(appName)
	if err != nil {
		if err == registry.ErrNotExist {
			return false, "", nil
		}
		return false, "", err
	}
	return strings.TrimSpace(value) != "", value, nil
}
