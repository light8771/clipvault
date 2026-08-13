package clipvault

import (
	"fmt"
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
	Source   string
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
	logDir := defaultLogDirectory(exeDir)

	if dataDirOverride != "" {
		dataDir, err := resolveAbsoluteDataDirectory(dataDirOverride, "data directory override")
		if err != nil {
			return Environment{}, err
		}
		return Environment{
			DataDir: dataDir,
			LogDir:  logDir,
			Source:  "command",
		}, nil
	}

	if envDir := os.Getenv("CLIPVAULT_DATA_DIR"); envDir != "" {
		dataDir, err := resolveAbsoluteDataDirectory(envDir, "CLIPVAULT_DATA_DIR")
		if err != nil {
			return Environment{}, err
		}
		return Environment{
			DataDir: dataDir,
			LogDir:  logDir,
			Source:  "environment",
		}, nil
	}

	portableFlag := filepath.Join(exeDir, "portable.flag")
	if isPortableFlag(portableFlag) {
		return Environment{
			DataDir:  filepath.Join(exeDir, "data"),
			LogDir:   filepath.Join(exeDir, "logs"),
			Portable: true,
			Source:   "portable",
		}, nil
	}

	controlDir := environmentControlDir()
	if configuredDir, err := configuredDataDirectory(controlDir); err != nil {
		return Environment{}, err
	} else if configuredDir != "" {
		return Environment{DataDir: configuredDir, LogDir: logDir, Source: "configured"}, nil
	}

	local := os.Getenv("LOCALAPPDATA")
	if local != "" {
		return Environment{
			DataDir: filepath.Join(local, appDirName),
			LogDir:  logDir,
			Source:  "default",
		}, nil
	}

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
		Source:  "default",
	}, nil
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
const (
	configVersion          = 1
	maxBackupIntervalHours = 720
)
