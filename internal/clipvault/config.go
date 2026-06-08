package clipvault

import (
	"os"
	"path/filepath"
)

const appDirName = "ClipVault"

// Environment 持有运行时路径信息（数据目录、日志目录）。
// 配置不再存储为 JSON 文件，而是持久化在数据库的 settings 表中。
type Environment struct {
	DataDir string
	LogDir  string
}

// Config 是应用的完整配置结构，通过 Web 界面修改，存储在数据库中。
type Config struct {
	MaxTextChars     int             `json:"maxTextChars"`
	MaxFileDropPaths int             `json:"maxFileDropPaths"`
	Search           SearchConfig    `json:"search"`
	Hotkey           HotkeyConfig    `json:"hotkey"`
	Sensitive        SensitiveConfig `json:"sensitive"`
	Autostart        AutostartConfig `json:"autostart"`
}

type SearchConfig struct {
	Enabled    bool   `json:"enabled"`
	ListenAddr string `json:"listenAddr"`
}

type HotkeyConfig struct {
	Enabled   bool     `json:"enabled"`
	Modifiers []string `json:"modifiers"`
	Key       string   `json:"key"`
}

// SensitiveConfig 过滤规则：默认全部记录，只有匹配自定义正则的内容才会被过滤。
type SensitiveConfig struct {
	CustomPatterns []string `json:"customPatterns"`
}

type AutostartConfig struct {
	Enabled bool   `json:"enabled"`
	AppName string `json:"appName"`
}

func NewEnvironment() (Environment, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		roaming := os.Getenv("APPDATA")
		if roaming == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return Environment{}, err
			}
			roaming = filepath.Join(home, "AppData", "Roaming")
		}
		local = roaming
	}

	exePath, err := os.Executable()
	if err != nil {
		return Environment{}, err
	}
	exeDir := filepath.Dir(exePath)

	return Environment{
		DataDir: filepath.Join(local, appDirName),
		LogDir:  filepath.Join(exeDir, "logs"),
	}, nil
}

func DefaultConfig() Config {
	return Config{
		MaxTextChars:     0, // 0 表示不限制
		MaxFileDropPaths: 0, // 0 表示不限制
		Search: SearchConfig{
			Enabled:    true,
			ListenAddr: "127.0.0.1:18765",
		},
		Hotkey: HotkeyConfig{
			Enabled:   true,
			Modifiers: []string{"ctrl", "alt"},
			Key:       "P",
		},
		Sensitive: SensitiveConfig{
			CustomPatterns: nil,
		},
		Autostart: AutostartConfig{
			Enabled: false,
			AppName: "ClipVault",
		},
	}
}

// DatabaseFile 返回数据库文件的绝对路径。
func (e Environment) DatabaseFile() string {
	return filepath.Join(e.DataDir, "clipvault.db")
}

// LogDirectory 返回日志目录路径。
func (e Environment) LogDirectory() string {
	return e.LogDir
}

// normalizeConfig 用默认值填充零值字段。
// MaxTextChars == 0 和 MaxFileDropPaths == 0 均为合法值（表示不限制），不做替换。
func normalizeConfig(cfg *Config) {
	def := DefaultConfig()
	if cfg.Search.ListenAddr == "" {
		cfg.Search.ListenAddr = def.Search.ListenAddr
	}
	if cfg.Hotkey.Modifiers == nil {
		cfg.Hotkey.Modifiers = def.Hotkey.Modifiers
	}
	if cfg.Hotkey.Key == "" {
		cfg.Hotkey.Key = def.Hotkey.Key
	}
	if cfg.Autostart.AppName == "" {
		cfg.Autostart.AppName = def.Autostart.AppName
	}
}
