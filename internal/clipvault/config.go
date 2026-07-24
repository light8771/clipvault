package clipvault

import (
	"os"
	"path/filepath"
	"strings"
)

const appDirName = "ClipVault"

const AppVersion = "0.1.0"

// Environment 持有运行时路径信息（数据目录、日志目录）。
// 配置不再存储为 JSON 文件，而是持久化在数据库的 settings 表中。
type Environment struct {
	DataDir  string
	LogDir   string
	Portable bool
}

// NewEnvironment resolves the runtime environment with DataDir priority:
// 1. dataDirOverride (from --data-dir flag)
// 2. CLIPVAULT_DATA_DIR env var
// 3. portable.flag in exe directory
// 4. %LOCALAPPDATA%\ClipVault
// 5. %APPDATA%\ClipVault fallback
func NewEnvironment() (Environment, error) {
	return newEnvironment("")
}

func newEnvironment(dataDirOverride string) (Environment, error) {
	exePath, err := os.Executable()
	if err != nil {
		return Environment{}, err
	}
	exeDir := filepath.Dir(exePath)
	logDir := defaultLogDirectory(exeDir)

	// 1. --data-dir override
	if dataDirOverride != "" {
		return Environment{
			DataDir: dataDirOverride,
			LogDir:  logDir,
		}, nil
	}

	// 2. CLIPVAULT_DATA_DIR env var
	if envDir := os.Getenv("CLIPVAULT_DATA_DIR"); envDir != "" {
		return Environment{
			DataDir: envDir,
			LogDir:  logDir,
		}, nil
	}

	// 3. portable.flag in exe directory
	portableFlag := filepath.Join(exeDir, "portable.flag")
	if _, err := os.Stat(portableFlag); err == nil {
		return Environment{
			DataDir:  filepath.Join(exeDir, "data"),
			LogDir:   filepath.Join(exeDir, "logs"),
			Portable: true,
		}, nil
	}

	// 4. %LOCALAPPDATA%\ClipVault
	local := os.Getenv("LOCALAPPDATA")
	if local != "" {
		return Environment{
			DataDir: filepath.Join(local, appDirName),
			LogDir:  logDir,
		}, nil
	}

	// 5. %APPDATA%\ClipVault fallback
	roaming := os.Getenv("APPDATA")
	if roaming == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Environment{}, err
		}
		roaming = filepath.Join(home, "AppData", "Roaming")
	}
	return Environment{
		DataDir: filepath.Join(roaming, appDirName),
		LogDir:  logDir,
	}, nil
}

func defaultLogDirectory(exeDir string) string {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, appDirName, "logs")
	}
	if roaming := os.Getenv("APPDATA"); roaming != "" {
		return filepath.Join(roaming, appDirName, "logs")
	}
	return filepath.Join(exeDir, "logs")
}

// StartupErrorLogFile returns a writable per-user location for errors that
// occur before the normal daily logger can be initialized.
func StartupErrorLogFile() string {
	exe, err := os.Executable()
	if err != nil {
		return "startup-error.log"
	}
	return filepath.Join(defaultLogDirectory(filepath.Dir(exe)), "startup-error.log")
}

// Config 是应用的完整配置结构，通过 Web 界面修改，存储在数据库中。
type Config struct {
	MaxTextChars     int                `json:"maxTextChars"`
	MaxFileDropPaths int                `json:"maxFileDropPaths"`
	Brand            BrandConfig        `json:"brand"`
	Background       BackgroundConfig   `json:"background"`
	Theme            ThemeConfig        `json:"theme"`
	Search           SearchConfig       `json:"search"`
	Sensitive        SensitiveConfig    `json:"sensitive"`
	Autostart        AutostartConfig    `json:"autostart"`
	Notifications    NotificationConfig `json:"notifications"`
	Security         SecurityConfig     `json:"security"`
	Hotkey           HotkeyConfig       `json:"hotkey"`
	SearchHotkey     HotkeyConfig       `json:"searchHotkey"`
	Backup           BackupConfig       `json:"backup"`
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
	Preset  string `json:"preset"`
	Mode    string `json:"mode"`
	Primary string `json:"primary"`
	Accent  string `json:"accent"`
}

type SearchConfig struct {
	Enabled    bool   `json:"enabled"`
	ListenAddr string `json:"listenAddr"`
}

// SensitiveConfig 过滤规则：默认全部记录，只有匹配自定义正则的内容才会被过滤。
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

