package clipvault

import (
	"maps"
	"slices"
)

func DefaultConfig() Config {
	return Config{
		Version: configVersion,
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
			Preset:         "mint",
			Mode:           "system",
			FontScale:      100,
			SurfaceOpacity: 100,
			Layout:         "vertical",
			PreviewLines:   2,
			NavOpacity:     -1,
			NavBlur:        16,
			NavBorder:      true,
		},
		MaxTextChars:     0,
		MaxFileDropPaths: 0,
		RecordSourceInfo: true,
		Source: SourceAppConfig{
			ShowExe:      true,
			ExeAliases:   map[string]string{},
			ExeWhitelist: nil,
		},
		Search: SearchConfig{
			ListenAddr: "127.0.0.1:15678",
			TimeZone:   "system",
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
			Modifiers: 0x0002 | 0x0001,
			Key:       0x50,
		},
		SearchHotkey: HotkeyConfig{
			Enabled:   true,
			Modifiers: 0x0002 | 0x0004,
			Key:       0x20,
		},
		Image: ImageCaptureConfig{
			Enabled:  true,
			MaxBytes: defaultImageMaxBytes,
		},
		Popup: SearchPopupConfig{
			HideDuplicates:  false,
			HideUnavailable: false,
			KeyOpen:         "Enter",
			KeyRestore:      "Shift+Enter",
			KeyPaste:        "Ctrl+Enter",
		},
		Backup: BackupConfig{
			DirBackup: DirBackupConfig{
				Interval:         24,
				Format:           "cvexport",
				Scope:            ExportScope{Mode: "full"},
				FullFallbackDays: 7,
			},
			EmailBackup: EmailBackupConfig{
				Interval:         24,
				SMTPPort:         587,
				Format:           "cvexport",
				Subject:          "ClipVault 备份 - {time}",
				Body:             "已生成 {format} 备份文件：{filename}",
				Style:            "card",
				AccentColor:      "#16A34A",
				Scope:            ExportScope{Mode: "full"},
				MaxPartSize:      20 << 20,
				FullFallbackDays: 7,
			},
		},
		DataAPI: ExternalServiceConfig{
			Enabled:          false,
			ListenAddr:       "127.0.0.1:17839",
			SensitiveDefault: false,
		},
		MCP: ExternalServiceConfig{
			Enabled:          false,
			ListenAddr:       "127.0.0.1:17840",
			SensitiveDefault: false,
		},
	}
}

// cloneConfig returns an ownership-independent configuration value. Config is
// otherwise mostly a value type, but its maps and slices must not cross mutex or
// callback boundaries by reference.
func cloneConfig(cfg Config) Config {
	if cfg.Source.ExeAliases != nil {
		cfg.Source.ExeAliases = maps.Clone(cfg.Source.ExeAliases)
	}
	if cfg.Source.ExeWhitelist != nil {
		cfg.Source.ExeWhitelist = slices.Clone(cfg.Source.ExeWhitelist)
	}
	if cfg.Sensitive.CustomPatterns != nil {
		cfg.Sensitive.CustomPatterns = slices.Clone(cfg.Sensitive.CustomPatterns)
	}
	return cfg
}
