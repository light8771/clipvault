package licensing

// EnforcementEnabled reports whether license validation is mandatory.
// All builds enforce licensing; users must enter an activation code that
// matches the current machine code to enable recording.
func EnforcementEnabled() bool { return true }
