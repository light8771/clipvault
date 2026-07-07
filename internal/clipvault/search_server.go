package clipvault

import (
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var webFS embed.FS

type SearchServer struct {
	cfgMgr *ConfigManager
	store  *Store
	state  *RuntimeState
	logger *DailyLogger
	server *http.Server
	url    string
}

func NewSearchServer(cfgMgr *ConfigManager, store *Store, state *RuntimeState, logger *DailyLogger) *SearchServer {
	return &SearchServer{cfgMgr: cfgMgr, store: store, state: state, logger: logger}
}

func (s *SearchServer) Start() (string, error) {
	cfg := s.cfgMgr.Get()
	if !cfg.Search.Enabled {
		return "", nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/pause", s.handlePause)
	mux.HandleFunc("DELETE /api/items", s.handleItems)
	mux.HandleFunc("PATCH /api/items", s.handleStarItem)
	mux.HandleFunc("POST /api/clear", s.handleClear)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handlePutConfig)
	mux.HandleFunc("GET /api/export/config", s.handleExportConfig)
	mux.HandleFunc("GET /api/export/data", s.handleExportData)
	mux.HandleFunc("GET /style.css", s.handleStatic)
	mux.HandleFunc("GET /script.js", s.handleStatic)
	mux.HandleFunc("GET /api/file/check", s.handleFileCheck)
	mux.HandleFunc("GET /api/file", s.handleFileServe)
	mux.HandleFunc("POST /api/file/opendir", s.handleFileOpenDir)

	host, portStr, _ := net.SplitHostPort(cfg.Search.ListenAddr)
	if host == "" {
		host = "127.0.0.1"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 18765
	}
	const maxAttempts = 5
	var ln net.Listener
	for i := 0; i < maxAttempts; i++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port+i))
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			if i > 0 {
				s.logger.Warn("search_server_port_fallback", map[string]any{
					"original": cfg.Search.ListenAddr, "actual": addr,
				})
			}
			break
		}
	}
	if err != nil {
		return "", fmt.Errorf("could not bind any port (tried %d..%d): %w", port, port+maxAttempts-1, err)
	}

	addr := ln.Addr().String()
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok && tcp.IP.IsUnspecified() {
		addr = "127.0.0.1:" + strconv.Itoa(tcp.Port)
	}
	s.url = "http://" + addr + "/"
	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("search_server_failed", err, map[string]any{"addr": cfg.Search.ListenAddr})
		}
	}()
	s.logger.Info("search_server_started", map[string]any{"url": s.url})
	return s.url, nil
}

func (s *SearchServer) URL() string {
	return s.url
}

func (s *SearchServer) Close(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *SearchServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, "web/index.html", "text/html; charset=utf-8")
}

func (s *SearchServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Path
	ct := "text/css; charset=utf-8"
	if strings.HasSuffix(name, ".js") {
		ct = "application/javascript; charset=utf-8"
	}
	s.serveFile(w, "web"+name, ct)
}

func (s *SearchServer) serveFile(w http.ResponseWriter, name, contentType string) {
	data, err := fs.ReadFile(webFS, name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *SearchServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, total, err := s.store.Search(r.Context(), SearchOptions{
		Query:       r.URL.Query().Get("q"),
		Limit:       limit,
		Offset:      offset,
		StarredOnly: r.URL.Query().Get("starred") == "1",
		Kind:        r.URL.Query().Get("kind"),
		DateFrom:    r.URL.Query().Get("date_from"),
		DateTo:      r.URL.Query().Get("date_to"),
	})
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total})
}

func (s *SearchServer) handleState(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{"paused": s.state.IsPaused()})
}

func (s *SearchServer) handlePause(w http.ResponseWriter, r *http.Request) {
	value := r.URL.Query().Get("paused")
	switch value {
	case "true", "1", "yes":
		s.state.SetPaused(true)
	case "false", "0", "no":
		s.state.SetPaused(false)
	default:
		s.state.TogglePaused()
	}
	s.writeJSON(w, map[string]any{"paused": s.state.IsPaused()})
}

func (s *SearchServer) handleItems(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.store.Delete(r.Context(), id); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) handleStarItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	starred := r.URL.Query().Get("starred") != "0"
	if err := s.store.Star(r.Context(), id, starred); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "starred": starred})
}

func (s *SearchServer) handleClear(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.ClearAll(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.logger.Info("history_cleared", map[string]any{"deleted": n})
	s.writeJSON(w, map[string]any{"ok": true, "deleted": n})
}

// handleGetConfig 返回当前完整配置（用于设置页面初始化）。
func (s *SearchServer) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, s.cfgMgr.Get())
}

// handlePutConfig 接收设置页面提交的配置并持久化，立即通知各组件。
// 注意：search.listenAddr 和 hotkey 变更需重启应用后生效。
func (s *SearchServer) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var cfg Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.cfgMgr.Update(cfg); err != nil {
		s.writeError(w, err)
		return
	}
	s.logger.Info("config_updated", nil)
	s.writeJSON(w, map[string]any{"ok": true, "config": s.cfgMgr.Get()})
}

// handleExportConfig 将当前配置以 JSON 文件形式下载。
func (s *SearchServer) handleExportConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="clipvault-config.json"`)
	_, _ = w.Write(data)
}

// handleExportData 导出所有剪切板记录，支持 format=json（默认）和 format=csv。
func (s *SearchServer) handleExportData(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format != "csv" {
		format = "json"
	}

	items, err := s.store.ExportAll(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}

	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="clipvault-data.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "createdAt", "kind", "format", "text", "byteLen", "charLen", "sha256", "starred"})
		for _, item := range items {
			starred := "0"
			if item.Starred {
				starred = "1"
			}
			_ = cw.Write([]string{
				strconv.FormatInt(item.ID, 10),
				item.CreatedAt.Format(time.RFC3339),
				item.Kind,
				item.Format,
				item.Text,
				strconv.Itoa(item.ByteLen),
				strconv.Itoa(item.CharLen),
				item.SHA256,
				starred,
			})
		}
		cw.Flush()
	default: // json
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="clipvault-data.json"`)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"exportedAt": time.Now().UTC().Format(time.RFC3339),
			"total":      len(items),
			"items":      items,
		})
	}
	s.logger.Info("data_exported", map[string]any{"format": format, "total": len(items)})
}

// handleFileCheck checks if a file exists and returns its metadata.
func (s *SearchServer) handleFileCheck(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(p)
	resp := map[string]any{"exists": false}
	if err == nil && !info.IsDir() {
		resp["exists"] = true
		resp["size"] = info.Size()
		resp["name"] = filepath.Base(p)
		resp["ext"] = strings.ToLower(filepath.Ext(p))
	}
	s.writeJSON(w, resp)
}

// handleFileServe serves a file for inline viewing or download.
func (s *SearchServer) handleFileServe(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(p)+`"`)
	}
	http.ServeFile(w, r, p)
}

// handleFileOpenDir opens the file's containing directory in the system file explorer.
func (s *SearchServer) handleFileOpenDir(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	if err := openFolderAndSelectItem(p); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *SearchServer) writeError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
