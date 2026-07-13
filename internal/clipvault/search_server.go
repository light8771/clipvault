package clipvault

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"clipboard_ext/internal/licensing"
)

//go:embed web/*
var webFS embed.FS

type SearchServer struct {
	cfgMgr    *ConfigManager
	store     *Store
	state     *RuntimeState
	logger    *DailyLogger
	auth      *AuthManager
	env       Environment
	server    *http.Server
	url       string
	restartCh chan struct{}
	backup    *BackupService
	csrfToken string
	license   *licensing.Manager
}

type freshSecondaryContextKey struct{}

func NewSearchServer(cfgMgr *ConfigManager, store *Store, state *RuntimeState, logger *DailyLogger, env Environment, backup *BackupService, license *licensing.Manager) *SearchServer {
	am := NewAuthManager()
	am.startSessionCleaner()
	return &SearchServer{cfgMgr: cfgMgr, store: store, state: state, logger: logger, auth: am, env: env, restartCh: make(chan struct{}, 1), backup: backup, license: license}
}

func (s *SearchServer) Start() (string, error) {
	cfg := s.cfgMgr.Get()
	if !cfg.Search.Enabled {
		return "", nil
	}
	host, port, err := validateListenAddr(cfg.Search.ListenAddr)
	if err != nil {
		return "", err
	}
	if s.csrfToken == "" {
		s.csrfToken, err = generateCSRFToken()
		if err != nil {
			return "", fmt.Errorf("generate csrf token: %w", err)
		}
	}
	mux := http.NewServeMux()

	// 公开端点（不需要锁屏认证）
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /style.css", s.handleStatic)
	mux.HandleFunc("GET /script.js", s.handleStatic)
	mux.HandleFunc("GET /api/csrf", s.handleCSRF)
	mux.HandleFunc("GET /api/license/status", s.handleLicenseStatus)
	mux.HandleFunc("GET /api/license/machine", s.handleLicenseMachine)
	mux.HandleFunc("POST /api/license/activate", s.handleLicenseActivate)
	mux.HandleFunc("POST /api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/secondary", s.handleAuthSecondary)
	mux.HandleFunc("POST /api/auth/password", s.requireAuth(s.handleAuthSetPassword))
	mux.HandleFunc("POST /api/restart", s.requireAuth(s.handleRestart))

	// 需要锁屏认证的端点
	mux.HandleFunc("GET /api/search", s.requireAuth(s.handleSearch))
	mux.HandleFunc("GET /api/state", s.requireAuth(s.handleState))
	mux.HandleFunc("POST /api/pause", s.requireAuth(s.handlePause))
	mux.HandleFunc("DELETE /api/items", s.requireAuth(s.handleItems))
	mux.HandleFunc("PATCH /api/items", s.requireAuth(s.handleStarItem))
	mux.HandleFunc("POST /api/clear", s.requireSecondary(s.handleClear))
	mux.HandleFunc("GET /api/config", s.requireAuth(s.handleGetConfig))
	mux.HandleFunc("PUT /api/config", s.requireSecondary(s.handlePutConfig))
	mux.HandleFunc("GET /api/sensitive/rules", s.requireSecondary(s.handleSensitiveRules))
	mux.HandleFunc("GET /api/search/sensitive", s.requireSecondary(s.handleSensitiveSearch))
	mux.HandleFunc("GET /api/items/sensitive", s.requireSecondary(s.handleSensitiveItem))
	mux.HandleFunc("GET /api/items/position", s.requireAuth(s.handleItemPosition))
	mux.HandleFunc("GET /api/file/check", s.requireAuth(s.handleFileCheck))
	mux.HandleFunc("GET /api/file", s.requireAuth(s.handleFileServe))
	mux.HandleFunc("POST /api/file/opendir", s.requireAuth(s.handleFileOpenDir))
	mux.HandleFunc("GET /api/file/hash", s.requireAuth(s.handleFileHash))

	// 分组管理端点
	mux.HandleFunc("GET /api/groups", s.requireAuth(s.handleGroupsList))
	mux.HandleFunc("POST /api/groups", s.requireAuth(s.handleGroupsCreate))
	mux.HandleFunc("PUT /api/groups", s.requireAuth(s.handleGroupsUpdate))
	mux.HandleFunc("DELETE /api/groups", s.requireAuth(s.handleGroupsDelete))
	mux.HandleFunc("POST /api/items/group", s.requireAuth(s.handleItemAddToGroup))
	mux.HandleFunc("DELETE /api/items/group", s.requireAuth(s.handleItemRemoveFromGroup))
	mux.HandleFunc("GET /api/items/groups", s.requireAuth(s.handleItemGroups))

	// 导出/导入端点（需二次认证）
	mux.HandleFunc("GET /api/export/config", s.requireSecondary(s.handleExportConfig))
	mux.HandleFunc("GET /api/export/data", s.requireSecondary(s.handleExportData))
	mux.HandleFunc("POST /api/export/data", s.requireSecondary(s.handleExportData))
	mux.HandleFunc("POST /api/import/data", s.requireSecondary(s.handleImportData))

	// 恢复密码管理端点（需二次认证）
	mux.HandleFunc("GET /api/recovery/status", s.requireSecondary(s.handleRecoveryStatus))
	mux.HandleFunc("POST /api/recovery/set", s.requireSecondary(s.handleRecoverySet))
	mux.HandleFunc("POST /api/recovery/change", s.requireSecondary(s.handleRecoveryChange))
	mux.HandleFunc("POST /api/recovery/remove", s.requireSecondary(s.handleRecoveryRemove))

	// 备份管理端点（需二次认证）
	mux.HandleFunc("GET /api/backup/status", s.requireAuth(s.handleBackupStatus))
	mux.HandleFunc("POST /api/backup/dir/now", s.requireSecondary(s.handleBackupDirNow))
	mux.HandleFunc("POST /api/backup/email/now", s.requireSecondary(s.handleBackupEmailNow))
	mux.HandleFunc("POST /api/backup/email/test", s.requireSecondary(s.handleBackupEmailTest))

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
		Handler:           s.secureHTTP(mux, host, tcpPort(ln.Addr(), port)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("search_server_failed", err, map[string]any{"addr": cfg.Search.ListenAddr})
		}
	}()
	s.logger.Info("search_server_started", map[string]any{"url": s.url})
	return s.url, nil
}

