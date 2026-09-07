package clipvault

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// diffConfigChanges 比较两份配置，返回逐配置项的新旧值变化（已脱敏、已格式化）。
// 用于操作记录；不区分修改来源，只描述最终持久化的差异。
//
// Label 与受控状态值（开启/关闭/已设置等）一律写入 i18n key
// （config_field.* / config_value.* 前缀），由前端展示时翻译——操作记录
// 是持久化数据，写入时翻译会让历史记录随语言切换而混杂。
func diffConfigChanges(oldCfg, newCfg Config) []ConfigChange {
	oldJSON, err := json.Marshal(oldCfg)
	if err != nil {
		return nil
	}
	newJSON, err := json.Marshal(newCfg)
	if err != nil {
		return nil
	}
	var oldMap, newMap map[string]any
	if json.Unmarshal(oldJSON, &oldMap) != nil || json.Unmarshal(newJSON, &newMap) != nil {
		return nil
	}
	changes := []ConfigChange{}
	walkConfigDiff("", oldMap, newMap, &changes)
	return changes
}

// 一次保存最多记录的变更项数，防止异常大体量 diff 撑爆单条日志。
const maxConfigChangesPerSave = 50

// 受控状态值的词条 key 前缀：前端渲染 old/new 时按该前缀判定是否翻译。
const (
	configValuePrefix      = "config_value."
	configValueNotSet      = "config_value.not_set"
	configValueSet         = "config_value.set"
	configValueUpdated     = "config_value.updated"
	configValueEmpty       = "config_value.empty"
	configValueEmptyString = "config_value.empty_string"
	configValueOn          = "config_value.on"
	configValueOff         = "config_value.off"
)

func walkConfigDiff(path string, oldVal, newVal any, changes *[]ConfigChange) {
	if len(*changes) >= maxConfigChangesPerSave {
		return
	}
	oldMap, oldIsMap := oldVal.(map[string]any)
	newMap, newIsMap := newVal.(map[string]any)
	if oldIsMap && newIsMap {
		for _, key := range slices.Sorted(maps.Keys(unionStringKeys(oldMap, newMap))) {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if configDiffSkippedPaths[childPath] {
				continue
			}
			walkConfigDiff(childPath, oldMap[key], newMap[key], changes)
		}
		return
	}
	if configDiffEqual(oldVal, newVal) {
		return
	}
	label := configFieldLabel(path)
	if configDiffMaskedPaths[path] {
		oldMasked, newMasked := maskSecretState(oldVal), maskSecretState(newVal)
		if oldMasked == newMasked {
			newMasked = configValueUpdated
		}
		*changes = append(*changes, ConfigChange{Path: path, Label: label, Old: oldMasked, New: newMasked})
		return
	}
	*changes = append(*changes, ConfigChange{
		Path:  path,
		Label: label,
		Old:   formatConfigDiffValue(oldVal),
		New:   formatConfigDiffValue(newVal),
	})
}

func unionStringKeys(a, b map[string]any) map[string]struct{} {
	keys := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	return keys
}