// SecurityConfig stores scrypt password hashes for lock and secondary authentication.
type SecurityConfig struct {
	LockPasswordHash      string `json:"lockPasswordHash"`
	SecondaryPasswordHash string `json:"secondaryPasswordHash"`
	LockSessionTimeout    int    `json:"lockSessionTimeout"`   // 锁屏会话超时(分钟)，0=浏览器会话cookie，N=N分钟
	SecondaryAuthTimeout  int    `json:"secondaryAuthTimeout"` // 二次认证缓存时长(分钟)，0=每次都校验，N=N分钟内免重复
}

// BackupConfig 存储定时备份配置。修改备份设置需要二次认证。
type BackupConfig struct {
	DirBackup   DirBackupConfig   `json:"dirBackup"`
	EmailBackup EmailBackupConfig `json:"emailBackup"`
}

// DirBackupConfig 定时备份到指定目录。
type DirBackupConfig struct {
	Enabled  bool   `json:"enabled"`
	Interval int    `json:"interval"` // 备份间隔(小时)
	Dir      string `json:"dir"`      // 目标目录路径
	Format   string `json:"format"`   // "json" 或 "cvexport"
	Password string `json:"password"` // cvexport 加密密码（仅 cvexport 格式使用）
}

// EmailBackupConfig 定时发送备份邮件到指定邮箱。
type EmailBackupConfig struct {
	Enabled  bool   `json:"enabled"`
	Interval int    `json:"interval"` // 备份间隔(小时)
	SMTPHost string `json:"smtpHost"` // SMTP 服务器地址
	SMTPPort int    `json:"smtpPort"` // SMTP 端口
	SMTPUser string `json:"smtpUser"` // SMTP 用户名
	SMTPPass string `json:"smtpPass"` // SMTP 密码（敏感，不向前端返回明文）
	From     string `json:"from"`     // 发件人邮箱
	To       string `json:"to"`       // 收件人邮箱
	Format   string `json:"format"`   // "json" 或 "cvexport"
	Password string `json:"password"` // cvexport 加密密码（仅 cvexport 格式使用）
}

func DefaultConfig() Config {
	return Config{
		Brand: BrandConfig{
			Title: "ClipVault",
		},
		Background: BackgroundConfig{
			Fit:        "cover",
			Opacity:    58,
			Overlay:    64,
			Brightness: 100,
			Contrast:   100,
			Saturation: 100,
			Scale:      100,
			PositionX:  50,
			PositionY:  50,
		},
		Theme: ThemeConfig{
			Preset: "mint",
			Mode:   "system",
		},
		MaxTextChars:     0, // 0 表示不限制
		MaxFileDropPaths: 0, // 0 表示不限制
		Search: SearchConfig{
			Enabled:    true,
			ListenAddr: "127.0.0.1:18765",
		},
		Sensitive: SensitiveConfig{
			CustomPatterns: nil,
		},
		Autostart: AutostartConfig{
			Enabled: false,
			AppName: "ClipVault",
		},
		Notifications: NotificationConfig{
			PauseResume: true,
			PauseTitle:  "ClipVault",
			PauseBody:   "剪贴板录制已暂停",
			ResumeTitle: "ClipVault",
			ResumeBody:  "剪贴板录制已恢复",
		},
		Security: SecurityConfig{
			LockPasswordHash:      "",
			SecondaryPasswordHash: "",
			LockSessionTimeout:    0,
			SecondaryAuthTimeout:  0,
		},
		Hotkey: HotkeyConfig{
			Enabled:   true,
			Modifiers: 0x0002 | 0x0001, // MOD_CONTROL | MOD_ALT
			Key:       0x50,            // VK_P
		},
		SearchHotkey: HotkeyConfig{
			Enabled:   true,
			Modifiers: 0x0002 | 0x0004, // MOD_CONTROL | MOD_SHIFT
			Key:       0x20,            // VK_SPACE
		},
		Backup: BackupConfig{
			DirBackup: DirBackupConfig{
				Interval: 24,
				Format:   "cvexport",
			},
			EmailBackup: EmailBackupConfig{
				Interval: 24,
				SMTPPort: 587,
				Format:   "cvexport",
			},
		},
	}
}

// DatabaseFile 返回 DuckDB 数据库文件的绝对路径。
func (e Environment) DatabaseFile() string {
	return filepath.Join(e.DataDir, "clipvault.duckdb")
}

// KeyFile 返回 DPAPI 保护的密钥文件路径。
func (e Environment) KeyFile() string {
	return filepath.Join(e.DataDir, "clipvault.key")
}

// RecoveryFile 返回恢复密码加密的密钥文件路径。
func (e Environment) RecoveryFile() string {
	return filepath.Join(e.DataDir, "clipvault.recovery")
}

// StateFile 返回应用状态文件路径。
func (e Environment) StateFile() string {
	return filepath.Join(e.DataDir, "clipvault.state")
}

