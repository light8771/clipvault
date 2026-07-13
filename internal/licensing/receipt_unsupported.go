//go:build !windows

package licensing

func writeReceipt(path, token string) error   { return ErrLicenseCorrupt }
func readReceipt(path string) (string, error) { return "", ErrLicenseMissing }
