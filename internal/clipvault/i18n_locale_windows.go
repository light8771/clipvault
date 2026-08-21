//go:build windows

package clipvault

import "syscall"

var procGetUserDefaultUILanguage = syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")

// detectSystemLanguage 读取 Windows 系统 UI 语言并映射为内置语言：
// 中文（含繁体/方言变体）→ zh-CN，其余 → en-US。用户后续显式选择
// 语言后，config.ui.language 写入具体 code，不再走此函数。
func detectSystemLanguage() string {
	langID, _, _ := procGetUserDefaultUILanguage.Call()
	// PRIMARYLANGID = LANGID & 0x3FF；LANG_CHINESE = 0x04。
	if langID&0x3FF == 0x04 {
		return I18nChinese
	}
	return I18nEnglish
}
