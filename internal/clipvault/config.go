package clipvault

import (
	"encoding/json"
	"fmt"
)

type Config struct {
	Version          int `json:"version"`
	MaxTextChars     int `json:"maxTextChars"`
	MaxFileDropPaths int `json:"maxFileDropPaths"`
	// RecordSourceInfo 记录每条剪贴板内容的来源进程（前台窗口法）。
	// 关闭后不再捕获任何来源信息，隐私敏感环境可关闭。
	RecordSourceInfo bool `json:"recordSourceInfo"`
	// Source 控制"来源应用"的展示、筛选与搜索；仅当顶层 RecordSourceInfo=true 时有意义。
	Source        SourceAppConfig    `json:"source"`
	Brand         BrandConfig        `json:"brand"`
	Background    BackgroundConfig   `json:"background"`
	Theme         ThemeConfig        `json:"theme"`
	Search        SearchConfig       `json:"search"`
	Sensitive     SensitiveConfig    `json:"sensitive"`
	Autostart     AutostartConfig    `json:"autostart"`
	Notifications NotificationConfig `json:"notifications"`
	Security      SecurityConfig     `json:"security"`
	Hotkey        HotkeyConfig       `json:"hotkey"`
	SearchHotkey  HotkeyConfig       `json:"searchHotkey"`
	Image         ImageCaptureConfig `json:"image"`
	Popup         SearchPopupConfig  `json:"popup"`
	Backup        BackupConfig       `json:"backup"`
}

// ImageCaptureConfig 图片捕获配置（Win+Shift+S 截图、浏览器 Copy Image、
// 微信/QQ 图片等）。图片统一转码为托管 PNG 文件保存。
type ImageCaptureConfig struct {
	Enabled bool `json:"enabled"`
	// MaxBytes 是单张图片（转码后 PNG）的最大字节数；0 = 32MiB 默认值。
	MaxBytes int `json:"maxBytes"`
}

// SearchPopupConfig 快速搜索弹窗行为配置。
// 显示过滤（HideDuplicates/HideUnavailable）对免费版同样生效：免费版始终
// 先在最新 100 条原始记录内过滤，不会向更早的记录补数量。
type SearchPopupConfig struct {
	HideDuplicates  bool `json:"hideDuplicates"`  // 弹窗默认过滤重复事件（is_duplicate）
	HideUnavailable bool `json:"hideUnavailable"` // 弹窗默认过滤无内容记录（content_available=false）
	// 三个动作的弹窗内快捷键，格式为 [Ctrl+][Shift+][Alt+]Enter。
	KeyOpen    string `json:"keyOpen"`    // 在浏览器中打开，默认 Enter
	KeyRestore string `json:"keyRestore"` // 仅恢复到 Windows 剪贴板，默认 Shift+Enter
	KeyPaste   string `json:"keyPaste"`   // 恢复并粘贴到之前窗口，默认 Ctrl+Enter
}

// UnmarshalJSON enforces the complete current configuration contract. This
// project uses fresh databases during development, so a missing field is
// corruption rather than an older shape to be defaulted. In particular,
// missing security fields must never turn password protection off silently.
func (cfg *Config) UnmarshalJSON(data []byte) error {
	type configWire Config
	var decoded configWire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return err
	}
	top, err := requiredJSONObject(data, "config",
		"version", "maxTextChars", "maxFileDropPaths", "recordSourceInfo", "source",
		"brand", "background", "theme", "search", "sensitive", "autostart",
		"notifications", "security", "hotkey", "searchHotkey", "image", "popup", "backup")
	if err != nil {
		return err
	}
	objects := []struct {
		field    string
		required []string
	}{
		{"brand", []string{"title", "logoContentType", "logoVersion", "faviconContentType", "faviconVersion"}},
		{"background", []string{"contentType", "version", "fit", "opacity", "overlay", "blur", "brightness", "contrast", "saturation", "scale", "positionX", "positionY"}},
		{"theme", []string{"preset", "mode", "primary", "accent", "fontScale", "surfaceOpacity"}},
		{"search", []string{"listenAddr", "timeZone"}},
		{"autostart", []string{"enabled", "appName"}},
		{"notifications", []string{"pauseResume", "pauseTitle", "pauseBody", "resumeTitle", "resumeBody"}},
		{"security", []string{"lockPasswordHash", "secondaryPasswordHash", "lockSessionTimeout", "secondaryAuthTimeout"}},
		{"hotkey", []string{"enabled", "modifiers", "key"}},
		{"searchHotkey", []string{"enabled", "modifiers", "key"}},
		{"image", []string{"enabled", "maxBytes"}},
		{"popup", []string{"hideDuplicates", "hideUnavailable", "keyOpen", "keyRestore", "keyPaste"}},
	}
	for _, object := range objects {
		if _, err := requiredJSONObject(top[object.field], "config."+object.field, object.required...); err != nil {
			return err
		}
	}
	if _, err := requiredJSONObjectAllowingNull(top["source"], "config.source", map[string]struct{}{"exeWhitelist": {}},
		"showExe", "exeAliases", "exeWhitelist"); err != nil {
		return err
	}
	if _, err := requiredJSONObjectAllowingNull(top["sensitive"], "config.sensitive", map[string]struct{}{"customPatterns": {}},
		"customPatterns"); err != nil {
		return err
	}
	backup, err := requiredJSONObject(top["backup"], "config.backup", "dirBackup", "emailBackup")
	if err != nil {
		return err
	}
	dir, err := requiredJSONObject(backup["dirBackup"], "config.backup.dirBackup",
		"enabled", "runOnStartup", "interval", "dir", "format", "password", "scope", "fullFallbackDays")
	if err != nil {
		return err
	}
	email, err := requiredJSONObject(backup["emailBackup"], "config.backup.emailBackup",
		"enabled", "runOnStartup", "interval", "smtpHost", "smtpPort", "smtpUser", "smtpPass",
		"from", "to", "format", "password", "subject", "body", "style", "accentColor",
		"scope", "maxPartSize", "fullFallbackDays")
	if err != nil {
		return err
	}
	if _, err := requiredJSONObject(dir["scope"], "config.backup.dirBackup.scope", "mode", "from", "to"); err != nil {
		return err
	}
	if _, err := requiredJSONObject(email["scope"], "config.backup.emailBackup.scope", "mode", "from", "to"); err != nil {
		return err
	}
	*cfg = Config(decoded)
	return nil
}

