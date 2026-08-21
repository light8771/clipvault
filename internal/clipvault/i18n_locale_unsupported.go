//go:build !windows

package clipvault

// detectSystemLanguage 非 Windows 平台无系统 UI 语言可用，默认英文。
// 托盘模式仅支持 Windows（app_unsupported.go），此 stub 仅为保证包可编译。
func detectSystemLanguage() string {
	return I18nEnglish
}
