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
	"/app/settings-navigation.js", "/app/settings-bootstrap.js",
}

type applicationPage struct {
	Path      string
	Name      string
	Title     string
	Bootstrap string
	Parts     []string
	template  *template.Template
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
		Bootstrap: "/app/settings-bootstrap.js",
		Parts: []string{
			"web/pages/page-start.html", "web/pages/settings-shell.html", "web/pages/settings-general.html",
			"web/pages/settings-backup.html", "web/pages/settings-source-overlay.html", "web/pages/auth-overlays.html",
			"web/pages/settings-overlays.html", "web/pages/page-end.html",
		},
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

func (s *SearchServer) serveApplicationPage(w http.ResponseWriter, page applicationPage) {
	var rendered bytes.Buffer
	if err := page.template.Execute(&rendered, page); err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "asset_invalid", "页面资源渲染失败", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(rendered.Bytes())
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
