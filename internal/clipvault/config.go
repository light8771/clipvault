package clipvault

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type Config struct {
	Version          int `json:"version"`
	MaxTextChars     int `json:"maxTextChars"`
	MaxFileDropPaths int `json:"maxFileDropPaths"`
	// RecordSourceInfo 记录每条剪贴板内容的来源进程（前台窗口法）。
	// 关闭后不再捕获任何来源信息，隐私敏感环境可关闭。
	RecordSourceInfo bool `json:"recordSourceInfo"`
	// Source 控制"来源应用"的展示、筛选与搜索；仅当顶层 RecordSourceInfo=true 时有意义。
	Source     SourceAppConfig  `json:"source"`
	Brand      BrandConfig      `json:"brand"`
	Background BackgroundConfig `json:"background"`
	// Appearance / Navigation / HistoryPage 按作用域拆分：
	// 全局视觉、全局导航栏、历史页专属布局互不污染。
	Appearance    AppearanceSettings  `json:"appearance"`
	Navigation    NavigationSettings  `json:"navigation"`
	HistoryPage   HistoryPageSettings `json:"historyPage"`
	Search        SearchConfig        `json:"search"`
	Sensitive     SensitiveConfig     `json:"sensitive"`
	Autostart     AutostartConfig     `json:"autostart"`
	Notifications NotificationConfig  `json:"notifications"`
	Security      SecurityConfig      `json:"security"`
	Hotkey        HotkeyConfig        `json:"hotkey"`
	SearchHotkey  HotkeyConfig        `json:"searchHotkey"`
	Image         ImageCaptureConfig  `json:"image"`
	Popup         SearchPopupConfig   `json:"popup"`
	Backup        BackupConfig        `json:"backup"`
	// DataAPI 本地/局域网数据 API：供本机或 LAN 中的 AI/Agent/脚本
	// 通过 HTTP + Bearer Token 只读访问 ClipVault 业务数据。
	DataAPI ExternalServiceConfig `json:"dataApi"`
	// MCP MCP 服务（Model Context Protocol，Streamable HTTP）：供 AI/MCP
	// 宿主通过 /mcp 端点 + Bearer Token 只读访问剪贴板数据。与数据接口
	// 共享 ExternalReadService、令牌与审计模型；令牌按 channel 隔离
	// （HTTP Token 不能调用 MCP，反之亦然）。
	MCP ExternalServiceConfig `json:"mcp"`
	// UI 界面偏好（当前语言等）。
	UI UIConfig `json:"ui"`
}

// UIConfig 是界面偏好配置。Language 取 "auto"（跟随系统 UI 语言）或
// i18n_languages 表中某个已启用语言的 code；存在性在 I18nService 层校验
// （config 纯函数不依赖数据库）。
type UIConfig struct {
	Language string `json:"language"`
}

