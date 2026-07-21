//go:build !windows

package clipvault

// ShowSearchWindow 在非 Windows 平台上为空实现。
func ShowSearchWindow(store *Store, searchURL string, logger *DailyLogger) {}
