package clipvault

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
)

func (s *SearchServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Path
	ct := "text/css; charset=utf-8"
	if strings.HasSuffix(name, ".js") {
		ct = "application/javascript; charset=utf-8"
	}
	s.serveFile(w, "web"+name, ct)
}

var applicationModuleAssets = []string{
	"/app/action-target.js", "/app/actions.js", "/app/app-context.js", "/app/appearance-background.js", "/app/appearance-form.js", "/app/appearance-loader.js", "/app/appearance-media.js", "/app/appearance-theme.js",
	"/app/auth.js", "/app/cleanup-modal.js", "/app/config-events.js", "/app/config-save.js", "/app/groups.js", "/app/history-bootstrap.js",
	"/app/clipboard-event-details.js", "/app/history-actions.js", "/app/history-layout.js", "/app/history-lifecycle.js", "/app/history-list.js", "/app/history-navigation.js", "/app/license.js",
	"/app/modal-controller.js", "/app/notifications.js", "/app/recovery.js", "/app/runtime-state.js", "/app/settings-lifecycle.js", "/app/settings-sources.js", "/app/settings-storage.js",
	"/app/source-data.js", "/app/text-utils.js", "/app/transfer.js",
	"/app/logs.js", "/app/groups-page.js", "/app/settings-navigation.js", "/app/settings-system.js", "/app/settings-bootstrap.js",
	"/app/data-api.js", "/app/data-api-audit.js", "/app/confirm-arm.js",
	"/app/mcp.js", "/app/mcp-audit.js",
	"/app/language-editor.js",
}

type applicationPage struct {
	Path string
	Name string
	// TitleKey 是页面标题的 i18n 词条 Key；渲染时经 {{T}} 翻译为当前语言。
	TitleKey string
	// Menu 是左侧菜单的分组键，模板用它高亮当前顶级菜单项。
	Menu             string
	Bootstrap        string
	RequireSecondary bool
	Parts            []string
	template         *template.Template
}

// settingsPageParts 组装一个设置分区页面：共享左侧菜单 + 分组内容 + 全部弹层。
func settingsPageParts(content string) []string {
	return []string{
		"web/pages/page-start.html", "web/pages/settings-menu.html", content,
		"web/pages/settings-source-overlay.html", "web/pages/auth-overlays.html",
		"web/pages/settings-overlays.html", "web/pages/page-end.html",
	}
}

var applicationPageDefinitions = []applicationPage{
	{
		Path:      "/",
		Name:      "history",
		TitleKey:  "app.title.history",
		Bootstrap: "/app/history-bootstrap.js",
		Parts: []string{
			"web/pages/page-start.html", "web/pages/history-content.html", "web/pages/history-overlay.html",
			"web/pages/auth-overlays.html", "web/pages/page-end.html",
		},
	},
	{
		Path:      "/settings",
		Name:      "settings",
		TitleKey:  "app.title.settings",
		Menu:      "settings",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-home.html"),
	},
	{
		Path:      "/settings/language",
		Name:      "settings",
		TitleKey:  "app.title.language",
		Menu:      "language",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-language.html"),
	},
	{
		Path:      "/settings/appearance",
		Name:      "settings",
		TitleKey:  "app.title.appearance",
		Menu:      "settings",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-appearance.html"),
	},
	{
		Path:      "/settings/history",
		Name:      "settings",
		TitleKey:  "app.title.settings_history",
		Menu:      "settings-history",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-history.html"),
	},
	{
		Path:      "/settings/navigation",
		Name:      "settings",
		TitleKey:  "app.title.settings_navigation",
		Menu:      "settings-navigation",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-navigation.html"),
	},
	{
		Path:      "/settings/general",
		Name:      "settings",
		TitleKey:  "app.title.general",
		Menu:      "settings",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-general.html"),
	},
	{
		Path:             "/settings/security",
		Name:             "settings",
		TitleKey:         "app.title.security",
		Menu:             "settings",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/settings-security.html"),
	},
	{
		Path:             "/settings/storage",
		Name:             "settings",
		TitleKey:         "app.title.storage",
		Menu:             "settings",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/settings-storage.html"),
	},
	{
		Path:             "/settings/backup",
		Name:             "settings",
		TitleKey:         "app.title.backup",
		Menu:             "settings",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/settings-backup.html"),
	},
	{
		Path:      "/settings/transfer",
		Name:      "settings",
		TitleKey:  "app.title.transfer",
		Menu:      "settings",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-transfer.html"),
	},
	{
		Path:             "/settings/service",
		Name:             "settings",
		TitleKey:         "app.title.service",
		Menu:             "settings",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/settings-service.html"),
	},
	{
		Path:      "/groups",
		Name:      "settings",
		TitleKey:  "app.title.groups",
		Menu:      "groups",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/page-groups.html"),
	},
	{
		Path:             "/data-api",
		Name:             "settings",
		TitleKey:         "app.title.data_api",
		Menu:             "data-api",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/page-data-api.html"),
	},
	{
		Path:             "/mcp",
		Name:             "settings",
		TitleKey:         "app.title.mcp",
		Menu:             "mcp",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/page-mcp.html"),
	},
	{
		Path:             "/license",
		Name:             "settings",
		TitleKey:         "app.title.license",
		Menu:             "license",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/page-license.html"),
	},
	{
		Path:             "/maintenance",
		Name:             "settings",
		TitleKey:         "app.title.maintenance",
		Menu:             "maintenance",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/page-maintenance.html"),
	},
	{
		Path:      "/logs/operations",
		Name:      "settings",
		TitleKey:  "app.title.logs_operations",
		Menu:      "logs-operations",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/page-logs-operations.html"),
	},
	{
		Path:      "/logs/searches",
		Name:      "settings",
		TitleKey:  "app.title.logs_searches",
		Menu:      "logs-searches",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/page-logs-searches.html"),
	},
}