// ExternalServiceConfig 是外部只读服务（数据接口 / MCP）的运行配置：
// 独立于主 Web 服务监听，可随时启停，仅对已激活用户开放，失败不影响主流程。
type ExternalServiceConfig struct {
	Enabled    bool   `json:"enabled"`
	ListenAddr string `json:"listenAddr"`
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

// UnmarshalJSON enforces the complete security-sensitive configuration
// contract. Newly introduced, non-security appearance fields may be defaulted
// explicitly for upgrade compatibility; missing security fields must never
// turn password protection off silently.
func (cfg *Config) UnmarshalJSON(data []byte) error {
	// 一次性数据结构迁移：旧版本把全局外观、导航栏与历史页布局混在
	// theme 对象里。检测到旧结构时拆分为 appearance/navigation/historyPage
	// 并删除 theme 键；新结构配置直接跳过。
	migrated, err := migrateLegacyThemeConfig(data)
	if err != nil {
		return err
	}
	data = migrated
	type configWire Config
	decoded := configWire(DefaultConfig())
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return err
	}
	top, err := requiredJSONObject(data, "config",
		"version", "maxTextChars", "maxFileDropPaths", "recordSourceInfo", "source",
		"brand", "background", "appearance", "navigation", "historyPage", "search", "sensitive", "autostart",
		"notifications", "security", "hotkey", "searchHotkey", "image", "popup", "backup", "dataApi", "mcp", "ui")
	if err != nil {
		return err
	}
	objects := []struct {
		field    string
		required []string
	}{
		{"brand", []string{"title", "logoContentType", "logoVersion", "faviconContentType", "faviconVersion"}},
		{"background", []string{"contentType", "version", "fit", "opacity", "overlay", "blur", "brightness", "contrast", "saturation", "scale", "positionX", "positionY"}},
		{"appearance", []string{"preset", "mode", "primary", "accent", "fontScale", "surfaceOpacity"}},
		{"navigation", []string{"sticky", "blur", "opacity", "divider"}},
		{"historyPage", []string{"layout", "columns", "forceColumns", "contentWidthMode", "contentWidth", "horizontalPadding", "cardGap", "previewLines", "fixedPreview"}},
		{"search", []string{"listenAddr", "timeZone"}},
		{"autostart", []string{"enabled", "appName"}},
		{"notifications", []string{"pauseResume", "pauseTitle", "pauseBody", "resumeTitle", "resumeBody"}},
		{"security", []string{"lockPasswordHash", "secondaryPasswordHash", "lockSessionTimeout", "secondaryAuthTimeout"}},
		{"hotkey", []string{"enabled", "modifiers", "key"}},
		{"searchHotkey", []string{"enabled", "modifiers", "key"}},
		{"image", []string{"enabled", "maxBytes"}},
		{"popup", []string{"hideDuplicates", "hideUnavailable", "keyOpen", "keyRestore", "keyPaste"}},
		{"dataApi", []string{"enabled", "listenAddr"}},
		{"mcp", []string{"enabled", "listenAddr"}},
		{"ui", []string{"language"}},
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

// AppearanceSettings 只负责全局视觉（皮肤、模式、颜色、字号、界面不透明度）。
// 任何页面专属布局（历史页列数、宽度、导航栏表现等）禁止放入本结构。
type AppearanceSettings struct {
	Preset         string `json:"preset"`
	Mode           string `json:"mode"`
	Primary        string `json:"primary"`
	Accent         string `json:"accent"`
	FontScale      int    `json:"fontScale"`
	SurfaceOpacity int    `json:"surfaceOpacity"`
}

// NavigationSettings 负责全局导航栏的视觉与行为。
// 导航栏的页面内容宽度不在此配置：它由各页面自己的 PageContainer 决定。
type NavigationSettings struct {
	Sticky  bool `json:"sticky"`  // 固定悬浮在页面顶部（否则随内容滚动）
	Blur    int  `json:"blur"`    // 毛玻璃模糊强度（0-32px），0 关闭
	Opacity int  `json:"opacity"` // 背景不透明度（0-100），-1 表示跟随界面不透明度
	Divider bool `json:"divider"` // 是否绘制底部分隔线
}

// HistoryPageSettings 集中管理历史页专属布局。
// Columns 取 0（自动）或 1-4；ForceColumns 为 true 时严格保持 Columns 列
// 且不设卡片最小宽度（用户选择强制模式即接受卡片被压缩）。
// ContentWidthMode 为 "auto"（充分使用窗口可用空间）或 "custom"（居中限宽），
// ContentWidth 仅在 custom 模式生效（auto 模式下作为切换后的备用值保留）。
type HistoryPageSettings struct {
	Layout            string `json:"layout"`            // "top-bottom"（默认）或 "left-right"
	Columns           int    `json:"columns"`           // 0=自动，1-4 指定列数
	ForceColumns      bool   `json:"forceColumns"`      // 强制按 Columns 显示，空间不足时不降级
	ContentWidthMode  string `json:"contentWidthMode"`  // "auto" | "custom"
	ContentWidth      int    `json:"contentWidth"`      // custom 模式的内容区宽度（720-3200px）
	HorizontalPadding int    `json:"horizontalPadding"` // 内容距窗口左右边缘（0-80px）
	CardGap           int    `json:"cardGap"`           // 卡片横向/纵向间距（0-32px）
	PreviewLines      int    `json:"previewLines"`      // 文本预览行数（1-20），0 不限制
	// FixedPreview 固定占位：内容不足 PreviewLines 行时预览区也保持满行数高度，
	// 卡片高度整齐一致；仅在 PreviewLines > 0 时有意义（normalize 会强制收敛）。
	FixedPreview bool `json:"fixedPreview"`
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

// migrateLegacyThemeConfig 把旧版混合在 theme 对象里的配置一次性迁移为
// appearance / navigation / historyPage 三个作用域对象，并删除 theme 键。
// 输入不含 theme 键时原样返回；theme 键存在但不是对象时返回错误。
// 迁移规则：
//   - appearance：preset/mode/primary/accent/fontScale/surfaceOpacity 原样映射
//   - navigation：navSticky→sticky、navBlur→blur、navOpacity→opacity、navBorder→divider
//   - historyPage：layout vertical→top-bottom、sidebar→left-right；
//     historyColumns "auto"→0、"1"-"4"→1-4；historyForceColumns→forceColumns；
//     previewLines→previewLines；新增字段取默认值
func migrateLegacyThemeConfig(data []byte) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return data, nil // 非对象 JSON 交给 decodeStrictJSON 报出规范错误
	}
	legacy, exists := raw["theme"]
	if !exists {
		return data, nil
	}
	var legacyTheme struct {
		Preset              string `json:"preset"`
		Mode                string `json:"mode"`
		Primary             string `json:"primary"`
		Accent              string `json:"accent"`
		FontScale           int    `json:"fontScale"`
		SurfaceOpacity      int    `json:"surfaceOpacity"`
		Layout              string `json:"layout"`
		HistoryColumns      string `json:"historyColumns"`
		HistoryForceColumns bool   `json:"historyForceColumns"`
		PreviewLines        int    `json:"previewLines"`
		NavOpacity          int    `json:"navOpacity"`
		NavBlur             int    `json:"navBlur"`
		NavBorder           bool   `json:"navBorder"`
		NavSticky           bool   `json:"navSticky"`
	}
	if err := json.Unmarshal(legacy, &legacyTheme); err != nil {
		return nil, fmt.Errorf("config.theme legacy migration failed: %w", err)
	}
	if _, exists := raw["appearance"]; !exists {
		raw["appearance"] = mustMarshalJSON(AppearanceSettings{
			Preset:         legacyTheme.Preset,
			Mode:           legacyTheme.Mode,
			Primary:        legacyTheme.Primary,
			Accent:         legacyTheme.Accent,
			FontScale:      legacyTheme.FontScale,
			SurfaceOpacity: legacyTheme.SurfaceOpacity,
		})
	}
	if _, exists := raw["navigation"]; !exists {
		raw["navigation"] = mustMarshalJSON(NavigationSettings{
			Sticky:  legacyTheme.NavSticky,
			Blur:    legacyTheme.NavBlur,
			Opacity: legacyTheme.NavOpacity,
			Divider: legacyTheme.NavBorder,
		})
	}
	if _, exists := raw["historyPage"]; !exists {
		def := DefaultConfig().HistoryPage
		columns := 0
		if n, err := strconv.Atoi(legacyTheme.HistoryColumns); err == nil && n >= 1 && n <= 4 {
			columns = n
		}
		previewLines := legacyTheme.PreviewLines
		if previewLines == 0 {
			previewLines = def.PreviewLines
		}
		// layout 取值迁移：vertical → top-bottom，sidebar → left-right。
		layout := def.Layout
		switch legacyTheme.Layout {
		case "sidebar":
			layout = "left-right"
		case "vertical":
			layout = "top-bottom"
		}
		raw["historyPage"] = mustMarshalJSON(HistoryPageSettings{
			Layout:            layout,
			Columns:           columns,
			ForceColumns:      columns > 0 && legacyTheme.HistoryForceColumns,
			ContentWidthMode:  def.ContentWidthMode,
			ContentWidth:      def.ContentWidth,
			HorizontalPadding: def.HorizontalPadding,
			CardGap:           def.CardGap,
			PreviewLines:      previewLines,
		})
	}
	delete(raw, "theme")
	return json.Marshal(raw)
}

func mustMarshalJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal config value: %v", err))
	}
	return data
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