func requiredJSONObject(data []byte, path string, fields ...string) (map[string]json.RawMessage, error) {
	return requiredJSONObjectAllowingNull(data, path, nil, fields...)
}

func requiredJSONObjectAllowingNull(data []byte, path string, nullable map[string]struct{}, fields ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("%s must be an object: %w", path, err)
	}
	if object == nil {
		return nil, fmt.Errorf("%s must be an object", path)
	}
	for _, field := range fields {
		value, ok := object[field]
		_, allowNull := nullable[field]
		if !ok || len(value) == 0 || (!allowNull && string(value) == "null") {
			return nil, fmt.Errorf("%s.%s is required", path, field)
		}
	}
	return object, nil
}

// SourceAppConfig 控制"来源应用"的展示、筛选与搜索；仅当顶层 RecordSourceInfo=true 时有意义。
// 列表标签展示由 ShowExe 控制（默认 true）；下拉白名单为空/nil 表示显示全部；
// 别名键为数据库原始 source_exe_name（大小写敏感），别名值参与匹配时大小写不敏感。
type SourceAppConfig struct {
	ShowExe      bool              `json:"showExe"`      // 列表是否展示来源应用标签，默认 true
	ExeAliases   map[string]string `json:"exeAliases"`   // 原始 exe 名 -> 自定义显示名，键大小写敏感
	ExeWhitelist []string          `json:"exeWhitelist"` // 下拉白名单；nil/空 = 显示全部
}

// normalize 填充零值字段并收敛类型，保证持久化与读取口径一致：
// 别名映射 nil 归一为 {}，白名单空切片归一为 nil（"显示全部"语义）。
func (s *SourceAppConfig) normalize() {
	if s.ExeAliases == nil {
		s.ExeAliases = map[string]string{}
	}
	if len(s.ExeWhitelist) == 0 {
		s.ExeWhitelist = nil
	}
}

// BrandConfig stores the visible site identity. Image content itself is kept
// as validated files in the application data directory.
type BrandConfig struct {
	Title              string `json:"title"`
	LogoContentType    string `json:"logoContentType"`
	LogoVersion        int64  `json:"logoVersion,string"`
	FaviconContentType string `json:"faviconContentType"`
	FaviconVersion     int64  `json:"faviconVersion,string"`
}

// BackgroundConfig stores the selected background media and its visual controls.
// The media bytes are kept as a validated file in the application data directory.
type BackgroundConfig struct {
	ContentType string `json:"contentType"`
	Version     int64  `json:"version,string"`
	Fit         string `json:"fit"`
	Opacity     int    `json:"opacity"`
	Overlay     int    `json:"overlay"`
	Blur        int    `json:"blur"`
	Brightness  int    `json:"brightness"`
	Contrast    int    `json:"contrast"`
	Saturation  int    `json:"saturation"`
	Scale       int    `json:"scale"`
	PositionX   int    `json:"positionX"`
	PositionY   int    `json:"positionY"`
}

// ThemeConfig stores the selected UI skin. Custom primary and accent colors
// are used only when Preset is "custom" and are normalized before persistence.
type ThemeConfig struct {
	Preset         string `json:"preset"`
	Mode           string `json:"mode"`
	Primary        string `json:"primary"`
	Accent         string `json:"accent"`
	FontScale      int    `json:"fontScale"`
	SurfaceOpacity int    `json:"surfaceOpacity"`
}

type SearchConfig struct {
	ListenAddr string `json:"listenAddr"`
	TimeZone   string `json:"timeZone"` // "system" 或 IANA 时区（如 Asia/Shanghai）
}

