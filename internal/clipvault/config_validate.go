package clipvault

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
)

func validateConfig(cfg Config) error {
	if cfg.Version != configVersion {
		return fmt.Errorf("unsupported config version %d", cfg.Version)
	}
	if cfg.MaxTextChars < 0 || cfg.MaxTextChars > 100000 {
		return fmt.Errorf("maxTextChars must be between 0 and 100000")
	}
	if cfg.MaxFileDropPaths < 0 || cfg.MaxFileDropPaths > 1000 {
		return fmt.Errorf("maxFileDropPaths must be between 0 and 1000")
	}
	if _, _, err := validateListenAddr(cfg.Search.ListenAddr); err != nil {
		return err
	}
	if _, err := configuredLocation(cfg.Search.TimeZone); err != nil {
		return err
	}
	if err := validateSensitiveRules(cfg.Sensitive); err != nil {
		return fmt.Errorf("invalid sensitive rule: %w", err)
	}
	if cfg.Security.LockSessionTimeout < 0 || cfg.Security.LockSessionTimeout > 525600 ||
		cfg.Security.SecondaryAuthTimeout < 0 || cfg.Security.SecondaryAuthTimeout > 525600 {
		return fmt.Errorf("security timeouts must be between 0 and 525600 minutes")
	}
	if err := validateStoredPasswordHash(cfg.Security.LockPasswordHash); err != nil {
		return fmt.Errorf("invalid lock password hash: %w", err)
	}
	if err := validateStoredPasswordHash(cfg.Security.SecondaryPasswordHash); err != nil {
		return fmt.Errorf("invalid secondary password hash: %w", err)
	}
	listenHost, _, _ := validateListenAddr(cfg.Search.ListenAddr)
	if !isLoopbackHost(listenHost) && cfg.Security.LockPasswordHash == "" {
		return fmt.Errorf("lock password is required for LAN access")
	}
	if err := validateHotkeyConfig("hotkey", cfg.Hotkey); err != nil {
		return err
	}
	if err := validateHotkeyConfig("searchHotkey", cfg.SearchHotkey); err != nil {
		return err
	}
	if cfg.Hotkey.Enabled && cfg.SearchHotkey.Enabled &&
		cfg.Hotkey.Modifiers == cfg.SearchHotkey.Modifiers && cfg.Hotkey.Key == cfg.SearchHotkey.Key {
		return fmt.Errorf("hotkey and searchHotkey must not use the same key combination")
	}
	if cfg.Image.MaxBytes < minImageMaxBytes || cfg.Image.MaxBytes > maxImageMaxBytes {
		return fmt.Errorf("image.maxBytes must be between %d and %d", minImageMaxBytes, maxImageMaxBytes)
	}
	if err := validatePopupKeys(cfg.Popup); err != nil {
		return err
	}
	if cfg.Backup.DirBackup.Interval < 1 || cfg.Backup.DirBackup.Interval > maxBackupIntervalHours ||
		cfg.Backup.EmailBackup.Interval < 1 || cfg.Backup.EmailBackup.Interval > maxBackupIntervalHours {
		return fmt.Errorf("backup intervals must be between 1 and %d hours", maxBackupIntervalHours)
	}
	if !isBackupFormat(cfg.Backup.DirBackup.Format) || !isBackupFormat(cfg.Backup.EmailBackup.Format) {
		return fmt.Errorf("unsupported backup format")
	}
	if cfg.Backup.EmailBackup.MaxPartSize < 1<<20 || cfg.Backup.EmailBackup.MaxPartSize > 50<<20 {
		return fmt.Errorf("email maxPartSize must be between 1 and 50 MiB")
	}
	if cfg.Backup.DirBackup.FullFallbackDays < 0 || cfg.Backup.DirBackup.FullFallbackDays > 365 ||
		cfg.Backup.EmailBackup.FullFallbackDays < 0 || cfg.Backup.EmailBackup.FullFallbackDays > 365 {
		return fmt.Errorf("full fallback days must be between 0 and 365")
	}
	if err := validateConfiguredBackupScope(cfg.Backup.DirBackup.Scope, cfg.Search.TimeZone); err != nil {
		return fmt.Errorf("invalid directory backup scope: %w", err)
	}
	if err := validateConfiguredBackupScope(cfg.Backup.EmailBackup.Scope, cfg.Search.TimeZone); err != nil {
		return fmt.Errorf("invalid email backup scope: %w", err)
	}
	if err := validateUIConfig(cfg.UI); err != nil {
		return err
	}
	dataAPIHost, dataAPIPort, err := validateListenAddr(cfg.DataAPI.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid data API listen address: %w", err)
	}
	if cfg.DataAPI.Enabled {
		searchHost, searchPort, _ := validateListenAddr(cfg.Search.ListenAddr)
		samePort := dataAPIPort == searchPort && searchPort != 0
		if samePort && (listenHostUnspecified(dataAPIHost) || listenHostUnspecified(searchHost) || strings.EqualFold(dataAPIHost, searchHost)) {
			return fmt.Errorf("data API listen address conflicts with the Web listen address")
		}
	}
	mcpHost, mcpPort, err := validateListenAddr(cfg.MCP.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid MCP listen address: %w", err)
	}
	if cfg.MCP.Enabled {
		searchHost, searchPort, _ := validateListenAddr(cfg.Search.ListenAddr)
		samePort := mcpPort == searchPort && searchPort != 0
		if samePort && (listenHostUnspecified(mcpHost) || listenHostUnspecified(searchHost) || strings.EqualFold(mcpHost, searchHost)) {
			return fmt.Errorf("MCP listen address conflicts with the Web listen address")
		}
		if cfg.DataAPI.Enabled {
			dataHost, dataPort, _ := validateListenAddr(cfg.DataAPI.ListenAddr)
			sameData := mcpPort == dataPort && dataPort != 0
			if sameData && (listenHostUnspecified(mcpHost) || listenHostUnspecified(dataHost) || strings.EqualFold(mcpHost, dataHost)) {
				return fmt.Errorf("MCP listen address conflicts with the Data API listen address")
			}
		}
	}
	return nil
}

