package clipvault

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
)

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
	if cfg.Search.TimeZone == "" {
		cfg.Search.TimeZone = def.Search.TimeZone
	}
	if cfg.Autostart.AppName == "" {
		cfg.Autostart.AppName = def.Autostart.AppName
	}
	if cfg.Backup.DirBackup.Format == "" {
		cfg.Backup.DirBackup.Format = def.Backup.DirBackup.Format
	}
	if cfg.Backup.EmailBackup.Format == "" {
		cfg.Backup.EmailBackup.Format = def.Backup.EmailBackup.Format
	}
	if cfg.Image.MaxBytes <= 0 {
		cfg.Image.MaxBytes = def.Image.MaxBytes
	}
	if cfg.Popup.KeyOpen == "" {
		cfg.Popup.KeyOpen = def.Popup.KeyOpen
	}
	if cfg.Popup.KeyRestore == "" {
		cfg.Popup.KeyRestore = def.Popup.KeyRestore
	}
	if cfg.Popup.KeyPaste == "" {
		cfg.Popup.KeyPaste = def.Popup.KeyPaste
	}
	if strings.TrimSpace(cfg.DataAPI.ListenAddr) == "" {
		cfg.DataAPI.ListenAddr = def.DataAPI.ListenAddr
	}
	if strings.TrimSpace(cfg.MCP.ListenAddr) == "" {
		cfg.MCP.ListenAddr = def.MCP.ListenAddr
	}
	cfg.UI.Language = strings.TrimSpace(cfg.UI.Language)
	if cfg.UI.Language == "" {
		cfg.UI.Language = def.UI.Language
	}
	cfg.Source.normalize()
}

func configuredLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "system" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid IANA time zone %q: %w", name, err)
	}
	return loc, nil
}

func isBackupFormat(format string) bool {
	switch format {
	case "cvexport", "xlsx", "json":
		return true
	default:
		return false
	}
}

func isEmailTemplateStyle(style string) bool {
	switch style {
	case "plain", "card", "accent":
		return true
	default:
		return false
	}
}

func emailTemplateErrors(backup EmailBackupConfig) []string {
	// 返回词条 key（backup.missing_*），由调用方翻译拼接。
	var errors []string
	if subject := strings.TrimSpace(backup.Subject); subject == "" || len([]rune(backup.Subject)) > 200 || strings.ContainsAny(backup.Subject, "\r\n") {
		errors = append(errors, "backup.missing_subject")
	}
	if len([]rune(backup.Body)) > 5000 {
		errors = append(errors, "backup.missing_body")
	}
	if !isEmailTemplateStyle(backup.Style) {
		errors = append(errors, "backup.missing_style")
	}
	if !isHexColor(backup.AccentColor) {
		errors = append(errors, "backup.missing_accent_color")
	}
	return errors
}

func smtpDeliveryErrors(backup EmailBackupConfig) []string {
	var errors []string
	if strings.TrimSpace(backup.SMTPHost) == "" {
		errors = append(errors, "backup.missing_smtp_host")
	}
	if backup.SMTPPort < 1 || backup.SMTPPort > 65535 {
		errors = append(errors, "backup.missing_smtp_port")
	}
	user := strings.TrimSpace(backup.SMTPUser)
	pass := backup.SMTPPass
	if (user == "") != (pass == "") {
		errors = append(errors, "backup.missing_smtp_user_pass_pair")
	}
	if user != "" {
		if _, err := mail.ParseAddress(user); err != nil {
			errors = append(errors, "backup.missing_smtp_user_email")
		}
	}
	if from := strings.TrimSpace(backup.From); from == "" {
		errors = append(errors, "backup.missing_from")
	} else if _, err := mail.ParseAddress(from); err != nil {
		errors = append(errors, "backup.missing_from")
	}
	if to := strings.TrimSpace(backup.To); to == "" {
		errors = append(errors, "backup.missing_to")
	} else if _, err := mail.ParseAddress(to); err != nil {
		errors = append(errors, "backup.missing_to")
	}
	return errors
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
	theme.FontScale = clampInt(theme.FontScale, 100, 150)
	theme.SurfaceOpacity = clampInt(theme.SurfaceOpacity, 0, 100)
	switch theme.Layout {
	case "vertical", "sidebar":
	default:
		theme.Layout = def.Layout
	}
	theme.PreviewLines = clampInt(theme.PreviewLines, 0, 20)
	theme.NavOpacity = clampInt(theme.NavOpacity, -1, 100)
	theme.NavBlur = clampInt(theme.NavBlur, 0, 32)
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
