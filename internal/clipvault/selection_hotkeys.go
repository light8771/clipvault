package clipvault

import (
	"fmt"
)

const maxSelectionHotkeys = 8

// SelectionHotkey 在前台软件复制选区，并对同一次采集设置标记。
type SelectionHotkey struct {
	Enabled   bool   `json:"enabled"`
	Modifiers int    `json:"modifiers"`
	Key       int    `json:"key"`
	Action    string `json:"action"`
}

func (h *SelectionHotkey) UnmarshalJSON(data []byte) error {
	type wire SelectionHotkey
	var decoded wire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return err
	}
	if _, err := requiredJSONObject(data, "selectionHotkeys[]", "enabled", "modifiers", "key", "action"); err != nil {
		return err
	}
	*h = SelectionHotkey(decoded)
	return nil
}

func validSelectionAction(action string) bool {
	switch action {
	case "copy", "favorite", "sensitive", "favorite-sensitive":
		return true
	default:
		return false
	}
}

func selectionActionFlags(action string) (starred, sensitive bool) {
	return action == "favorite" || action == "favorite-sensitive", action == "sensitive" || action == "favorite-sensitive"
}

func validateSelectionHotkeys(cfg Config) error {
	if cfg.SelectionHotkeys == nil || len(cfg.SelectionHotkeys) > maxSelectionHotkeys {
		return fmt.Errorf("选区快捷键必须为数组，最多 %d 条", maxSelectionHotkeys)
	}
	used := map[[2]int]bool{}
	for _, h := range []HotkeyConfig{cfg.Hotkey, cfg.SearchHotkey} {
		if h.Enabled {
			used[[2]int{h.Modifiers, h.Key}] = true
		}
	}
	for i, h := range cfg.SelectionHotkeys {
		if !validSelectionAction(h.Action) {
			return fmt.Errorf("选区快捷键 %d 的动作无效", i+1)
		}
		if err := validateHotkeyConfig("选区快捷键", HotkeyConfig{Enabled: h.Enabled, Modifiers: h.Modifiers, Key: h.Key}); err != nil {
			return err
		}
		if !h.Enabled {
			continue
		}
		for _, other := range []HotkeyConfig{cfg.Hotkey, cfg.SearchHotkey} {
			if other.Enabled && other.Modifiers == 2 && other.Key == 'C' {
				return fmt.Errorf("启用选区快捷键时，其他全局快捷键不能占用 Ctrl+C")
			}
		}
		// 不抢占系统复制/剪切/粘贴，避免注入 Ctrl+C 再次触发自身。
		if h.Modifiers == 2 && (h.Key == 'C' || h.Key == 'X' || h.Key == 'V') {
			return fmt.Errorf("选区快捷键不能占用 Ctrl+C、Ctrl+X 或 Ctrl+V")
		}
		key := [2]int{h.Modifiers, h.Key}
		if used[key] {
			return fmt.Errorf("选区快捷键 %d 与其他快捷键冲突", i+1)
		}
		used[key] = true
	}
	return nil
}