var applicationPages = compileApplicationPages(applicationPageDefinitions)

var applicationPageParts = collectApplicationPageParts(applicationPageDefinitions)

func compileApplicationPages(pages []applicationPage) map[string]applicationPage {
	compiled := make(map[string]applicationPage, len(pages))
	for _, page := range pages {
		if _, exists := compiled[page.Path]; exists {
			panic("duplicate application page route: " + page.Path)
		}
		var source strings.Builder
		for _, name := range page.Parts {
			data, err := fs.ReadFile(webFS, name)
			if err != nil {
				panic("read application page fragment " + name + ": " + err.Error())
			}
			source.Write(data)
			source.WriteByte('\n')
		}
		page.template = template.Must(template.New(page.Name).Funcs(templateFuncMap()).Parse(source.String()))
		compiled[page.Path] = page
	}
	return compiled
}

func collectApplicationPageParts(pages []applicationPage) []string {
	seen := make(map[string]struct{})
	var parts []string
	for _, page := range pages {
		for _, name := range page.Parts {
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			parts = append(parts, name)
		}
	}
	return parts
}

type applicationPageView struct {
	applicationPage
	// Title 遮蔽嵌入的页面定义，渲染期为当前语言的标题译文。
	Title string
	// Lang 是当前界面语言（BCP 47 code），渲染为 <html lang>。
	Lang string
	// ThemeMode 非空时表示固定显示模式（"light"/"dark"），渲染为 <html> 的
	// data-theme-mode 属性；system 模式留空，由 theme-mode.js 按系统偏好设置。
	ThemeMode string
	// AppPage 是页面标识（如 "history"、"settings"），渲染为 <html> 的
	// data-app-page 属性，供 CSS 把历史页布局规则严格限定在历史页面。
	AppPage string
	// 历史页布局的 <html> 属性值：使历史页布局与列数随首帧生效，
	// 避免 JS 拉取配置后再重排造成的闪动。值均为受控标识符（模板安全转义）。
	HistoryLayout       string // top-bottom | left-right
	HistoryColumns      string // "0"（自动）| "1"-"4"
	HistoryForceColumns string // 非空时按该列数强制固定
}

func (s *SearchServer) serveApplicationPage(w http.ResponseWriter, page applicationPage) {
	appearance := DefaultConfig().Appearance
	historyPage := DefaultConfig().HistoryPage
	if s.cfgMgr != nil {
		cfg := s.cfgMgr.Get()
		appearance = cfg.Appearance
		historyPage = cfg.HistoryPage
	}
	historyAttrs := historyPageAttrsValue(historyPage)
	view := applicationPageView{applicationPage: page, Title: templateT(page.TitleKey, nil), Lang: currentTemplateLanguage(), ThemeMode: themeFixedMode(appearance), AppPage: page.Name,
		HistoryLayout: historyAttrs.layout, HistoryColumns: historyAttrs.columns, HistoryForceColumns: historyAttrs.force}
	var rendered bytes.Buffer
	if err := page.template.Execute(&rendered, view); err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "asset_invalid", s.i18nT("api.error.asset_invalid", nil), err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(rendered.Bytes())
}

// historyPageAttrsValue 解析历史页布局的 <html> 属性值。属性在所有页面输出
// （选择器只在历史页命中，设置页无副作用）；列数/强制列数属性由 CSS 断点
// 体系消费，详见 web/history.css。
func historyPageAttrsValue(page HistoryPageSettings) (attrs struct {
	layout  string
	columns string
	force   string
}) {
	layout := "top-bottom"
	if page.Layout == "left-right" {
		layout = "left-right"
	}
	columns := clampInt(page.Columns, 0, 4)
	attrs.layout = layout
	attrs.columns = strconv.Itoa(columns)
	if columns > 0 && page.ForceColumns {
		attrs.force = strconv.Itoa(columns)
	}
	return attrs
}

// handleThemeBootCSS 按当前配置动态生成主题变量样式表。作为外部 CSS 引用时
// 是渲染阻塞的，首帧即按用户配置的皮肤绘制，且满足 style-src 'self' 的 CSP。
func (s *SearchServer) handleThemeBootCSS(w http.ResponseWriter, _ *http.Request) {
	cfg := DefaultConfig()
	if s.cfgMgr != nil {
		cfg = s.cfgMgr.Get()
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(themeBootCSS(cfg.Appearance, cfg.Navigation, cfg.HistoryPage)))
}

func (s *SearchServer) serveFile(w http.ResponseWriter, name, contentType string) {
	data, err := fs.ReadFile(webFS, name)
	if err != nil {
		s.writeAPIError(w, http.StatusNotFound, "not_found", s.i18nT("api.error.not_found", nil), nil)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func serveEmbeddedWebFile(w http.ResponseWriter, name, contentType string) {
	data, err := fs.ReadFile(webFS, name)
	if err != nil {
		http.Error(w, "resource not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
