package clipvault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const appDirName = "ClipVault"

const AppVersion = "0.1.0"

// Environment 持有启动期已解析的运行时路径。数据和日志位置必须在
// 打开数据库前确定，因此使用用户级启动控制文件；其余应用设置存在 settings 表。
type Environment struct {
	DataDir    string
	LogDir     string
	LogWarning string
	Portable   bool
	Source     string
}

// NewEnvironment resolves the runtime environment with DataDir priority:
// 1. dataDirOverride (from --data-dir flag)
// 2. CLIPVAULT_DATA_DIR env var
// 3. portable.flag in exe directory
// 4. configured directory selected in settings
// 5. %LOCALAPPDATA%\ClipVault
// 6. %APPDATA%\ClipVault fallback
func NewEnvironment() (Environment, error) {
	return newEnvironment("")
}

func newEnvironment(dataDirOverride string) (Environment, error) {
	exePath, err := os.Executable()
	if err != nil {
		return Environment{}, err
	}
	exeDir := filepath.Dir(exePath)
	var env Environment
	if dataDirOverride != "" {
		dataDir, err := resolveAbsoluteDataDirectory(dataDirOverride, "data directory override")
		if err != nil {
			return Environment{}, err
		}
		env = Environment{DataDir: dataDir, Source: "command"}
	} else if envDir := os.Getenv("CLIPVAULT_DATA_DIR"); envDir != "" {
		dataDir, err := resolveAbsoluteDataDirectory(envDir, "CLIPVAULT_DATA_DIR")
		if err != nil {
			return Environment{}, err
		}
		env = Environment{DataDir: dataDir, Source: "environment"}
	} else if isPortableFlag(filepath.Join(exeDir, "portable.flag")) {
		env = Environment{
			DataDir:  filepath.Join(exeDir, "data"),
			LogDir:   filepath.Join(exeDir, "logs"),
			Portable: true,
			Source:   "portable",
		}
	} else {
		controlDir := environmentControlDir()
		configuredDir, err := configuredDataDirectory(controlDir)
		if err != nil {
			return Environment{}, err
		}
		if configuredDir != "" {
			env = Environment{DataDir: configuredDir, Source: "configured"}
		} else {
			env = Environment{DataDir: controlDir, Source: "default"}
		}
	}

	if env.Portable {
		return env, nil
	}
	env.LogDir = defaultLogDirectory(exeDir)
	configuredLogDir, err := configuredLogDirectory(environmentControlDir())
	if err != nil {
		env.LogWarning = err.Error()
	} else if configuredLogDir != "" {
		env.LogDir = configuredLogDir
	}
	return env, nil
}

func resolveAbsoluteDataDirectory(value, source string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", source)
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be an absolute path", source)
	}
	return filepath.Clean(value), nil
}

func isPortableFlag(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
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

func defaultLogDirectoryForCurrentExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return filepath.Join(environmentControlDir(), "logs")
	}
	return defaultLogDirectory(filepath.Dir(exe))
}

// StartupErrorLogFile returns a writable per-user location for errors that
// occur before the normal daily logger can be initialized.
func StartupErrorLogFile() string {
	exe, err := os.Executable()
	if err != nil {
		return "startup-error.log"
	}
	exeDir := filepath.Dir(exe)
	logDir := defaultLogDirectory(exeDir)
	if isPortableFlag(filepath.Join(exeDir, "portable.flag")) {
		logDir = filepath.Join(exeDir, "logs")
	} else if configuredDir, configErr := configuredLogDirectory(environmentControlDir()); configErr == nil && configuredDir != "" {
		logDir = configuredDir
	}
	return filepath.Join(logDir, "startup-error.log")
}

// Config 是应用的完整配置结构，通过 Web 界面修改，存储在数据库中。
const (
	configVersion          = 2
	maxBackupIntervalHours = 720
)
