package clipvault

// 本文件把 web/app/appearance-theme.js 的主题计算逻辑移植到服务端，
// 通过 /theme-boot.css 外部样式表把主题 CSS 变量随首帧下发（CSP 禁止内联
// 样式），避免页面先以默认皮肤绘制、等 JS 拉取配置后再切换造成的闪烁。
// 修改任一一侧时需保持同步。

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type themePaletteColors struct {
	P, P2, Accent, Bg, Bg2, Card, Border, Tx, Sub, Muted string
}

// themePresetPalettes 与 appearance-theme.js 中的 THEME_PALETTES 保持一致。
var themePresetPalettes = map[string]map[string]themePaletteColors{
	"mint": {
		"light": {P: "#14B8A6", P2: "#0D9488", Accent: "#F97316", Bg: "#F0FDFB", Bg2: "#E6F9F5", Card: "#FFFFFF", Border: "rgba(20,184,166,.18)", Tx: "#0F2825", Sub: "#3A6863", Muted: "#5C8C87"},
		"dark":  {P: "#2DD4BF", P2: "#14B8A6", Accent: "#FB923C", Bg: "#070C14", Bg2: "#0C1520", Card: "#101820", Border: "rgba(45,212,191,.14)", Tx: "#E8F6F3", Sub: "#9AD3CC", Muted: "#70AAA4"},
	},
	"ocean": {
		"light": {P: "#0284C7", P2: "#0369A1", Accent: "#2563EB", Bg: "#F0F9FF", Bg2: "#E0F2FE", Card: "#FFFFFF", Border: "rgba(2,132,199,.18)", Tx: "#102A43", Sub: "#3D5A75", Muted: "#6B849C"},
		"dark":  {P: "#38BDF8", P2: "#0EA5E9", Accent: "#60A5FA", Bg: "#07111F", Bg2: "#0B1B30", Card: "#102038", Border: "rgba(56,189,248,.16)", Tx: "#E8F4FF", Sub: "#A7C7E7", Muted: "#7FA5CB"},
	},
	"sunrise": {
		"light": {P: "#EA580C", P2: "#C2410C", Accent: "#D97706", Bg: "#FFF7ED", Bg2: "#FFEDD5", Card: "#FFFFFF", Border: "rgba(234,88,12,.18)", Tx: "#342012", Sub: "#72513B", Muted: "#99735B"},
		"dark":  {P: "#FB923C", P2: "#F97316", Accent: "#FBBF24", Bg: "#16100B", Bg2: "#24170E", Card: "#2B1C10", Border: "rgba(251,146,60,.16)", Tx: "#FFF2E5", Sub: "#E6B58A", Muted: "#B98D6B"},
	},
	"graphite": {
		"light": {P: "#475569", P2: "#334155", Accent: "#0F766E", Bg: "#F8FAFC", Bg2: "#EEF2F6", Card: "#FFFFFF", Border: "rgba(71,85,105,.18)", Tx: "#1E293B", Sub: "#475569", Muted: "#748196"},
		"dark":  {P: "#94A3B8", P2: "#64748B", Accent: "#2DD4BF", Bg: "#0B1018", Bg2: "#151D28", Card: "#182230", Border: "rgba(148,163,184,.16)", Tx: "#EDF2F7", Sub: "#B5C0CF", Muted: "#8A9AAD"},
	},
}

func isThemeHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func validThemeHex(value, fallback string) string {
	if len(value) == 7 && value[0] == '#' {
		valid := true
		for i := 1; i < 7; i++ {
			if !isThemeHexDigit(value[i]) {
				valid = false
				break
			}
		}
		if valid {
			return value
		}
	}
	return fallback
}

func themeRGB(hex string) [3]int {
	value := validThemeHex(hex, "#000000")
	var out [3]int
	for i, offset := range []int{1, 3, 5} {
		v, _ := strconv.ParseUint(value[offset:offset+2], 16, 8)
		out[i] = int(v)
	}
	return out
}

func blendThemeHex(from, to string, amount float64) string {
	ratio := math.Max(0, math.Min(1, amount))
	a, b := themeRGB(from), themeRGB(to)
	var sb strings.Builder
	sb.WriteByte('#')
	for i := 0; i < 3; i++ {
		channel := int(math.Round(float64(a[i])*(1-ratio) + float64(b[i])*ratio))
		fmt.Fprintf(&sb, "%02x", channel)
	}
	return sb.String()
}

func themeHexToRgba(hex string, alpha float64) string {
	c := themeRGB(hex)
	return fmt.Sprintf("rgba(%d,%d,%d,%s)", c[0], c[1], c[2], strconv.FormatFloat(alpha, 'f', -1, 64))
}

func themeRelativeLuminance(hex string) float64 {
	channels := themeRGB(hex)
	weights := [3]float64{0.2126, 0.7152, 0.0722}
	sum := 0.0
	for i, channel := range channels {
		value := float64(channel) / 255
		if value <= 0.04045 {
			value /= 12.92
		} else {
			value = math.Pow((value+0.055)/1.055, 2.4)
		}
		sum += weights[i] * value
	}
	return sum
}

