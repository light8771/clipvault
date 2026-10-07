//go:build !windows

package clipvault

import "errors"

func restoreItemFromWeb(StoredItem, string) error {
	return errors.New("native clipboard restore requires Windows")
}