// validateUIConfig 校验界面语言取值："auto" 或 BCP 47 风格语言代码。
// 语言是否存在/启用由 I18nService 在运行时解析（此处不依赖数据库）。
func validateUIConfig(ui UIConfig) error {
	if ui.Language == I18nAuto {
		return nil
	}
	if err := validateI18nLanguageCode(ui.Language); err != nil {
		return fmt.Errorf("ui.language: %w", err)
	}
	return nil
}

// listenHostUnspecified 报告主机是否为通配地址（0.0.0.0 / ::），通配地址与同端口任何地址冲突。
func listenHostUnspecified(host string) bool {
	ip := net.ParseIP(strings.TrimSpace(strings.Trim(host, "[]")))
	return ip != nil && ip.IsUnspecified()
}

func validateHotkeyConfig(name string, hotkey HotkeyConfig) error {
	if hotkey.Modifiers < 0 || hotkey.Modifiers&^validHotkeyModifierMask != 0 {
		return fmt.Errorf("%s.modifiers contains unsupported Win32 modifier bits", name)
	}
	if hotkey.Key != 0 && !isSupportedHotkeyKey(hotkey.Key) {
		return fmt.Errorf("%s.key must be A-Z, 0-9, F1-F24, or Space", name)
	}
	if hotkey.Enabled && (hotkey.Modifiers == 0 || hotkey.Key == 0) {
		return fmt.Errorf("%s requires at least one modifier and a virtual-key code when enabled", name)
	}
	return nil
}

func isSupportedHotkeyKey(key int) bool {
	return key == vkSpace ||
		(key >= vkDigitFirst && key <= vkDigitLast) ||
		(key >= vkLetterFirst && key <= vkLetterLast) ||
		(key >= vkFunctionFirst && key <= vkFunctionLast)
}

func validateConfiguredBackupScope(scope ExportScope, timeZone string) error {
	switch scope.Mode {
	case "", "full":
		return validateExportScope(scope)
	case "incremental":
		if scope.From != "" || scope.To != "" {
			return fmt.Errorf("incremental backup scope does not accept from or to")
		}
		return nil
	case "range":
		_, err := normalizeExportRange(scope, timeZone)
		return err
	default:
		return fmt.Errorf("unsupported export mode %q", scope.Mode)
	}
}

// DatabaseFile 返回 DuckDB 数据库文件的绝对路径。
func (e Environment) DatabaseFile() string {
	return activeDatabaseFile(e.DataDir, databaseFileName)
}

// KeyFile 返回 DPAPI 保护的密钥文件路径。
func (e Environment) KeyFile() string {
	return activeDatabaseFile(e.DataDir, databaseKeyFileName)
}

// RecoveryFile 返回恢复密码加密的密钥文件路径。
func (e Environment) RecoveryFile() string {
	return activeDatabaseFile(e.DataDir, databaseRecoveryFileName)
}

// StateFile 返回应用状态文件路径。
func (e Environment) StateFile() string {
	return activeDatabaseFile(e.DataDir, databaseStateFileName)
}

// LogDirectory 返回日志目录路径。
func (e Environment) LogDirectory() string {
	return e.LogDir
}

// ImagesDirectory 返回托管图片（剪贴板图片转码 PNG）的根目录。
func (e Environment) ImagesDirectory() string {
	return filepath.Join(e.DataDir, "images")
}