func tcpPort(addr net.Addr, fallback int) int {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.Port
	}
	return fallback
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

// RestartCh 返回重启信号 channel，收到信号时应用应关闭并重新启动 search server。
func (s *SearchServer) RestartCh() <-chan struct{} {
	return s.restartCh
}

// handleRestart 触发应用重启。
func (s *SearchServer) handleRestart(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{"ok": true})
	s.logger.Info("restart_requested", nil)
	go func() {
		select {
		case s.restartCh <- struct{}{}:
		default:
		}
	}()
}

func (s *SearchServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favicon.ico" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, "web/index.html", "text/html; charset=utf-8")
}

func (s *SearchServer) handleCSRF(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{"token": s.csrfToken})
}

func (s *SearchServer) handleLicenseStatus(w http.ResponseWriter, r *http.Request) {
	if s.license == nil {
		s.writeJSON(w, map[string]any{"licensed": true, "status": "disabled", "enforced": false})
		return
	}
	ctx := s.license.Context()
	s.writeJSON(w, map[string]any{
		"licensed":    ctx.Valid,
		"status":      ctx.Status,
		"enforced":    ctx.Enforced,
		"licenseId":   ctx.LicenseID,
		"edition":     ctx.Edition,
		"machineCode": ctx.MachineCode,
		"version":     AppVersion,
	})
}

func (s *SearchServer) handleLicenseMachine(w http.ResponseWriter, r *http.Request) {
	if s.license == nil {
		http.Error(w, "license manager unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := s.license.Context()
	if ctx.MachineID == "" {
		http.Error(w, "machine id unavailable", http.StatusServiceUnavailable)
		return
	}
	s.writeJSON(w, map[string]any{"machineId": ctx.MachineID, "machineCode": ctx.MachineCode})
}

func (s *SearchServer) handleLicenseActivate(w http.ResponseWriter, r *http.Request) {
	if s.license == nil {
		http.Error(w, "license manager unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Token) == "" {
		s.writeJSON(w, map[string]any{"ok": false, "error": "license_bad_format"})
		return
	}
	ctx, err := s.license.Activate(req.Token)
	if err != nil {
		code := licenseErrorCode(err)
		s.logger.Warn("license_activation_failed", map[string]any{"error": code, "machineCode": ctx.MachineCode})
		s.writeJSON(w, map[string]any{"ok": false, "error": code, "machineCode": ctx.MachineCode})
		return
	}
	if s.backup != nil {
		s.backup.Reload(s.cfgMgr.Get())
	}
	s.logger.Info("license_activated", map[string]any{"licenseId": ctx.LicenseID, "machineCode": ctx.MachineCode})
	s.writeJSON(w, map[string]any{"ok": true, "licenseId": ctx.LicenseID, "edition": ctx.Edition})
}

func licenseErrorCode(err error) string {
	switch {
	case errors.Is(err, licensing.ErrBadTokenFormat), errors.Is(err, licensing.ErrInvalidLicenseID), errors.Is(err, licensing.ErrInvalidMachineID):
		return "license_bad_format"
	case errors.Is(err, licensing.ErrBadSignature):
		return "license_bad_signature"
	case errors.Is(err, licensing.ErrMachineMismatch):
		return "license_machine_mismatch"
	case errors.Is(err, licensing.ErrUnknownKey), errors.Is(err, licensing.ErrUnsupportedVersion), errors.Is(err, licensing.ErrLicenseUnconfigured):
		return "license_unsupported"
	case errors.Is(err, licensing.ErrMachineReadFailed):
		return "license_machine_unavailable"
	default:
		return "license_save_failed"
	}
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
	groupID, _ := strconv.ParseInt(r.URL.Query().Get("group_id"), 10, 64)
	items, total, err := s.store.Search(r.Context(), SearchOptions{
		Query:            r.URL.Query().Get("q"),
		Limit:            limit,
		Offset:           offset,
		StarredOnly:      r.URL.Query().Get("starred") == "1",
		Kind:             r.URL.Query().Get("kind"),
		DateFrom:         r.URL.Query().Get("date_from"),
		DateTo:           r.URL.Query().Get("date_to"),
		IncludeSensitive: true,
		GroupID:          groupID,
	})
	if err != nil {
		s.writeError(w, err)
		return
	}
	// 未设置二次密码时不需要遮蔽；已通过二次认证时也不遮蔽
	cfg := s.cfgMgr.Get()
	token := GetSessionToken(r)
	if s.auth.HasSecondaryPassword(cfg) && !s.auth.HasSecondaryAuth(token) {
		for i := range items {
			if items[i].Sensitive {
				items[i].Text = "【敏感内容，点击查看】"
			}
		}
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total})
}

// handleSensitiveSearch 仅返回敏感记录，需要二次认证。
func (s *SearchServer) handleSensitiveSearch(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, total, err := s.store.Search(r.Context(), SearchOptions{
		Query:         r.URL.Query().Get("q"),
		Limit:         limit,
		Offset:        offset,
		Kind:          r.URL.Query().Get("kind"),
		DateFrom:      r.URL.Query().Get("date_from"),
		DateTo:        r.URL.Query().Get("date_to"),
		SensitiveOnly: true,
	})
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total})
}

