package clipvault

import (
	"bytes"
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
	"/app/actions.js", "/app/app-context.js", "/app/appearance-form.js", "/app/appearance-media.js", "/app/appearance-theme.js",
	"/app/auth.js", "/app/bootstrap.js", "/app/config-events.js", "/app/config-save.js", "/app/groups.js",
	"/app/history-actions.js", "/app/history-list.js", "/app/license.js", "/app/lifecycle.js", "/app/navigation.js",
	"/app/recovery.js", "/app/settings-sources.js", "/app/settings-storage.js", "/app/text-utils.js", "/app/transfer.js",
	"/app/view-navigation.js",
}

var applicationPageParts = []string{
	"web/pages/shell.html", "web/pages/settings-general.html", "web/pages/settings-backup.html", "web/pages/overlays.html",
}

func (s *SearchServer) serveApplicationPage(w http.ResponseWriter) {
	var page bytes.Buffer
	for _, name := range applicationPageParts {
		data, err := fs.ReadFile(webFS, name)
		if err != nil {
			s.writeAPIError(w, http.StatusInternalServerError, "asset_missing", "页面资源不完整", err)
			return
		}
		page.Write(data)
		page.WriteByte('\n')
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page.Bytes())
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
