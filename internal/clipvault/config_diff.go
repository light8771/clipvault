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
			newMasked = "已更新"
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
		return "未设置"
	}
	return "已设置"
}

const maxConfigDiffValueRunes = 80

func formatConfigDiffValue(value any) string {
	var text string
	switch v := value.(type) {
	case nil:
		text = "空"
	case bool:
		if v {
			text = "开启"
		} else {
			text = "关闭"
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
			text = "（空）"
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

// configFieldLabels 把点分配置路径映射为界面展示用的中文名称；
// 未收录的路径回退为路径本身。
var configFieldLabels = map[string]string{
	"maxTextChars":                        "单条文本最大长度",
	"maxFileDropPaths":                    "文件拖放最大路径数",
	"recordSourceInfo":                    "记录来源应用",
	"source.showExe":                      "显示来源应用标签",
	"source.exeAliases":                   "来源应用别名",
	"source.exeWhitelist":                 "来源应用白名单",
	"brand.title":                         "站点标题",
	"background.fit":                      "背景填充方式",
	"background.opacity":                  "背景不透明度",
	"background.overlay":                  "背景遮罩浓度",
	"background.blur":                     "背景模糊",
	"background.brightness":               "背景亮度",
	"background.contrast":                 "背景对比度",
	"background.saturation":               "背景饱和度",
	"background.scale":                    "背景缩放",
	"background.positionX":                "背景水平位置",
	"background.positionY":                "背景垂直位置",
	"theme.preset":                        "皮肤",
	"theme.mode":                          "显示模式",
	"theme.primary":                       "主题主色",
	"theme.accent":                        "主题强调色",
	"theme.fontScale":                     "字体大小",
	"theme.surfaceOpacity":                "界面不透明度",
	"theme.layout":                        "历史页布局",
	"theme.previewLines":                  "内容预览行数",
	"theme.navOpacity":                    "导航栏不透明度",
	"theme.navBlur":                       "导航栏背景模糊",
	"theme.navBorder":                     "导航栏分隔线",
	"search.listenAddr":                   "Web 监听地址",
	"search.timeZone":                     "日期筛选时区",
	"dataApi.enabled":                     "数据接口开关",
	"dataApi.listenAddr":                  "数据接口监听地址",
	"mcp.enabled":                         "MCP 开关",
	"mcp.listenAddr":                      "MCP 监听地址",
	"sensitive.customPatterns":            "敏感内容过滤规则",
	"autostart.enabled":                   "开机自启",
	"autostart.appName":                   "自启应用名",
	"notifications.pauseResume":           "暂停/恢复通知",
	"notifications.pauseTitle":            "暂停通知标题",
	"notifications.pauseBody":             "暂停通知内容",
	"notifications.resumeTitle":           "恢复通知标题",
	"notifications.resumeBody":            "恢复通知内容",
	"security.lockPasswordHash":           "锁屏密码",
	"security.secondaryPasswordHash":      "二次验证密码",
	"security.lockSessionTimeout":         "锁屏会话超时",
	"security.secondaryAuthTimeout":       "二次认证缓存时长",
	"hotkey.enabled":                      "录制暂停快捷键",
	"hotkey.modifiers":                    "录制快捷键修饰键",
	"hotkey.key":                          "录制快捷键按键",
	"searchHotkey.enabled":                "搜索弹窗快捷键",
	"searchHotkey.modifiers":              "搜索快捷键修饰键",
	"searchHotkey.key":                    "搜索快捷键按键",
	"image.enabled":                       "图片捕获",
	"image.maxBytes":                      "图片最大字节数",
	"popup.hideDuplicates":                "弹窗过滤重复记录",
	"popup.hideUnavailable":               "弹窗过滤无内容记录",
	"popup.keyOpen":                       "弹窗快捷键-打开",
	"popup.keyRestore":                    "弹窗快捷键-恢复",
	"popup.keyPaste":                      "弹窗快捷键-粘贴",
	"backup.dirBackup.enabled":            "目录定时备份",
	"backup.dirBackup.runOnStartup":       "目录备份-启动时执行",
	"backup.dirBackup.interval":           "目录备份-间隔(小时)",
	"backup.dirBackup.dir":                "目录备份-目标目录",
	"backup.dirBackup.format":             "目录备份-内容格式",
	"backup.dirBackup.password":           "目录备份-加密密码",
	"backup.dirBackup.scope.mode":         "目录备份-范围模式",
	"backup.dirBackup.scope.from":         "目录备份-范围起始",
	"backup.dirBackup.scope.to":           "目录备份-范围结束",
	"backup.dirBackup.fullFallbackDays":   "目录备份-全量兜底周期(天)",
	"backup.emailBackup.enabled":          "邮件定时备份",
	"backup.emailBackup.runOnStartup":     "邮件备份-启动时执行",
	"backup.emailBackup.interval":         "邮件备份-间隔(小时)",
	"backup.emailBackup.smtpHost":         "邮件备份-SMTP 服务器",
	"backup.emailBackup.smtpPort":         "邮件备份-SMTP 端口",
	"backup.emailBackup.smtpUser":         "邮件备份-SMTP 用户名",
	"backup.emailBackup.smtpPass":         "邮件备份-SMTP 密码",
	"backup.emailBackup.from":             "邮件备份-发件人",
	"backup.emailBackup.to":               "邮件备份-收件人",
	"backup.emailBackup.format":           "邮件备份-内容格式",
	"backup.emailBackup.password":         "邮件备份-加密密码",
	"backup.emailBackup.subject":          "邮件备份-主题",
	"backup.emailBackup.body":             "邮件备份-正文",
	"backup.emailBackup.style":            "邮件备份-样式",
	"backup.emailBackup.accentColor":      "邮件备份-强调色",
	"backup.emailBackup.scope.mode":       "邮件备份-范围模式",
	"backup.emailBackup.scope.from":       "邮件备份-范围起始",
	"backup.emailBackup.scope.to":         "邮件备份-范围结束",
	"backup.emailBackup.maxPartSize":      "邮件备份-单封附件阈值",
	"backup.emailBackup.fullFallbackDays": "邮件备份-全量兜底周期(天)",
}

func configFieldLabel(path string) string {
	if label, ok := configFieldLabels[path]; ok {
		return label
	}
	return strings.ReplaceAll(path, ".", " · ")
}