// LogDirectory 返回日志目录路径。
func (e Environment) LogDirectory() string {
	return e.LogDir
}

// normalizeConfig 用默认值填充零值字段。
// MaxTextChars == 0 和 MaxFileDropPaths == 0 均为合法值（表示不限制），不做替换。
func normalizeConfig(cfg *Config) {
	def := DefaultConfig()
	normalizeBrand(&cfg.Brand, def.Brand)
	normalizeBackground(&cfg.Background, def.Background)
	normalizeTheme(&cfg.Theme, def.Theme)
	normalizeNotifications(&cfg.Notifications)
	if cfg.Search.ListenAddr == "" {
		cfg.Search.ListenAddr = def.Search.ListenAddr
	}
	if cfg.Autostart.AppName == "" {
		cfg.Autostart.AppName = def.Autostart.AppName
	}
	if cfg.Hotkey.Enabled && (cfg.Hotkey.Modifiers == 0 || cfg.Hotkey.Key == 0) {
		cfg.Hotkey = def.Hotkey
	}
	if cfg.SearchHotkey.Enabled && (cfg.SearchHotkey.Modifiers == 0 || cfg.SearchHotkey.Key == 0) {
		cfg.SearchHotkey = def.SearchHotkey
	}
	if cfg.Backup.DirBackup.Format == "" {
		cfg.Backup.DirBackup.Format = def.Backup.DirBackup.Format
	} else if cfg.Backup.DirBackup.Format != "cvexport" {
		cfg.Backup.DirBackup.Format = "json"
	}
	// 邮件会离开本机，始终要求使用加密导出格式，不允许明文 JSON。
	cfg.Backup.EmailBackup.Format = "cvexport"
}

func normalizeNotifications(notifications *NotificationConfig) {
	notifications.PauseTitle = truncateRunes(strings.TrimSpace(notifications.PauseTitle), 60)
	notifications.PauseBody = truncateRunes(strings.TrimSpace(notifications.PauseBody), 240)
	notifications.ResumeTitle = truncateRunes(strings.TrimSpace(notifications.ResumeTitle), 60)
	notifications.ResumeBody = truncateRunes(strings.TrimSpace(notifications.ResumeBody), 240)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func normalizeBrand(brand *BrandConfig, def BrandConfig) {
	brand.Title = strings.TrimSpace(brand.Title)
	if brand.Title == "" {
		brand.Title = def.Title
	}
	if title := []rune(brand.Title); len(title) > 60 {
		brand.Title = string(title[:60])
	}
	if !isBrandContentType(brand.LogoContentType, "logo") || brand.LogoVersion <= 0 {
		brand.LogoContentType = ""
		brand.LogoVersion = 0
	}
	if !isBrandContentType(brand.FaviconContentType, "favicon") || brand.FaviconVersion <= 0 {
		brand.FaviconContentType = ""
		brand.FaviconVersion = 0
	}
}

func normalizeBackground(background *BackgroundConfig, def BackgroundConfig) {
	if *background == (BackgroundConfig{}) {
		*background = def
		return
	}
	if !isBackgroundContentType(background.ContentType) || background.Version <= 0 {
		background.ContentType = ""
		background.Version = 0
	}
	background.Opacity = clampInt(background.Opacity, 0, 100)
	background.Overlay = clampInt(background.Overlay, 0, 90)
	background.Blur = clampInt(background.Blur, 0, 32)
	background.Brightness = clampInt(background.Brightness, 40, 160)
	background.Contrast = clampInt(background.Contrast, 40, 160)
	background.Saturation = clampInt(background.Saturation, 0, 200)
	background.Scale = clampInt(background.Scale, 25, 200)
	background.PositionX = clampInt(background.PositionX, 0, 100)
	background.PositionY = clampInt(background.PositionY, 0, 100)
	switch background.Fit {
	case "cover", "contain", "fill":
	default:
		background.Fit = def.Fit
	}
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func normalizeTheme(theme *ThemeConfig, def ThemeConfig) {
	switch theme.Preset {
	case "mint", "ocean", "sunrise", "graphite", "custom":
	default:
		theme.Preset = def.Preset
	}
	switch theme.Mode {
	case "system", "light", "dark":
	default:
		theme.Mode = def.Mode
	}
	if theme.Preset != "custom" {
		theme.Primary = ""
		theme.Accent = ""
		return
	}
	if !isHexColor(theme.Primary) {
		theme.Primary = "#14B8A6"
	}
	if !isHexColor(theme.Accent) {
		theme.Accent = "#F97316"
	}
}

func isHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, c := range value[1:] {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') && !(c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