// handleSensitiveItem 返回单条敏感记录的明文，需要二次认证。
func (s *SearchServer) handleSensitiveItem(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	item, err := s.store.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	if !item.Sensitive {
		http.Error(w, `{"error":"not sensitive"}`, http.StatusBadRequest)
		return
	}
	s.writeJSON(w, map[string]any{"item": item})
}

func (s *SearchServer) handleItemPosition(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	pos, err := s.store.GetPositionByID(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"position": pos})
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
// 敏感过滤规则（customPatterns）在未通过二次认证时被遮蔽，需要二次认证后通过 /api/sensitive/rules 获取。
func (s *SearchServer) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	token := GetSessionToken(r)
	if s.auth.HasSecondaryPassword(cfg) && !s.auth.HasSecondaryAuth(token) {
		masked := make([]string, len(cfg.Sensitive.CustomPatterns))
		for i := range cfg.Sensitive.CustomPatterns {
			masked[i] = "__masked__"
		}
		cfg.Sensitive.CustomPatterns = masked
	}
	s.writeJSON(w, stripSecrets(cfg))
}

// stripSecrets 清除配置中的密码哈希和盐，避免向前端或导出文件泄露。
func stripSecrets(cfg Config) Config {
	cfg.Security.LockPasswordHash = ""
	cfg.Security.SecondaryPasswordHash = ""
	cfg.Security.PasswordSalt = ""
	// 备份相关敏感密码用占位符标记，前端回传时保留原值
	if cfg.Backup.DirBackup.Password != "" {
		cfg.Backup.DirBackup.Password = "__set__"
	}
	if cfg.Backup.EmailBackup.SMTPPass != "" {
		cfg.Backup.EmailBackup.SMTPPass = "__set__"
	}
	if cfg.Backup.EmailBackup.Password != "" {
		cfg.Backup.EmailBackup.Password = "__set__"
	}
	return cfg
}

// handleSensitiveRules 返回真实的敏感过滤规则，需要二次认证。
func (s *SearchServer) handleSensitiveRules(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	s.writeJSON(w, map[string]any{"customPatterns": cfg.Sensitive.CustomPatterns})
}

// handlePutConfig 接收设置页面提交的配置并持久化，立即通知各组件。
// 注意：search.listenAddr 变更需重启应用后生效。
func (s *SearchServer) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var cfg Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	listenHost, _, err := validateListenAddr(cfg.Search.ListenAddr)
	if err != nil {
		http.Error(w, "invalid search.listenAddr: "+err.Error(), http.StatusBadRequest)
		return
	}
	curCfg := s.cfgMgr.Get()
	if !isLoopbackHost(listenHost) && (!s.auth.HasLockPassword(curCfg) || !s.auth.HasSecondaryPassword(curCfg)) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]any{
			"ok":    false,
			"error": "开放局域网监听前，必须先设置锁屏密码和二次密码",
		})
		return
	}
	curListenHost, _, curListenErr := validateListenAddr(curCfg.Search.ListenAddr)
	openingLAN := curListenErr == nil && isLoopbackHost(curListenHost) && !isLoopbackHost(listenHost)
	if openingLAN {
		freshVerified, _ := r.Context().Value(freshSecondaryContextKey{}).(bool)
		if !freshVerified {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			s.writeJSON(w, map[string]any{
				"ok":    false,
				"error": "开放局域网监听需要重新输入一次二次密码确认",
				"code":  "fresh_secondary_required",
			})
			return
		}
	}
	// 前端可能在未通过二次认证时提交遮蔽后的 patterns（__masked__），保留原值
	for _, p := range cfg.Sensitive.CustomPatterns {
		if p == "__masked__" {
			cfg.Sensitive.CustomPatterns = s.cfgMgr.Get().Sensitive.CustomPatterns
			break
		}
	}
	// 保留当前密码哈希和盐，不被前端回传的空值覆盖
	if cfg.Security.LockPasswordHash == "" {
		cfg.Security.LockPasswordHash = curCfg.Security.LockPasswordHash
	}
	if cfg.Security.SecondaryPasswordHash == "" {
		cfg.Security.SecondaryPasswordHash = curCfg.Security.SecondaryPasswordHash
	}
	if cfg.Security.PasswordSalt == "" {
		cfg.Security.PasswordSalt = curCfg.Security.PasswordSalt
	}
	// 保留备份相关敏感密码，不被前端回传的占位符或空值覆盖
	if cfg.Backup.DirBackup.Password == "" || cfg.Backup.DirBackup.Password == "__set__" {
		cfg.Backup.DirBackup.Password = curCfg.Backup.DirBackup.Password
	}
	if cfg.Backup.EmailBackup.SMTPPass == "" || cfg.Backup.EmailBackup.SMTPPass == "__set__" {
		cfg.Backup.EmailBackup.SMTPPass = curCfg.Backup.EmailBackup.SMTPPass
	}
	if cfg.Backup.EmailBackup.Password == "" || cfg.Backup.EmailBackup.Password == "__set__" {
		cfg.Backup.EmailBackup.Password = curCfg.Backup.EmailBackup.Password
	}
	if err := s.cfgMgr.Update(cfg); err != nil {
		var hkErr *HotkeyError
		if errors.As(err, &hkErr) {
			s.logger.Info("config_updated", map[string]any{"hotkey_error": hkErr.Error()})
			s.writeJSON(w, map[string]any{"ok": true, "config": stripSecrets(s.cfgMgr.Get()), "hotkeyError": hkErr.Error()})
			return
		}
		s.writeError(w, err)
		return
	}
	s.logger.Info("config_updated", nil)
	s.writeJSON(w, map[string]any{"ok": true, "config": stripSecrets(s.cfgMgr.Get())})
}

