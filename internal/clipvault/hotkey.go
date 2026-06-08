package clipvault

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	modAlt     = 0x0001
	modControl = 0x0002
	modShift   = 0x0004
	modWin     = 0x0008
)

func hotkeyMask(cfg HotkeyConfig) (uint32, uint32, error) {
	var mods uint32
	for _, mod := range cfg.Modifiers {
		switch strings.ToLower(strings.TrimSpace(mod)) {
		case "ctrl", "control":
			mods |= modControl
		case "alt":
			mods |= modAlt
		case "shift":
			mods |= modShift
		case "win", "windows", "meta":
			mods |= modWin
		case "":
		default:
			return 0, 0, fmt.Errorf("unknown hotkey modifier %q", mod)
		}
	}
	key, err := virtualKey(cfg.Key)
	if err != nil {
		return 0, 0, err
	}
	return mods, key, nil
}

func virtualKey(key string) (uint32, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	if len(key) == 1 {
		ch := key[0]
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			return uint32(ch), nil
		}
	}
	if strings.HasPrefix(key, "F") {
		n, err := strconv.Atoi(strings.TrimPrefix(key, "F"))
		if err == nil && n >= 1 && n <= 24 {
			return uint32(0x70 + n - 1), nil
		}
	}
	switch key {
	case "SPACE":
		return 0x20, nil
	case "TAB":
		return 0x09, nil
	case "ESC", "ESCAPE":
		return 0x1B, nil
	case "PAUSE":
		return 0x13, nil
	default:
		return 0, fmt.Errorf("unknown hotkey key %q", key)
	}
}
