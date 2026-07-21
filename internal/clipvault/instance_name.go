package clipvault

// singleInstanceName returns the mutex name used to enforce a single running
// ClipVault instance. All builds share the same name.
func singleInstanceName() string { return "ClipVaultClipboardRecorder" }