func configDiffEqual(a, b any) bool {
	aj, aErr := json.Marshal(a)
	bj, bErr := json.Marshal(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return string(aj) == string(bj)
}

func maskSecretState(value any) string {
	if text, ok := value.(string); !ok || text == "" {
		return configValueNotSet
	}
	return configValueSet
}

const maxConfigDiffValueRunes = 80

func formatConfigDiffValue(value any) string {
	var text string
	switch v := value.(type) {
	case nil:
		text = configValueEmpty
	case bool:
		if v {
			text = configValueOn
		} else {
			text = configValueOff
		}
	case float64:
		if v == float64(int64(v)) {
			text = fmt.Sprintf("%d", int64(v))
		} else {
			text = fmt.Sprintf("%v", v)
		}
	case string:
		text = v
		if text == "" {
			text = configValueEmptyString
		}
	default:
		data, err := json.Marshal(v)
		if err != nil {
			text = fmt.Sprintf("%v", v)
		} else {
			text = string(data)
		}
	}
	if utf8.RuneCountInString(text) > maxConfigDiffValueRunes {
		runes := []rune(text)
		text = string(runes[:maxConfigDiffValueRunes]) + "…"
	}
	return text
}

// 不参与操作记录的字段：版本号与品牌/背景资产元数据（由专用上传接口维护，
// 普通设置保存会原样保留，diff 出来只是内部版本号噪音）。
var configDiffSkippedPaths = map[string]bool{
	"version":                  true,
	"brand.logoContentType":    true,
	"brand.logoVersion":        true,
	"brand.faviconContentType": true,
	"brand.faviconVersion":     true,
	"background.contentType":   true,
	"background.version":       true,
}

// 值需要脱敏的字段：只记录设置状态，不记录明文或哈希。
var configDiffMaskedPaths = map[string]bool{
	"security.lockPasswordHash":      true,
	"security.secondaryPasswordHash": true,
	"backup.dirBackup.password":      true,
	"backup.emailBackup.smtpPass":    true,
	"backup.emailBackup.password":    true,
}

// configFieldLabels 把点分配置路径映射为操作记录展示用的词条 key
// （config_field.*，双语词条见 i18n_seed_config.go）；未收录的路径回退
// 为路径本身（新增字段未登记词条时仍有可读的兜底）。
var configFieldLabels = map[string]string{
	"maxTextChars":                        "config_field.max_text_chars",
	"maxFileDropPaths":                    "config_field.max_file_drop_paths",
	"recordSourceInfo":                    "config_field.record_source_info",
	"source.showExe":                      "config_field.show_exe",
	"source.exeAliases":                   "config_field.exe_aliases",
	"source.exeWhitelist":                 "config_field.exe_whitelist",
	"brand.title":                         "config_field.brand_title",
	"background.fit":                      "config_field.bg_fit",
	"background.opacity":                  "config_field.bg_opacity",
	"background.overlay":                  "config_field.bg_overlay",
	"background.blur":                     "config_field.bg_blur",
	"background.brightness":               "config_field.bg_brightness",
	"background.contrast":                 "config_field.bg_contrast",
	"background.saturation":               "config_field.bg_saturation",
	"background.scale":                    "config_field.bg_scale",
	"background.positionX":                "config_field.bg_position_x",
	"background.positionY":                "config_field.bg_position_y",
	"appearance.preset":                   "config_field.appearance_preset",
	"appearance.mode":                     "config_field.appearance_mode",
	"appearance.primary":                  "config_field.appearance_primary",
	"appearance.accent":                   "config_field.appearance_accent",
	"appearance.fontScale":                "config_field.appearance_font_scale",
	"appearance.surfaceOpacity":           "config_field.appearance_surface_opacity",
	"navigation.sticky":                   "config_field.navigation_sticky",
	"navigation.blur":                     "config_field.navigation_blur",
	"navigation.opacity":                  "config_field.navigation_opacity",
	"navigation.divider":                  "config_field.navigation_divider",
	"historyPage.layout":                  "config_field.history_layout",
	"historyPage.columns":                 "config_field.history_columns",
	"historyPage.forceColumns":            "config_field.history_force_columns",
	"historyPage.contentWidthMode":        "config_field.history_content_width_mode",
	"historyPage.contentWidth":            "config_field.history_content_width",
	"historyPage.horizontalPadding":       "config_field.history_horizontal_padding",
	"historyPage.cardGap":                 "config_field.history_card_gap",
	"historyPage.previewLines":            "config_field.history_preview_lines",
	"historyPage.fixedPreview":            "config_field.history_fixed_preview",
	"search.listenAddr":                   "config_field.web_listen_addr",
	"search.timeZone":                     "config_field.filter_timezone",
	"dataApi.enabled":                     "config_field.data_api_enabled",
	"dataApi.listenAddr":                  "config_field.data_api_listen_addr",
	"mcp.enabled":                         "config_field.mcp_enabled",
	"mcp.listenAddr":                      "config_field.mcp_listen_addr",
	"sensitive.customPatterns":            "config_field.sensitive_patterns",
	"autostart.enabled":                   "config_field.autostart_enabled",
	"autostart.appName":                   "config_field.autostart_app_name",
	"notifications.pauseResume":           "config_field.notify_pause_resume",
	"notifications.pauseTitle":            "config_field.notify_pause_title",
	"notifications.pauseBody":             "config_field.notify_pause_body",
	"notifications.resumeTitle":           "config_field.notify_resume_title",
	"notifications.resumeBody":            "config_field.notify_resume_body",
	"security.lockPasswordHash":           "config_field.lock_password",
	"security.secondaryPasswordHash":      "config_field.secondary_password",
	"security.lockSessionTimeout":         "config_field.lock_session_timeout",
	"security.secondaryAuthTimeout":       "config_field.secondary_auth_timeout",
	"hotkey.enabled":                      "config_field.hotkey_enabled",
	"hotkey.modifiers":                    "config_field.hotkey_modifiers",
	"hotkey.key":                          "config_field.hotkey_key",
	"searchHotkey.enabled":                "config_field.search_hotkey_enabled",
	"searchHotkey.modifiers":              "config_field.search_hotkey_modifiers",
	"searchHotkey.key":                    "config_field.search_hotkey_key",
	"image.enabled":                       "config_field.image_enabled",
	"image.maxBytes":                      "config_field.image_max_bytes",
	"popup.keyOpen":                       "config_field.popup_key_open",
	"popup.keyRestore":                    "config_field.popup_key_restore",
	"popup.keyPaste":                      "config_field.popup_key_paste",
	"popup.hideConsecutiveDuplicates":     "config_field.popup_hide_consecutive_duplicates",
	"backup.dirBackup.enabled":            "config_field.dir_backup_enabled",
	"backup.dirBackup.runOnStartup":       "config_field.dir_backup_run_on_startup",
	"backup.dirBackup.interval":           "config_field.dir_backup_interval",
	"backup.dirBackup.dir":                "config_field.dir_backup_dir",
	"backup.dirBackup.format":             "config_field.dir_backup_format",
	"backup.dirBackup.password":           "config_field.dir_backup_password",
	"backup.dirBackup.scope.mode":         "config_field.dir_backup_scope_mode",
	"backup.dirBackup.scope.from":         "config_field.dir_backup_scope_from",
	"backup.dirBackup.scope.to":           "config_field.dir_backup_scope_to",
	"backup.dirBackup.fullFallbackDays":   "config_field.dir_backup_fallback_days",
	"backup.emailBackup.enabled":          "config_field.email_backup_enabled",
	"backup.emailBackup.runOnStartup":     "config_field.email_backup_run_on_startup",
	"backup.emailBackup.interval":         "config_field.email_backup_interval",
	"backup.emailBackup.smtpHost":         "config_field.email_backup_smtp_host",
	"backup.emailBackup.smtpPort":         "config_field.email_backup_smtp_port",
	"backup.emailBackup.smtpUser":         "config_field.email_backup_smtp_user",
	"backup.emailBackup.smtpPass":         "config_field.email_backup_smtp_pass",
	"backup.emailBackup.from":             "config_field.email_backup_from",
	"backup.emailBackup.to":               "config_field.email_backup_to",
	"backup.emailBackup.format":           "config_field.email_backup_format",
	"backup.emailBackup.password":         "config_field.email_backup_password",
	"backup.emailBackup.subject":          "config_field.email_backup_subject",
	"backup.emailBackup.body":             "config_field.email_backup_body",
	"backup.emailBackup.style":            "config_field.email_backup_style",
	"backup.emailBackup.accentColor":      "config_field.email_backup_accent_color",
	"backup.emailBackup.scope.mode":       "config_field.email_backup_scope_mode",
	"backup.emailBackup.scope.from":       "config_field.email_backup_scope_from",
	"backup.emailBackup.scope.to":         "config_field.email_backup_scope_to",
	"backup.emailBackup.maxPartSize":      "config_field.email_backup_max_part_size",
	"backup.emailBackup.fullFallbackDays": "config_field.email_backup_fallback_days",
}

func configFieldLabel(path string) string {
	if label, ok := configFieldLabels[path]; ok {
		return label
	}
	return strings.ReplaceAll(path, ".", " · ")
}
