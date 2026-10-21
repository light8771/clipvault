//go:build !windows

package clipvault

import "path/filepath"

func canonicalExistingPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