// SensitiveConfig 敏感规则：匹配内容仍会加密保存，读取时受二次认证保护。
type SensitiveConfig struct {
	CustomPatterns []string `json:"customPatterns"`
}

type AutostartConfig struct {
	Enabled bool   `json:"enabled"`
	AppName string `json:"appName"`
}

// NotificationConfig controls local Windows notifications.
type NotificationConfig struct {
	PauseResume bool   `json:"pauseResume"`
	PauseTitle  string `json:"pauseTitle"`
	PauseBody   string `json:"pauseBody"`
	ResumeTitle string `json:"resumeTitle"`
	ResumeBody  string `json:"resumeBody"`
}

// HotkeyConfig 全局快捷键配置，用于暂停/恢复录制。
// Modifiers 使用 Win32 MOD_* 位掩码：MOD_ALT=1, MOD_CONTROL=2, MOD_SHIFT=4, MOD_WIN=8
// Key 使用 Win32 虚拟键码（如 'P'=0x50）。
// Modifiers=0 且 Key=0 表示快捷键已禁用。
type HotkeyConfig struct {
	Enabled   bool `json:"enabled"`
	Modifiers int  `json:"modifiers"`
	Key       int  `json:"key"`
}

const (
	validHotkeyModifierMask = 0x0001 | 0x0002 | 0x0004 | 0x0008 // ALT | CONTROL | SHIFT | WIN
	vkSpace                 = 0x20
	vkDigitFirst            = 0x30
	vkDigitLast             = 0x39
	vkLetterFirst           = 0x41
	vkLetterLast            = 0x5A
	vkFunctionFirst         = 0x70
	vkFunctionLast          = 0x87 // F1-F24
)

// SecurityConfig stores scrypt password hashes for lock and secondary authentication.
type SecurityConfig struct {
	LockPasswordHash      string `json:"lockPasswordHash"`
	SecondaryPasswordHash string `json:"secondaryPasswordHash"`
	LockSessionTimeout    int    `json:"lockSessionTimeout"`   // 锁屏会话超时(分钟)，0=浏览器会话cookie，N=N分钟
	SecondaryAuthTimeout  int    `json:"secondaryAuthTimeout"` // 二次认证缓存时长(分钟)，0=关闭浏览器后失效，N=N分钟内免重复
}

// BackupConfig 存储定时备份配置。修改备份设置需要二次认证。
type BackupConfig struct {
	DirBackup   DirBackupConfig   `json:"dirBackup"`
	EmailBackup EmailBackupConfig `json:"emailBackup"`
}

// DirBackupConfig 定时备份到指定目录。
type DirBackupConfig struct {
	Enabled          bool        `json:"enabled"`
	RunOnStartup     bool        `json:"runOnStartup"`
	Interval         int         `json:"interval"`         // 备份间隔(小时)
	Dir              string      `json:"dir"`              // 目标目录路径
	Format           string      `json:"format"`           // "json" / "xlsx" / "cvexport"（内容格式，文件始终是 .cvexport 加密容器）
	Password         string      `json:"password"`         // 加密密码（所有格式必填；json/xlsx 会先打进 zip 再整体加密）
	Scope            ExportScope `json:"scope"`            // 备份范围 full/range/incremental
	FullFallbackDays int         `json:"fullFallbackDays"` // 距上次全量超过该天数后自动全量兜底，0=不自动兜底，默认7
}

// EmailBackupConfig 定时发送备份邮件到指定邮箱。
type EmailBackupConfig struct {
	Enabled          bool        `json:"enabled"`
	RunOnStartup     bool        `json:"runOnStartup"`
	Interval         int         `json:"interval"` // 备份间隔(小时)
	SMTPHost         string      `json:"smtpHost"` // SMTP 服务器地址
	SMTPPort         int         `json:"smtpPort"` // SMTP 端口
	SMTPUser         string      `json:"smtpUser"` // SMTP 用户名
	SMTPPass         string      `json:"smtpPass"` // SMTP 密码（敏感，不向前端返回明文）
	From             string      `json:"from"`     // 发件人邮箱
	To               string      `json:"to"`       // 收件人邮箱
	Format           string      `json:"format"`   // "json" / "xlsx" / "cvexport"（内容格式，附件始终是 .cvexport 加密容器）
	Password         string      `json:"password"` // 加密密码（所有格式必填；json/xlsx 会先打进 zip 再整体加密）
	Subject          string      `json:"subject"`
	Body             string      `json:"body"`
	Style            string      `json:"style"`
	AccentColor      string      `json:"accentColor"`
	Scope            ExportScope `json:"scope"`            // 备份范围 full/range/incremental
	MaxPartSize      int         `json:"maxPartSize"`      // 单封附件阈值(字节)，默认20MB
	FullFallbackDays int         `json:"fullFallbackDays"` // 距上次全量超过该天数后自动全量兜底，0=不自动兜底，默认7
}
