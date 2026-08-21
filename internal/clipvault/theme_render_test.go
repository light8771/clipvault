package clipvault

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThemeBootCSSSystemMode(t *testing.T) {
	theme := ThemeConfig{Preset: "ocean", Mode: "system", FontScale: 100, SurfaceOpacity: 100}
	css := themeBootCSS(theme)
	if mode := themeFixedMode(theme); mode != "" {
		t.Fatalf("system mode should not pin data-theme-mode, got %q", mode)
	}
	for _, want := range []string{
		"--p:#02679b",                         // ocean light primary（对比度调整后，与前端一致）
		"--bg:#F0F9FF",                        // ocean light background
		"@media (prefers-color-scheme: dark)", // dark variants via media query
		"--p:#38BDF8",                         // ocean dark primary
	} {
		if !strings.Contains(css, want) {
			t.Errorf("system theme boot missing %q:\n%s", want, css)
		}
	}
	// CSP（style-src 'self'）会丢弃内联样式块，主题引导必须保持为纯 CSS。
	for _, forbidden := range []string{"<style", "<script", "</"} {
		if strings.Contains(css, forbidden) {
			t.Errorf("theme boot CSS must not contain markup %q:\n%s", forbidden, css)
		}
	}
}

func TestThemeModeScriptRespectsPinnedMode(t *testing.T) {
	data, err := fs.ReadFile(webFS, "web/theme-mode.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	// system 模式下由该脚本同步设置 data-theme-mode；固定模式已在 <html> 上输出，不得覆盖。
	for _, want := range []string{"dataset.themeMode", "prefers-color-scheme: dark", "if (!document.documentElement.dataset.themeMode)"} {
		if !strings.Contains(script, want) {
			t.Errorf("theme-mode.js missing %q", want)
		}
	}
}

func TestThemeBootCSSFixedModes(t *testing.T) {
	lightTheme := ThemeConfig{Preset: "mint", Mode: "light", FontScale: 100, SurfaceOpacity: 100}
	if mode := themeFixedMode(lightTheme); mode != "light" {
		t.Fatalf("light mode = %q, want light", mode)
	}
	light := themeBootCSS(lightTheme)
	if strings.Contains(light, "prefers-color-scheme") {
		t.Errorf("fixed light mode must not emit media query:\n%s", light)
	}
	if !strings.Contains(light, "--bg:#F0FDFB") {
		t.Errorf("fixed light mode missing mint light background:\n%s", light)
	}

	darkTheme := ThemeConfig{Preset: "sunrise", Mode: "dark", FontScale: 100, SurfaceOpacity: 100}
	if mode := themeFixedMode(darkTheme); mode != "dark" {
		t.Fatalf("dark mode = %q, want dark", mode)
	}
	dark := themeBootCSS(darkTheme)
	if !strings.Contains(dark, "--bg:#16100B") {
		t.Errorf("fixed dark mode missing sunrise dark background:\n%s", dark)
	}
	if strings.Contains(dark, "--bg:#FFF7ED") {
		t.Errorf("fixed dark mode must not contain light palette:\n%s", dark)
	}
}

func TestThemeBootCSSSurfaceOpacityAndFontScale(t *testing.T) {
	css := themeBootCSS(ThemeConfig{Preset: "mint", Mode: "light", FontScale: 125, SurfaceOpacity: 72, NavOpacity: -1})
	for _, want := range []string{"--card-rgba:rgba(255,255,255,0.72)", "--nav-bg:rgba(240,253,251,0.72)", "--font-scale:1.25"} {
		if !strings.Contains(css, want) {
			t.Errorf("theme boot missing %q:\n%s", want, css)
		}
	}

	css = themeBootCSS(ThemeConfig{Preset: "mint", Mode: "light", FontScale: 0, SurfaceOpacity: 0})
	if !strings.Contains(css, "--font-scale:1") || strings.Contains(css, "--font-scale:1.0") {
		t.Errorf("zero font scale should fall back to 1:\n%s", css)
	}
	if !strings.Contains(css, "--card-rgba:rgba(255,255,255,0)") {
		t.Errorf("zero surface opacity means fully transparent:\n%s", css)
	}
}

func TestThemeBootCSSCustomPreset(t *testing.T) {
	theme := ThemeConfig{Preset: "custom", Mode: "light", Primary: "#2563EB", Accent: "#F59E0B", FontScale: 100, SurfaceOpacity: 100}
	css := themeBootCSS(theme)
	// 自定义皮肤表面色沿用 graphite，品牌色来自用户配置（经对比度调整）。
	for _, want := range []string{"--p:#225ad6", "--bg:#F8FAFC", "--card:#FFFFFF"} {
		if !strings.Contains(css, want) {
			t.Errorf("custom theme boot missing %q:\n%s", want, css)
		}
	}
}

func TestThemeBootCSSNavAndPreviewOptions(t *testing.T) {
	// 默认：导航栏跟随界面不透明度，16px 毛玻璃，带分隔线，吸附跟随滚动，文本预览 2 行
	follow := themeBootCSS(ThemeConfig{Preset: "mint", Mode: "light", FontScale: 100, SurfaceOpacity: 60,
		NavOpacity: -1, NavBlur: 16, NavBorder: true, NavSticky: true, PreviewLines: 2})
	for _, want := range []string{
		"--nav-bg:rgba(240,253,251,0.6)",
		"--nav-backdrop:blur(16px) saturate(180%)",
		"--nav-border-bottom:1px solid ",
		"--nav-position:sticky",
		"--preview-max-h:3.4em",
		"--on-accent:",
	} {
		if !strings.Contains(follow, want) {
			t.Errorf("default nav/preview boot missing %q:\n%s", want, follow)
		}
	}

	// 显式配置：独立不透明度、关闭模糊与分隔线、导航栏随页面滚动、预览不限制
	explicit := themeBootCSS(ThemeConfig{Preset: "mint", Mode: "light", FontScale: 100, SurfaceOpacity: 60,
		NavOpacity: 35, NavBlur: 0, NavBorder: false, NavSticky: false, PreviewLines: 0})
	for _, want := range []string{
		"--nav-bg:rgba(240,253,251,0.35)",
		"--nav-backdrop:none",
		"--nav-border-bottom:none",
		"--nav-position:static",
		"--preview-max-h:none",
	} {
		if !strings.Contains(explicit, want) {
			t.Errorf("explicit nav/preview boot missing %q:\n%s", want, explicit)
		}
	}
}

func TestThemeRootVarsMatchFrontendApplyTheme(t *testing.T) {
	// 与 appearance-theme.js applyTheme 写入的变量集合保持一致。
	vars := themeRootVars(ThemeConfig{Preset: "mint", Mode: "light", FontScale: 100, SurfaceOpacity: 100}, false)
	for _, name := range []string{
		"--p", "--p2", "--on-p", "--on-p2", "--accent", "--on-accent", "--bg", "--bg2", "--card",
		"--card-border", "--card-rgba", "--nav-bg", "--nav-backdrop", "--nav-border-bottom",
		"--nav-position", "--preview-max-h", "--tx", "--sub", "--muted",
		"--p-dim", "--p-glow", "--p-border", "--accent-dim", "--accent-glow", "--accent-border",
		"--shadow-md", "--shadow-lg",
	} {
		if !strings.Contains(vars, name+":") {
			t.Errorf("theme root vars missing %s", name)
		}
	}
	// mint 浅色主色需满足有色表面上的对比度，与前端同样被调整为 #0c7267。
	if !strings.Contains(vars, "--p:#0c7267") {
		t.Errorf("mint light primary should match frontend adjustment:\n%s", vars)
	}
}

// TestThemeColorsMatchFrontend 的期望值由 Node 直接运行 web/theme-utils.js 计算得出，
// 保证服务端内联的主题与前端 applyTheme 的运行时结果完全一致。
func TestThemeColorsMatchFrontend(t *testing.T) {
	tests := []struct {
		name                  string
		theme                 ThemeConfig
		dark                  bool
		p, p2, accent, border string
		onP, onP2             string
	}{
		{"mint light", ThemeConfig{Preset: "mint"}, false, "#0c7267", "#0a7269", "#9d480e", "rgba(12,114,103,0.2)", "#FFFFFF", "#FFFFFF"},
		{"mint dark", ThemeConfig{Preset: "mint"}, true, "#2DD4BF", "#14B8A6", "#FB923C", "rgba(45,212,191,0.18)", "#000000", "#000000"},
		{"ocean light", ThemeConfig{Preset: "ocean"}, false, "#02679b", "#03669c", "#2055ca", "rgba(2,103,155,0.2)", "#FFFFFF", "#FFFFFF"},
		{"ocean dark", ThemeConfig{Preset: "ocean"}, true, "#38BDF8", "#0EA5E9", "#60A5FA", "rgba(56,189,248,0.18)", "#000000", "#000000"},
		{"sunrise light", ThemeConfig{Preset: "sunrise"}, false, "#a83f09", "#ad3a0b", "#8d4d04", "rgba(168,63,9,0.2)", "#FFFFFF", "#FFFFFF"},
		{"sunrise dark", ThemeConfig{Preset: "sunrise"}, true, "#FB923C", "#F97316", "#FBBF24", "rgba(251,146,60,0.18)", "#000000", "#000000"},
		{"graphite light", ThemeConfig{Preset: "graphite"}, false, "#475569", "#334155", "#0e6a63", "rgba(71,85,105,0.2)", "#FFFFFF", "#FFFFFF"},
		{"graphite dark", ThemeConfig{Preset: "graphite"}, true, "#94A3B8", "#8b97a8", "#2DD4BF", "rgba(148,163,184,0.18)", "#000000", "#000000"},
		{"custom light", ThemeConfig{Preset: "custom", Primary: "#2563EB", Accent: "#F59E0B"}, false, "#225ad6", "#1e4fbc", "#845506", "rgba(34,90,214,0.2)", "#FFFFFF", "#FFFFFF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			colors := resolveThemeColors(tt.theme, tt.dark)
			if colors.P != tt.p || colors.P2 != tt.p2 || colors.Accent != tt.accent || colors.Border != tt.border {
				t.Errorf("colors = (%s, %s, %s, %s), want (%s, %s, %s, %s)",
					colors.P, colors.P2, colors.Accent, colors.Border, tt.p, tt.p2, tt.accent, tt.border)
			}
			if onP := themeTextColorFor(colors.P); onP != tt.onP {
				t.Errorf("on-p = %s, want %s", onP, tt.onP)
			}
			if onP2 := themeTextColorFor(colors.P2); onP2 != tt.onP2 {
				t.Errorf("on-p2 = %s, want %s", onP2, tt.onP2)
			}
		})
	}
}

