//go:build !windows

package clipvault

import "errors"

func Run(env Environment) error {
	return errors.New("clipvault tray mode currently supports Windows only")
}
