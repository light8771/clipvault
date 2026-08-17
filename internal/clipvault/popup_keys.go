package clipvault

import (
	"fmt"
	"strings"
)

// 图片捕获尺寸限制（转码后 PNG 字节数）。
const (
	defaultImageMaxBytes = 32 << 20
	minImageMaxBytes     = 64 << 10
	maxImageMaxBytes     = 256 << 20
)

// 弹窗内动作快捷键的修饰键位掩码（仅用于弹窗内部分派，不是 Win32 MOD_*）。
const (
	popupModCtrl  = 1
	popupModShift = 2
	popupModAlt   = 4
)

// parsePopupKeyBinding 解析 "[Ctrl+][Shift+][Alt+]Enter" 形式的弹窗动作快捷键。
// 弹窗内动作以 Enter 为主键（在编辑框中拦截），修饰键可任意组合。
func parsePopupKeyBinding(value string) (mods int, ok bool) {
	parts := strings.Split(strings.TrimSpace(value), "+")
	if len(parts) == 0 {
		return 0, false
	}
	seen := map[int]bool{}
	for i, part := range parts {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "ctrl", "control":
			if seen[popupModCtrl] {
				return 0, false
			}
			seen[popupModCtrl] = true
			mods |= popupModCtrl
		case "shift":
			if seen[popupModShift] {
				return 0, false
			}
			seen[popupModShift] = true
			mods |= popupModShift
		case "alt":
			if seen[popupModAlt] {
				return 0, false
			}
			seen[popupModAlt] = true
			mods |= popupModAlt
		case "enter", "return":
			if i != len(parts)-1 {
				return 0, false
			}
		default:
			return 0, false
		}
	}
	last := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	if last != "enter" && last != "return" {
		return 0, false
	}
	return mods, true
}

// validatePopupKeys 校验三个弹窗动作快捷键：必须可解析且互不冲突。
func validatePopupKeys(popup SearchPopupConfig) error {
	bindings := []struct {
		name  string
		value string
	}{
		{"popup.keyOpen", popup.KeyOpen},
		{"popup.keyRestore", popup.KeyRestore},
		{"popup.keyPaste", popup.KeyPaste},
	}
	seen := map[int]string{}
	for _, b := range bindings {
		mods, ok := parsePopupKeyBinding(b.value)
		if !ok {
			return fmt.Errorf("%s 无效：仅支持 [Ctrl+][Shift+][Alt+]Enter 形式", b.name)
		}
		if other, dup := seen[mods]; dup {
			return fmt.Errorf("%s 与 %s 快捷键冲突", b.name, other)
		}
		seen[mods] = b.name
	}
	return nil
}