func themeContrastRatio(first, second string) float64 {
	l1, l2 := themeRelativeLuminance(first), themeRelativeLuminance(second)
	if l2 > l1 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func themeTextColorFor(background string) string {
	if themeContrastRatio("#FFFFFF", background) >= themeContrastRatio("#000000", background) {
		return "#FFFFFF"
	}
	return "#000000"
}

func ensureThemeContrastOnSurfaces(color string, surfaces []string, minimum float64, tintAlphas []float64) string {
	base := validThemeHex(color, "#14B8A6")
	var candidates []string
	for _, surface := range surfaces {
		if v := validThemeHex(surface, ""); v != "" {
			candidates = append(candidates, v)
		}
	}
	if len(candidates) == 0 {
		candidates = []string{"#FFFFFF"}
	}
	if minimum <= 0 {
		minimum = 4.5
	}
	meets := func(candidate string) bool {
		for _, surface := range candidates {
			if themeContrastRatio(candidate, surface) < minimum {
				return false
			}
			for _, alpha := range tintAlphas {
				if themeContrastRatio(candidate, blendThemeHex(surface, candidate, alpha)) < minimum {
					return false
				}
			}
		}
		return true
	}
	if meets(base) {
		return base
	}
	average := 0.0
	for _, candidate := range candidates {
		average += themeRelativeLuminance(candidate)
	}
	average /= float64(len(candidates))
	target := "#FFFFFF"
	if average > 0.5 {
		target = "#000000"
	}
	for step := 1; step <= 100; step++ {
		candidate := blendThemeHex(base, target, float64(step)/100)
		if meets(candidate) {
			return candidate
		}
	}
	return target
}

type themeResolvedColors struct {
	P, P2, Accent, Bg, Bg2, Card, Border, Tx, Sub, Muted string
}

func themeBorderAlpha(dark bool) float64 {
	if dark {
		return .18
	}
	return .2
}

// readableThemePalette 对应前端的 readablePalette：在保持色相的同时保证对比度。
func readableThemePalette(p themePaletteColors, dark bool) themeResolvedColors {
	surfaces := []string{p.Card, p.Bg, p.Bg2}
	primary := ensureThemeContrastOnSurfaces(p.P, surfaces, 4.5, []float64{.12})
	hover := ensureThemeContrastOnSurfaces(p.P2, surfaces, 4.5, []float64{.12})
	accent := ensureThemeContrastOnSurfaces(p.Accent, surfaces, 4.5, []float64{.1, .16})
	return themeResolvedColors{
		P: primary, P2: hover, Accent: accent,
		Bg: p.Bg, Bg2: p.Bg2, Card: p.Card,
		Border: themeHexToRgba(primary, themeBorderAlpha(dark)),
		Tx:     p.Tx, Sub: p.Sub, Muted: p.Muted,
	}
}

// customThemePalette 对应前端的 customPalette：自定义皮肤只改品牌色，表面色沿用 graphite。
func customThemePalette(theme ThemeConfig, dark bool) themeResolvedColors {
	neutral := themePresetPalettes["graphite"][themeModeKey(dark)]
	surfaces := []string{neutral.Card, neutral.Bg, neutral.Bg2}
	primary := ensureThemeContrastOnSurfaces(validThemeHex(theme.Primary, "#14B8A6"), surfaces, 4.5, []float64{.12})
	blendTarget := "#000000"
	if dark {
		blendTarget = "#FFFFFF"
	}
	hover := ensureThemeContrastOnSurfaces(blendThemeHex(primary, blendTarget, .12), surfaces, 4.5, []float64{.12})
	accent := ensureThemeContrastOnSurfaces(validThemeHex(theme.Accent, "#F97316"), surfaces, 4.5, []float64{.1, .16})
	return themeResolvedColors{
		P: primary, P2: hover, Accent: accent,
		Bg: neutral.Bg, Bg2: neutral.Bg2, Card: neutral.Card,
		Border: themeHexToRgba(primary, themeBorderAlpha(dark)),
		Tx:     neutral.Tx, Sub: neutral.Sub, Muted: neutral.Muted,
	}
}

func themeModeKey(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}

func resolveThemeColors(theme ThemeConfig, dark bool) themeResolvedColors {
	if theme.Preset == "custom" {
		return customThemePalette(theme, dark)
	}
	modes, ok := themePresetPalettes[theme.Preset]
	if !ok {
		modes = themePresetPalettes["mint"]
	}
	return readableThemePalette(modes[themeModeKey(dark)], dark)
}

func themeFontScaleValue(theme ThemeConfig) string {
	scale := theme.FontScale
	if scale == 0 {
		scale = 100
	}
	scale = max(100, min(150, scale))
	return strconv.FormatFloat(float64(scale)/100, 'f', -1, 64)
}

func themeSurfaceAlpha(theme ThemeConfig) float64 {
	opacity := theme.SurfaceOpacity
	opacity = max(0, min(100, opacity))
	return float64(opacity) / 100
}

// themeNavAlpha 解析导航栏背景不透明度：NavOpacity 为负表示跟随界面不透明度。
func themeNavAlpha(theme ThemeConfig) float64 {
	if theme.NavOpacity < 0 {
		return themeSurfaceAlpha(theme)
	}
	return float64(max(0, min(100, theme.NavOpacity))) / 100
}

// themeNavBackdrop 生成导航栏 backdrop-filter 值，0 表示关闭模糊。
func themeNavBackdrop(theme ThemeConfig) string {
	blur := max(0, min(32, theme.NavBlur))
	if blur == 0 {
		return "none"
	}
	return "blur(" + strconv.Itoa(blur) + "px) saturate(180%)"
}

// themeNavBorderBottom 生成导航栏底部分隔线，关闭时为 none。
func themeNavBorderBottom(theme ThemeConfig, border string) string {
	if !theme.NavBorder {
		return "none"
	}
	return "1px solid " + border
}

// themeNavPosition 返回导航栏 position 值：NavSticky 为 true 时固定悬浮顶部（sticky），否则随页面滚动（static）。
func themeNavPosition(theme ThemeConfig) string {
	if !theme.NavSticky {
		return "static"
	}
	return "sticky"
}

// themePreviewMaxH 生成历史卡片文本预览的最大高度（N 行 × 1.7em 行高），0 为不限制。
func themePreviewMaxH(theme ThemeConfig) string {
	lines := max(0, min(20, theme.PreviewLines))
	if lines == 0 {
		return "none"
	}
	return strconv.FormatFloat(float64(lines)*1.7, 'f', -1, 64) + "em"
}

// themeRootVars 生成与前端 applyTheme 写入 documentElement 完全一致的 CSS 变量声明。
func themeRootVars(theme ThemeConfig, dark bool) string {
	colors := resolveThemeColors(theme, dark)
	alpha := themeSurfaceAlpha(theme)
	var sb strings.Builder
	write := func(name, value string) {
		sb.WriteString(name)
		sb.WriteByte(':')
		sb.WriteString(value)
		sb.WriteByte(';')
	}
	write("--p", colors.P)
	write("--p2", colors.P2)
	write("--on-p", themeTextColorFor(colors.P))
	write("--on-p2", themeTextColorFor(colors.P2))
	write("--accent", colors.Accent)
	write("--on-accent", themeTextColorFor(colors.Accent))
	write("--bg", colors.Bg)
	write("--bg2", colors.Bg2)
	write("--card", colors.Card)
	write("--card-border", colors.Border)
	write("--card-rgba", themeHexToRgba(colors.Card, alpha))
	write("--nav-bg", themeHexToRgba(colors.Bg, themeNavAlpha(theme)))
	write("--nav-backdrop", themeNavBackdrop(theme))
	write("--nav-border-bottom", themeNavBorderBottom(theme, colors.Border))
	write("--nav-position", themeNavPosition(theme))
	write("--preview-max-h", themePreviewMaxH(theme))
	write("--tx", colors.Tx)
	write("--sub", colors.Sub)
	write("--muted", colors.Muted)
	write("--p-dim", themeHexToRgba(colors.P, .12))
	write("--p-glow", themeHexToRgba(colors.P, .18))
	write("--p-border", themeHexToRgba(colors.P, .22))
	write("--accent-dim", themeHexToRgba(colors.Accent, .1))
	write("--accent-glow", themeHexToRgba(colors.Accent, .16))
	write("--accent-border", themeHexToRgba(colors.Accent, .3))
	write("--shadow-md", "0 4px 16px "+themeHexToRgba(colors.P, .1)+", 0 2px 6px rgba(0,0,0,.05)")
	write("--shadow-lg", "0 8px 32px "+themeHexToRgba(colors.P, .15)+", 0 3px 10px rgba(0,0,0,.08)")
	return sb.String()
}

// themeBootCSS 生成 /theme-boot.css 的内容：主题变量以外部样式表形式下发，
// 兼容页面 CSP（style-src 'self' 禁止内联 <style>，内联块会被浏览器直接丢弃，
// 首帧退化为 tokens.css 的默认皮肤）。system 模式下 data-theme-mode 属性
// 由 web/theme-mode.js 在首帧前同步设置。
func themeBootCSS(theme ThemeConfig) string {
	fontScale := themeFontScaleValue(theme)
	switch theme.Mode {
	case "light", "dark":
		return `:root{` + themeRootVars(theme, theme.Mode == "dark") + `--font-scale:` + fontScale + `}`
	default:
		return `:root{` + themeRootVars(theme, false) + `--font-scale:` + fontScale + `}` +
			`@media (prefers-color-scheme: dark){:root{` + themeRootVars(theme, true) + `}}`
	}
}

// themeFixedMode 返回固定显示模式（"light"/"dark"），system 模式返回空串，
// 由客户端脚本按系统偏好设置属性。
func themeFixedMode(theme ThemeConfig) string {
	if theme.Mode == "light" || theme.Mode == "dark" {
		return theme.Mode
	}
	return ""
}