func TestApplicationPageIncludesThemeBoot(t *testing.T) {
	body := renderApplicationPage(t, "/")
	headEnd := strings.Index(body, "</head>")
	if headEnd < 0 {
		t.Fatal("rendered page missing </head>")
	}
	head := body[:headEnd]
	for _, want := range []string{`href="/theme-boot.css"`, `src="/theme-mode.js"`} {
		if !strings.Contains(head, want) {
			t.Errorf("rendered page <head> missing %s", want)
		}
	}
	// theme-boot.css 必须最后加载，才能以相同优先级覆盖 tokens.css 的默认变量。
	if strings.Index(head, `href="/theme-boot.css"`) < strings.Index(head, `href="/tokens.css"`) {
		t.Errorf("theme-boot.css must load after tokens.css")
	}
	// 内联样式/脚本会被 CSP 丢弃，页面不得再包含 theme boot 的内联块。
	if strings.Contains(head, "<style") || strings.Contains(body, "<script>") {
		t.Errorf("rendered page must not inline theme boot markup (blocked by CSP)")
	}
}

func TestThemeBootCSSHandlerServesCurrentTheme(t *testing.T) {
	manager := &ConfigManager{cfg: func() Config {
		cfg := DefaultConfig()
		cfg.Theme = ThemeConfig{Preset: "sunrise", Mode: "dark", FontScale: 100, SurfaceOpacity: 100}
		return cfg
	}()}
	server := &SearchServer{cfgMgr: manager}
	result := httptest.NewRecorder()
	server.handleThemeBootCSS(result, httptest.NewRequest(http.MethodGet, "http://clipvault.local/theme-boot.css", nil))
	if result.Code != http.StatusOK {
		t.Fatalf("status = %d", result.Code)
	}
	if ct := result.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("content type = %q, want text/css", ct)
	}
	if cc := result.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("cache control = %q, want no-cache", cc)
	}
	body := result.Body.String()
	if !strings.Contains(body, "--bg:#16100B") || strings.Contains(body, "--bg:#FFF7ED") {
		t.Errorf("theme boot CSS does not reflect configured sunrise dark theme:\n%s", body)
	}
}
