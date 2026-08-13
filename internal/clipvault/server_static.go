package clipvault

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
)

func (s *SearchServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/script.js" {
		s.serveApplicationModule(w)
		return
	}
	name := r.URL.Path
	ct := "text/css; charset=utf-8"
	if strings.HasSuffix(name, ".js") {
		ct = "application/javascript; charset=utf-8"
	}
	s.serveFile(w, "web"+name, ct)
}

var applicationModuleParts = []string{
	"web/app/bootstrap.js", "web/app/auth.js", "web/app/history.js", "web/app/files.js", "web/app/appearance.js",
	"web/app/settings.js", "web/app/config-form.js", "web/app/transfer.js", "web/app/groups.js", "web/app/lifecycle.js",
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

func (s *SearchServer) serveApplicationModule(w http.ResponseWriter) {
	var module bytes.Buffer
	for _, name := range applicationModuleParts {
		data, err := fs.ReadFile(webFS, name)
		if err != nil {
			s.writeAPIError(w, http.StatusInternalServerError, "asset_missing", "应用资源不完整", err)
			return
		}
		module.Write(data)
		module.WriteByte('\n')
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(module.Bytes())
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
