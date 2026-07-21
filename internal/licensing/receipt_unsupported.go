//go:build !windows

package licensing

func writeReceipt(path, token string) error   { return ErrLicenseCorrupt }
func readReceipt(path string) (string, error) { return "", ErrLicenseMissing }

// WriteReceipt is a no-op on non-Windows platforms. See receipt_windows.go
// for the real implementation.
func WriteReceipt(path, token string) error { return ErrLicenseCorrupt }
