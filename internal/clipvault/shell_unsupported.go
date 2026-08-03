//go:build !windows

package clipvault

import (
	"os/exec"
	"path/filepath"
)

func openFolderAndSelectItem(path string) error {
	return exec.Command("xdg-open", filepath.Dir(path)).Start()
}

func openDirectory(path string) error {
	return exec.Command("xdg-open", path).Start()
}