// handleExportConfig 将当前配置以 JSON 文件形式下载。
func (s *SearchServer) handleExportConfig(w http.ResponseWriter, r *http.Request) {
	cfg := stripSecrets(s.cfgMgr.Get())
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="clipvault-config.json"`)
	_, _ = w.Write(data)
}

// handleExportData 导出所有剪切板记录，支持 format=json（默认）、format=csv、format=cvexport。
func (s *SearchServer) handleExportData(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format != "csv" && format != "cvexport" {
		format = "json"
	}

	switch format {
	case "csv":
		items, err := s.store.ExportAll(r.Context())
		if err != nil {
			s.writeError(w, err)
			return
		}
		// Build item -> groups map for CSV
		itemGroupsMap := make(map[int64][]int64)
		for _, item := range items {
			gids, _ := s.store.GetItemGroups(r.Context(), item.ID)
			itemGroupsMap[item.ID] = gids
		}
		groups, _ := s.store.ListGroups(r.Context())
		groupNameMap := make(map[int64]string)
		for _, g := range groups {
			groupNameMap[g.ID] = g.Name
		}

		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="clipvault-data.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "createdAt", "kind", "format", "text", "byteLen", "charLen", "sha256", "metaJson", "starred", "sensitive", "groupIds", "groupNames"})
		for _, item := range items {
			starred := "false"
			if item.Starred {
				starred = "true"
			}
			sensitive := "false"
			if item.Sensitive {
				sensitive = "true"
			}
			gids := itemGroupsMap[item.ID]
			gidStrs := make([]string, len(gids))
			nameStrs := make([]string, len(gids))
			for i, gid := range gids {
				gidStrs[i] = strconv.FormatInt(gid, 10)
				nameStrs[i] = groupNameMap[gid]
			}
			_ = cw.Write([]string{
				strconv.FormatInt(item.ID, 10),
				item.CreatedAt.Format(time.RFC3339),
				csvSafe(item.Kind),
				csvSafe(item.Format),
				csvSafe(item.Text),
				strconv.Itoa(item.ByteLen),
				strconv.Itoa(item.CharLen),
				item.SHA256,
				csvSafe(item.MetaJSON),
				starred,
				sensitive,
				strings.Join(gidStrs, ";"),
				csvSafe(strings.Join(nameStrs, ";")),
			})
		}
		cw.Flush()
		s.logger.Info("data_exported", map[string]any{"format": "csv", "total": len(items)})
	default: // json
		export, err := s.store.ExportFullForJSON(r.Context())
		if err != nil {
			s.writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="clipvault-data.json"`)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(export)
		s.logger.Info("data_exported", map[string]any{"format": "json", "total": len(export.Items)})
	case "cvexport":
		if r.Method != http.MethodPost {
			http.Error(w, "cvexport requires POST", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		password := req.Password
		if password == "" {
			http.Error(w, "password is required for cvexport format", http.StatusBadRequest)
			return
		}
		export, err := s.store.ExportFullForJSON(r.Context())
		if err != nil {
			s.writeError(w, err)
			return
		}
		encData, err := EncryptExportJSON(export, password)
		if err != nil {
			s.writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="clipvault-data.cvexport"`)
		_, _ = w.Write(encData)
		s.logger.Info("data_exported", map[string]any{"format": "cvexport", "total": len(export.Items)})
	}
}

// csvSafe prefixes cell values that start with =, +, -, or @ with a single quote
// to prevent CSV formula injection.
func csvSafe(s string) string {
	if len(s) == 0 {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}

// handleImportData receives a JSON export file (or encrypted .cvexport file) and imports it.
func (s *SearchServer) handleImportData(w http.ResponseWriter, r *http.Request) {
	// Limit upload to 100MB
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Check if this is an encrypted .cvexport file
	var jsonData []byte
	if IsCVExport(data) {
		password := r.Header.Get("X-ClipVault-Import-Password")
		if password == "" {
			http.Error(w, "password is required to decrypt .cvexport file", http.StatusBadRequest)
			return
		}
		jsonData, err = DecryptExport(data, password)
		if err != nil {
			s.writeError(w, fmt.Errorf("decrypt cvexport: %w", err))
			return
		}
	} else {
		jsonData = data
	}

	if err := s.store.ImportFullJSON(r.Context(), jsonData); err != nil {
		s.writeError(w, err)
		s.logger.Error("import_failed", err, nil)
		return
	}
	s.logger.Info("data_imported", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// ── Recovery password management ──────────────────────────────

func (s *SearchServer) handleRecoveryStatus(w http.ResponseWriter, r *http.Request) {
	hasRecovery := HasRecoveryPassword(s.env.DataDir)
	s.writeJSON(w, map[string]any{"hasRecovery": hasRecovery})
}

func (s *SearchServer) handleRecoverySet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := SetRecoveryPassword(s.env.DataDir, s.store.DBKey(), req.Password); err != nil {
		s.logger.Error("recovery_set_failed", err, nil)
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.logger.Info("recovery_password_set", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) handleRecoveryChange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := ChangeRecoveryPassword(s.env.DataDir, s.store.DBKey(), req.OldPassword, req.NewPassword); err != nil {
		s.logger.Error("recovery_change_failed", err, nil)
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.logger.Info("recovery_password_changed", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) handleRecoveryRemove(w http.ResponseWriter, r *http.Request) {
	if err := RemoveRecoveryPassword(s.env.DataDir); err != nil {
		s.logger.Error("recovery_remove_failed", err, nil)
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.logger.Info("recovery_password_removed", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// ── Backup handlers ───────────────────────────────────────────

// handleBackupStatus 返回目录备份和邮件备份的最近状态。
func (s *SearchServer) handleBackupStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	resp := map[string]any{
		"dirBackup": map[string]any{
			"enabled":     cfg.Backup.DirBackup.Enabled,
			"interval":    cfg.Backup.DirBackup.Interval,
			"dir":         cfg.Backup.DirBackup.Dir,
			"format":      cfg.Backup.DirBackup.Format,
			"hasPassword": cfg.Backup.DirBackup.Password != "",
		},
		"emailBackup": map[string]any{
			"enabled":     cfg.Backup.EmailBackup.Enabled,
			"interval":    cfg.Backup.EmailBackup.Interval,
			"smtpHost":    cfg.Backup.EmailBackup.SMTPHost,
			"smtpPort":    cfg.Backup.EmailBackup.SMTPPort,
			"smtpUser":    cfg.Backup.EmailBackup.SMTPUser,
			"from":        cfg.Backup.EmailBackup.From,
			"to":          cfg.Backup.EmailBackup.To,
			"format":      cfg.Backup.EmailBackup.Format,
			"hasSmtpPass": cfg.Backup.EmailBackup.SMTPPass != "",
			"hasPassword": cfg.Backup.EmailBackup.Password != "",
		},
	}
	if s.backup != nil {
		ds := s.backup.DirStatus()
		es := s.backup.EmailStatus()
		resp["dirStatus"] = map[string]any{
			"lastTime":   ds.LastTime.Format(time.RFC3339),
			"lastOK":     ds.LastOK,
			"lastError":  ds.LastError,
			"backupFile": ds.BackupFile,
		}
		resp["emailStatus"] = map[string]any{
			"lastTime":   es.LastTime.Format(time.RFC3339),
			"lastOK":     es.LastOK,
			"lastError":  es.LastError,
			"backupFile": es.BackupFile,
		}
	}
	s.writeJSON(w, resp)
}

// handleBackupDirNow 立即执行一次目录备份。
func (s *SearchServer) handleBackupDirNow(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		s.writeJSON(w, map[string]any{"ok": false, "error": "备份服务未启动"})
		return
	}
	cfg := s.cfgMgr.Get()
	if cfg.Backup.DirBackup.Dir == "" {
		s.writeJSON(w, map[string]any{"ok": false, "error": "未配置备份目录"})
		return
	}
	if err := s.backup.RunDirBackup(); err != nil {
		s.logger.Error("dir_backup_manual_failed", err, nil)
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.logger.Info("dir_backup_manual_ok", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleBackupEmailNow 立即执行一次邮件备份。
func (s *SearchServer) handleBackupEmailNow(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		s.writeJSON(w, map[string]any{"ok": false, "error": "备份服务未启动"})
		return
	}
	cfg := s.cfgMgr.Get()
	if cfg.Backup.EmailBackup.SMTPHost == "" || cfg.Backup.EmailBackup.To == "" {
		s.writeJSON(w, map[string]any{"ok": false, "error": "SMTP 配置不完整"})
		return
	}
	if err := s.backup.RunEmailBackup(); err != nil {
		s.logger.Error("email_backup_manual_failed", err, nil)
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.logger.Info("email_backup_manual_ok", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleBackupEmailTest 发送一封测试邮件。
func (s *SearchServer) handleBackupEmailTest(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		s.writeJSON(w, map[string]any{"ok": false, "error": "备份服务未启动"})
		return
	}
	if err := s.backup.SendTestEmail(); err != nil {
		s.logger.Error("email_test_failed", err, nil)
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.logger.Info("email_test_ok", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleFileCheck checks if a file exists and returns its metadata.
func (s *SearchServer) handleFileCheck(w http.ResponseWriter, r *http.Request) {
	p, err := s.resolveRecordedFilePath(r)
	if err != nil {
		http.Error(w, "invalid file reference", http.StatusBadRequest)
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
	p, err := s.resolveRecordedFilePath(r)
	if err != nil {
		http.Error(w, "invalid file reference", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("download") == "1" || !safeInlineImageExt(filepath.Ext(p)) {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(p)}))
	}
	http.ServeFile(w, r, p)
}

// handleFileHash computes and returns the MD5 and SHA256 hashes of a file on demand.
func (s *SearchServer) handleFileHash(w http.ResponseWriter, r *http.Request) {
	p, err := s.resolveRecordedFilePath(r)
	if err != nil {
		http.Error(w, "invalid file reference", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		s.writeJSON(w, map[string]any{"ok": false, "error": "file not found"})
		return
	}
	f, err := os.Open(p)
	if err != nil {
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer f.Close()
	m := md5.New()
	sh := sha256.New()
	if _, err := io.Copy(io.MultiWriter(m, sh), f); err != nil {
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "md5": hex.EncodeToString(m.Sum(nil)), "sha256": hex.EncodeToString(sh.Sum(nil))})
}

// handleFileOpenDir opens the file's containing directory in the system file explorer.
func (s *SearchServer) handleFileOpenDir(w http.ResponseWriter, r *http.Request) {
	p, err := s.resolveRecordedFilePath(r)
	if err != nil {
		http.Error(w, "invalid file reference", http.StatusBadRequest)
		return
	}
	if err := openFolderAndSelectItem(p); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) resolveRecordedFilePath(r *http.Request) (string, error) {
	itemID, err := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64)
	if err != nil || itemID <= 0 {
		return "", fmt.Errorf("invalid item_id")
	}
	fileIndex, err := strconv.Atoi(r.URL.Query().Get("file_index"))
	if err != nil || fileIndex < 0 {
		return "", fmt.Errorf("invalid file_index")
	}
	item, err := s.store.GetByID(r.Context(), itemID)
	if err != nil {
		return "", fmt.Errorf("item not found")
	}
	if item.Format != "CF_HDROP" || !isStoredFileKind(item.Kind) {
		return "", fmt.Errorf("item is not a file record")
	}
	lines := strings.Split(strings.ReplaceAll(item.Text, "\r\n", "\n"), "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			paths = append(paths, line)
		}
	}
	if fileIndex >= len(paths) {
		return "", fmt.Errorf("file_index out of range")
	}
	return paths[fileIndex], nil
}

func isStoredFileKind(kind string) bool {
	switch kind {
	case "image", "video", "archive", "office", "files":
		return true
	default:
		return false
	}
}

func safeInlineImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".ico", ".tiff", ".tif", ".avif":
		return true
	default:
		return false
	}
}

// ── Groups handlers ───────────────────────────────────────────

// handleGroupsList 返回所有分组（平铺列表，前端构建树）。
func (s *SearchServer) handleGroupsList(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"groups": groups})
}

// handleGroupsCreate 创建新分组。
func (s *SearchServer) handleGroupsCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		ParentID int64  `json:"parentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		s.writeJSON(w, map[string]any{"ok": false, "error": "分组名称不能为空"})
		return
	}
	g, err := s.store.CreateGroup(r.Context(), strings.TrimSpace(req.Name), req.ParentID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.logger.Info("group_created", map[string]any{"id": g.ID, "name": g.Name, "parentId": g.ParentID})
	s.writeJSON(w, map[string]any{"ok": true, "group": g})
}

// handleGroupsUpdate 更新分组（重命名 / 移动父级）。
func (s *SearchServer) handleGroupsUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct {
		Name     string `json:"name"`
		ParentID int64  `json:"parentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		s.writeJSON(w, map[string]any{"ok": false, "error": "分组名称不能为空"})
		return
	}
	if err := s.store.UpdateGroup(r.Context(), id, strings.TrimSpace(req.Name), req.ParentID); err != nil {
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.logger.Info("group_updated", map[string]any{"id": id, "name": req.Name, "parentId": req.ParentID})
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleGroupsDelete 删除分组（含后代）。
func (s *SearchServer) handleGroupsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteGroup(r.Context(), id); err != nil {
		s.writeError(w, err)
		return
	}
	s.logger.Info("group_deleted", map[string]any{"id": id})
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleItemAddToGroup 将条目加入分组。
func (s *SearchServer) handleItemAddToGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemID  int64 `json:"itemId"`
		GroupID int64 `json:"groupId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.ItemID <= 0 || req.GroupID <= 0 {
		s.writeJSON(w, map[string]any{"ok": false, "error": "invalid itemId or groupId"})
		return
	}
	if err := s.store.AddItemToGroup(r.Context(), req.ItemID, req.GroupID); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleItemRemoveFromGroup 将条目从分组移除。
func (s *SearchServer) handleItemRemoveFromGroup(w http.ResponseWriter, r *http.Request) {
	itemID, err := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64)
	if err != nil || itemID <= 0 {
		http.Error(w, "invalid item_id", http.StatusBadRequest)
		return
	}
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group_id"), 10, 64)
	if err != nil || groupID <= 0 {
		http.Error(w, "invalid group_id", http.StatusBadRequest)
		return
	}
	if err := s.store.RemoveItemFromGroup(r.Context(), itemID, groupID); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleItemGroups 返回指定条目所属的分组 id 列表。
func (s *SearchServer) handleItemGroups(w http.ResponseWriter, r *http.Request) {
	itemID, err := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64)
	if err != nil || itemID <= 0 {
		http.Error(w, "invalid item_id", http.StatusBadRequest)
		return
	}
	ids, err := s.store.GetItemGroups(r.Context(), itemID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if ids == nil {
		ids = []int64{}
	}
	s.writeJSON(w, map[string]any{"groupIds": ids})
}

func (s *SearchServer) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *SearchServer) writeError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// formatDuration 将时长格式化为中文可读字符串。
func formatDuration(d time.Duration) string {
	if d >= time.Minute {
		min := int((d + time.Minute - 1) / time.Minute)
		return fmt.Sprintf("%d 分钟", min)
	}
	seconds := int((d + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d 秒", seconds)
}

func (s *SearchServer) writeRateLimitBlocked(w http.ResponseWriter, status RateLimitStatus) {
	s.writeJSON(w, map[string]any{
		"ok":                     false,
		"error":                  fmt.Sprintf("5 分钟内最多尝试 10 次，请 %s 后再试", formatDuration(status.RetryAfter)),
		"remainingAttempts":      0,
		"retryAfterSeconds":      int((status.RetryAfter + time.Second - 1) / time.Second),
		"rateLimitWindowMinutes": 5,
	})
}

func (s *SearchServer) writePasswordFailure(w http.ResponseWriter, label string, status RateLimitStatus) {
	if !status.Allowed {
		s.writeRateLimitBlocked(w, status)
		return
	}
	s.writeJSON(w, map[string]any{
		"ok":                     false,
		"error":                  fmt.Sprintf("%s；5 分钟内还可尝试 %d 次", label, status.Remaining),
		"remainingAttempts":      status.Remaining,
		"rateLimitWindowMinutes": 5,
	})
}

// ── Auth middleware ───────────────────────────────────────────

// requireAuth 确保请求已通过锁屏认证。如果未设置锁屏密码，则直接放行。
func (s *SearchServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.cfgMgr.Get()
		if !s.auth.HasLockPassword(cfg) {
			next(w, r)
			return
		}
		token := GetSessionToken(r)
		sess := s.auth.GetSession(token)
		if sess == nil || !sess.Authenticated {
			http.Error(w, "{\"error\":\"unauthorized\"}", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// requireSecondary 确保请求已通过锁屏认证且拥有有效的二次认证。
// 支持两种二次认证方式：
//  1. 会话级缓存（timeout > 0 时，GrantSecondaryAuth 设置的有效期内免重复）
//  2. 一次性 token（timeout = 0 时，每次操作需通过请求头携带）
//
// 如果未设置二次密码，则只要求锁屏认证。
func (s *SearchServer) requireSecondary(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.cfgMgr.Get()
		if !s.auth.HasLockPassword(cfg) {
			// 未设置锁屏密码时，也不需要二次认证
			if !s.auth.HasSecondaryPassword(cfg) {
				next(w, r)
				return
			}
		}
		token := GetSessionToken(r)
		sess := s.auth.GetSession(token)
		if sess == nil || !sess.Authenticated {
			if s.auth.HasLockPassword(cfg) {
				http.Error(w, "{\"error\":\"unauthorized\"}", http.StatusUnauthorized)
				return
			}
			// 没有锁屏密码时，不需要会话认证，但仍需二次认证
		}
		if s.auth.HasSecondaryPassword(cfg) {
			freshVerified := false
			if freshToken := r.Header.Get(freshSecondaryHeaderName); freshToken != "" {
				freshVerified = s.auth.ConsumeSecondaryToken(freshToken)
				if freshVerified {
					r = r.WithContext(context.WithValue(r.Context(), freshSecondaryContextKey{}, true))
				}
			}
			// 先检查会话级二次认证缓存
			if !freshVerified && !s.auth.HasSecondaryAuth(token) {
				// 再检查一次性 token，避免把敏感 token 写入 URL 和浏览器历史。
				oneTimeToken := r.Header.Get("X-ClipVault-Secondary-Token")
				if !s.auth.ConsumeSecondaryToken(oneTimeToken) {
					http.Error(w, "{\"error\":\"secondary_required\"}", http.StatusForbidden)
					return
				}
			}
		}
		next(w, r)
	}
}

// ── Auth endpoints ────────────────────────────────────────────

// handleAuthStatus 返回当前认证状态。
func (s *SearchServer) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	hasLock := s.auth.HasLockPassword(cfg)
	hasSecondary := s.auth.HasSecondaryPassword(cfg)

	token := GetSessionToken(r)
	sess := s.auth.GetSession(token)

	authenticated := !hasLock || (sess != nil && sess.Authenticated)
	secondaryAuthed := !hasSecondary || (sess != nil && sess.SecondaryAuth && !time.Now().After(sess.SecondaryExpiry))

	s.writeJSON(w, map[string]any{
		"hasLockPassword":      hasLock,
		"hasSecondaryPassword": hasSecondary,
		"authenticated":        authenticated,
		"secondaryAuthed":      secondaryAuthed,
	})
}

// handleAuthLogin 验证锁屏密码并创建会话。
func (s *SearchServer) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	cfg := s.cfgMgr.Get()
	if !s.auth.HasLockPassword(cfg) {
		s.writeJSON(w, map[string]any{"ok": true, "message": "no lock password set"})
		return
	}
	if status := s.auth.CheckRateLimit(); !status.Allowed {
		s.writeRateLimitBlocked(w, status)
		return
	}
	if !s.auth.VerifyLockPassword(cfg, req.Password) {
		s.writePasswordFailure(w, "密码错误", s.auth.RecordFailedAttempt())
		return
	}
	if err := s.migratePasswordHash("lock", req.Password); err != nil {
		s.logger.Warn("password_hash_migration_failed", map[string]any{"type": "lock", "error": err.Error()})
	}
	s.auth.ResetRateLimit()
	token, err := s.auth.CreateSession(cfg.Security.LockSessionTimeout)
	if err != nil {
		s.writeError(w, err)
		return
	}
	SetSessionCookie(w, token, cfg.Security.LockSessionTimeout)
	s.logger.Info("auth_login", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleAuthLogout 销毁当前会话。
func (s *SearchServer) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	token := GetSessionToken(r)
	s.auth.DestroySession(token)
	ClearSessionCookie(w)
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleAuthSecondary 验证二次密码并授予二次认证权限。
func (s *SearchServer) handleAuthSecondary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	cfg := s.cfgMgr.Get()
	if !s.auth.HasSecondaryPassword(cfg) {
		s.writeJSON(w, map[string]any{"ok": true, "message": "no secondary password set"})
		return
	}
	if status := s.auth.CheckRateLimit(); !status.Allowed {
		s.writeRateLimitBlocked(w, status)
		return
	}
	token := GetSessionToken(r)
	sess := s.auth.GetSession(token)
	if sess == nil || !sess.Authenticated {
		if s.auth.HasLockPassword(cfg) {
			http.Error(w, "{\"error\":\"unauthorized\"}", http.StatusUnauthorized)
			return
		}
		// 没有锁屏密码时，创建一个临时会话用于二次认证
		var err error
		token, err = s.auth.CreateSession(cfg.Security.LockSessionTimeout)
		if err != nil {
			s.writeError(w, err)
			return
		}
		SetSessionCookie(w, token, cfg.Security.LockSessionTimeout)
	}
	if !s.auth.VerifySecondaryPassword(cfg, req.Password) {
		s.writePasswordFailure(w, "二次密码错误", s.auth.RecordFailedAttempt())
		return
	}
	if err := s.migratePasswordHash("secondary", req.Password); err != nil {
		s.logger.Warn("password_hash_migration_failed", map[string]any{"type": "secondary", "error": err.Error()})
	}
	s.auth.ResetRateLimit()
	s.auth.GrantSecondaryAuth(token, cfg.Security.SecondaryAuthTimeout)

	// 无论 timeout 是否为 0，都生成一个一次性 token 供当前操作使用
	oneTimeToken, err := s.auth.GenerateSecondaryToken(token)
	if err != nil {
		s.writeError(w, err)
		return
	}

	resp := map[string]any{"ok": true, "secondaryToken": oneTimeToken}
	if cfg.Security.SecondaryAuthTimeout > 0 {
		resp["message"] = fmt.Sprintf("二次验证成功，%d 分钟内无需重复验证", cfg.Security.SecondaryAuthTimeout)
	} else {
		resp["message"] = "二次验证成功"
	}
	s.logger.Info("auth_secondary", nil)
	s.writeJSON(w, resp)
}

// handleAuthSetPassword 设置或修改锁屏密码和二次密码。
// 请求体格式:
//
//	{ "type": "lock"|"secondary", "action": "set"|"change"|"remove", "oldPassword": "", "newPassword": "" }
func (s *SearchServer) handleAuthSetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type        string `json:"type"`        // "lock" or "secondary"
		Action      string `json:"action"`      // "set", "change" or "remove"
		OldPassword string `json:"oldPassword"` // 旧密码（change/remove 时必填）
		NewPassword string `json:"newPassword"` // 新密码
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if req.Type != "lock" && req.Type != "secondary" {
		http.Error(w, "invalid type", http.StatusBadRequest)
		return
	}
	if req.Action != "set" && req.Action != "change" && req.Action != "remove" {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}

	cfg := s.cfgMgr.Get()

	// 修改或删除前必须校验现有密码。
	if req.Action == "change" || req.Action == "remove" {
		if status := s.auth.CheckRateLimit(); !status.Allowed {
			s.writeRateLimitBlocked(w, status)
			return
		}
		if req.Type == "lock" {
			if !s.auth.VerifyLockPassword(cfg, req.OldPassword) {
				s.writePasswordFailure(w, "当前锁屏密码错误", s.auth.RecordFailedAttempt())
				return
			}
		} else {
			if !s.auth.VerifySecondaryPassword(cfg, req.OldPassword) {
				s.writePasswordFailure(w, "当前二次密码错误", s.auth.RecordFailedAttempt())
				return
			}
		}
		s.auth.ResetRateLimit()
	} else if req.Action == "set" {
		// set 操作要求该密码尚未设置
		if req.Type == "lock" && s.auth.HasLockPassword(cfg) {
			s.writeJSON(w, map[string]any{"ok": false, "error": "锁屏密码已设置，请使用修改功能"})
			return
		}
		if req.Type == "secondary" && s.auth.HasSecondaryPassword(cfg) {
			s.writeJSON(w, map[string]any{"ok": false, "error": "二次密码已设置，请使用修改功能"})
			return
		}
	}

	if req.Action == "remove" {
		listenHost, _, listenErr := validateListenAddr(cfg.Search.ListenAddr)
		if listenErr == nil && !isLoopbackHost(listenHost) {
			s.writeJSON(w, map[string]any{"ok": false, "error": "当前已开放局域网监听，请先将监听地址改回 127.0.0.1 后再删除密码"})
			return
		}
		if req.Type == "lock" {
			cfg.Security.LockPasswordHash = ""
		} else {
			cfg.Security.SecondaryPasswordHash = ""
		}
		if !isLegacyPasswordHash(cfg.Security.LockPasswordHash) && !isLegacyPasswordHash(cfg.Security.SecondaryPasswordHash) {
			cfg.Security.PasswordSalt = ""
		}
		if err := s.cfgMgr.Update(cfg); err != nil {
			s.writeError(w, err)
			return
		}
		if req.Type == "lock" {
			s.auth.DestroyAllSessionsExcept(GetSessionToken(r))
		} else {
			s.auth.RevokeAllSecondaryAuth()
		}
		s.logger.Info("auth_password_removed", map[string]any{"type": req.Type})
		s.writeJSON(w, map[string]any{"ok": true})
		return
	}

	if req.NewPassword == "" {
		s.writeJSON(w, map[string]any{"ok": false, "error": "新密码不能为空"})
		return
	}
	if err := CheckPasswordStrength(req.NewPassword); err != nil {
		if rpErr, ok := err.(*RecoveryPasswordError); ok {
			s.writeJSON(w, map[string]any{"ok": false, "error": rpErr.Code})
		} else {
			s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		}
		return
	}

	hash, err := newPasswordHash(req.NewPassword)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if req.Type == "lock" {
		cfg.Security.LockPasswordHash = hash
	} else {
		cfg.Security.SecondaryPasswordHash = hash
	}
	if !isLegacyPasswordHash(cfg.Security.LockPasswordHash) && !isLegacyPasswordHash(cfg.Security.SecondaryPasswordHash) {
		cfg.Security.PasswordSalt = ""
	}

	if err := s.cfgMgr.Update(cfg); err != nil {
		s.writeError(w, err)
		return
	}

	// 密码修改后失效旧认证状态
	if req.Type == "lock" {
		// 锁屏密码修改：踢出其他会话，保留当前会话
		s.auth.DestroyAllSessionsExcept(GetSessionToken(r))
	} else {
		// 二次密码修改：撤销所有会话的二次认证，强制重新验证
		s.auth.RevokeAllSecondaryAuth()
	}

	s.logger.Info("auth_password_set", map[string]any{"type": req.Type, "action": req.Action})
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) migratePasswordHash(passwordType, password string) error {
	cfg := s.cfgMgr.Get()
	var current string
	if passwordType == "lock" {
		current = cfg.Security.LockPasswordHash
	} else {
		current = cfg.Security.SecondaryPasswordHash
	}
	if !isLegacyPasswordHash(current) {
		return nil
	}
	hash, err := newPasswordHash(password)
	if err != nil {
		return err
	}
	if passwordType == "lock" {
		cfg.Security.LockPasswordHash = hash
	} else {
		cfg.Security.SecondaryPasswordHash = hash
	}
	if !isLegacyPasswordHash(cfg.Security.LockPasswordHash) && !isLegacyPasswordHash(cfg.Security.SecondaryPasswordHash) {
		cfg.Security.PasswordSalt = ""
	}
	return s.cfgMgr.Update(cfg)
}
