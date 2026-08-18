package clipvault

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
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
	"/app/history-actions.js", "/app/history-lifecycle.js", "/app/history-list.js", "/app/history-navigation.js", "/app/license.js",
	"/app/modal-controller.js", "/app/notifications.js", "/app/recovery.js", "/app/runtime-state.js", "/app/settings-lifecycle.js", "/app/settings-sources.js", "/app/settings-storage.js",
	"/app/source-data.js", "/app/text-utils.js", "/app/transfer.js",
	"/app/logs.js", "/app/groups-page.js", "/app/settings-navigation.js", "/app/settings-system.js", "/app/settings-bootstrap.js",
}

type applicationPage struct {
	Path  string
	Name  string
	Title string
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
		Title:     "历史",
		Bootstrap: "/app/history-bootstrap.js",
		Parts: []string{
			"web/pages/page-start.html", "web/pages/history-content.html", "web/pages/history-overlay.html",
			"web/pages/auth-overlays.html", "web/pages/page-end.html",
		},
	},
	{
		Path:      "/settings",
		Name:      "settings",
		Title:     "设置",
		Menu:      "settings",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-home.html"),
	},
	{
		Path:      "/settings/appearance",
		Name:      "settings",
		Title:     "外观与通知",
		Menu:      "settings",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-appearance.html"),
	},
	{
		Path:      "/settings/general",
		Name:      "settings",
		Title:     "录制与快捷键",
		Menu:      "settings",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-general.html"),
	},
	{
		Path:             "/settings/security",
		Name:             "settings",
		Title:            "安全与隐私",
		Menu:             "settings",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/settings-security.html"),
	},
	{
		Path:             "/settings/storage",
		Name:             "settings",
		Title:            "存储与数据",
		Menu:             "settings",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/settings-storage.html"),
	},
	{
		Path:             "/settings/backup",
		Name:             "settings",
		Title:            "定时备份",
		Menu:             "settings",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/settings-backup.html"),
	},
	{
		Path:      "/settings/transfer",
		Name:      "settings",
		Title:     "导入导出",
		Menu:      "settings",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/settings-transfer.html"),
	},
	{
		Path:             "/settings/service",
		Name:             "settings",
		Title:            "网络与服务",
		Menu:             "settings",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/settings-service.html"),
	},
	{
		Path:      "/groups",
		Name:      "settings",
		Title:     "分组管理",
		Menu:      "groups",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/page-groups.html"),
	},
	{
		Path:             "/license",
		Name:             "settings",
		Title:            "软件授权",
		Menu:             "license",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/page-license.html"),
	},
	{
		Path:             "/maintenance",
		Name:             "settings",
		Title:            "运行维护",
		Menu:             "maintenance",
		Bootstrap:        "/app/settings-bootstrap.js",
		RequireSecondary: true,
		Parts:            settingsPageParts("web/pages/page-maintenance.html"),
	},
	{
		Path:      "/logs/operations",
		Name:      "settings",
		Title:     "操作记录",
		Menu:      "logs-operations",
		Bootstrap: "/app/settings-bootstrap.js",
		Parts:     settingsPageParts("web/pages/page-logs-operations.html"),
	},
	{
		Path:      "/logs/searches",
		Name:      "settings",
		Title:     "搜索记录",
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
		page.template = template.Must(template.New(page.Name).Parse(source.String()))
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
	// ThemeMode 非空时表示固定显示模式（"light"/"dark"），渲染为 <html> 的
	// data-theme-mode 属性；system 模式留空，由 theme-mode.js 按系统偏好设置。
	ThemeMode string
}

func (s *SearchServer) serveApplicationPage(w http.ResponseWriter, page applicationPage) {
	theme := DefaultConfig().Theme
	if s.cfgMgr != nil {
		theme = s.cfgMgr.Get().Theme
	}
	view := applicationPageView{applicationPage: page, ThemeMode: themeFixedMode(theme)}
	var rendered bytes.Buffer
	if err := page.template.Execute(&rendered, view); err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "asset_invalid", "页面资源渲染失败", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(rendered.Bytes())
}

// handleThemeBootCSS 按当前配置动态生成主题变量样式表。作为外部 CSS 引用时
// 是渲染阻塞的，首帧即按用户配置的皮肤绘制，且满足 style-src 'self' 的 CSP。
func (s *SearchServer) handleThemeBootCSS(w http.ResponseWriter, _ *http.Request) {
	theme := DefaultConfig().Theme
	if s.cfgMgr != nil {
		theme = s.cfgMgr.Get().Theme
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(themeBootCSS(theme)))
}

func (s *SearchServer) serveFile(w http.ResponseWriter, name, contentType string) {
	data, err := fs.ReadFile(webFS, name)
	if err != nil {
		s.writeAPIError(w, http.StatusNotFound, "not_found", "资源不存在", nil)
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
