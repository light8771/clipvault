package licensing

import "errors"

var (
	ErrLicenseMissing      = errors.New("license missing")
	ErrLicenseCorrupt      = errors.New("license receipt corrupt")
	ErrBadTokenFormat      = errors.New("bad license token format")
	ErrBadSignature        = errors.New("bad license signature")
	ErrUnknownKey          = errors.New("unknown license signing key")
	ErrUnsupportedVersion  = errors.New("unsupported license version")
	ErrWrongProduct        = errors.New("license is for another product")
	ErrWrongEdition        = errors.New("unsupported license edition")
	ErrInvalidKeyID        = errors.New("invalid license key id")
	ErrInvalidLicenseID    = errors.New("invalid license id")
	ErrInvalidMachineID    = errors.New("invalid machine id")
	ErrMachineMismatch     = errors.New("license machine mismatch")
	ErrMachineReadFailed   = errors.New("machine id unavailable")
	ErrLicenseUnconfigured = errors.New("license public key is not configured")
)
