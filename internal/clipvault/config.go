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
	Sensitive        SensitiveConfig `json:"sensitive"`
	Autostart        AutostartConfig `json:"autostart"`
	Security         SecurityConfig  `json:"security"`
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

// SecurityConfig 存储锁屏密码和二次密码的哈希值及盐。
// 密码以 SHA-256(salt + password) 形式存储，不保存明文。
type SecurityConfig struct {
	LockPasswordHash      string `json:"lockPasswordHash"`
	SecondaryPasswordHash string `json:"secondaryPasswordHash"`
	PasswordSalt          string `json:"passwordSalt"`
	LockSessionTimeout    int    `json:"lockSessionTimeout"`   // 锁屏会话超时(分钟)，0=浏览器会话cookie，N=N分钟
	SecondaryAuthTimeout  int    `json:"secondaryAuthTimeout"` // 二次认证缓存时长(分钟)，0=每次都校验，N=N分钟内免重复
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
		Sensitive: SensitiveConfig{
			CustomPatterns: nil,
		},
		Autostart: AutostartConfig{
			Enabled: false,
			AppName: "ClipVault",
		},
		Security: SecurityConfig{
			LockPasswordHash:      "",
			SecondaryPasswordHash: "",
			PasswordSalt:          "",
			LockSessionTimeout:    0,
			SecondaryAuthTimeout:  0,
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
	if cfg.Search.ListenAddr == "" {
		cfg.Search.ListenAddr = def.Search.ListenAddr
	}
	if cfg.Autostart.AppName == "" {
		cfg.Autostart.AppName = def.Autostart.AppName
	}
}
