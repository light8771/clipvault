package clipvault

import (
	"clipboard_ext/internal/licensing"
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

//go:embed web/*
var webFS embed.FS

const (
	importReadTimeout       = 60 * time.Minute
	importProcessingTimeout = 60 * time.Minute
	exportWriteTimeout      = 60 * time.Minute
)

type SearchServer struct {
	cfgMgr       *ConfigManager
	store        *Store
	state        *RuntimeState
	logger       *DailyLogger
	auth         *AuthManager
	env          Environment
	server       *http.Server
	url          string
	restartCh    chan struct{}
	backup       *BackupService
	csrfToken    string
	license      *licensing.Manager
	assetMu      sync.Mutex
	recoveryMu   sync.Mutex
	reverseProxy ReverseProxyConfig
}

type freshSecondaryContextKey struct{}

func (s *SearchServer) configuredTimeZone() string {
	if s.cfgMgr == nil {
		return "system"
	}
	return s.cfgMgr.Get().Search.TimeZone
}

func NewSearchServer(cfgMgr *ConfigManager, store *Store, state *RuntimeState, logger *DailyLogger, env Environment, backup *BackupService, license *licensing.Manager) *SearchServer {
	am := NewAuthManager()
	am.startSessionCleaner()
	return &SearchServer{cfgMgr: cfgMgr, store: store, state: state, logger: logger, auth: am, env: env, restartCh: make(chan struct{}, 1), backup: backup, license: license}
}

func (s *SearchServer) Start() (string, error) {
	cfg := s.cfgMgr.Get()
	s.reverseProxy = cfg.Search.ReverseProxy
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

	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /favicon.ico", s.handleBrandFavicon)
	mux.HandleFunc("GET /brand/logo", s.handleBrandLogo)
	mux.HandleFunc("GET /background/media", s.handleBackgroundMedia)
	mux.HandleFunc("GET /style.css", s.handleStatic)
	mux.HandleFunc("GET /theme-utils.js", s.handleStatic)
	mux.HandleFunc("GET /hotkey-utils.js", s.handleStatic)
	mux.HandleFunc("GET /script.js", s.handleStatic)
	mux.HandleFunc("GET /api/csrf", s.handleCSRF)
	mux.HandleFunc("GET /api/appearance", s.handlePublicAppearance)
	mux.HandleFunc("POST /api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/secondary", s.handleAuthSecondary)
	mux.HandleFunc("POST /api/auth/password", s.requireAuth(s.handleAuthSetPassword))
	mux.HandleFunc("POST /api/restart", s.requireAuth(s.handleRestart))
	mux.HandleFunc("PUT /api/brand/logo", s.requireSecondary(s.handleBrandLogoUpload))
	mux.HandleFunc("DELETE /api/brand/logo", s.requireSecondary(s.handleBrandLogoReset))
	mux.HandleFunc("PUT /api/brand/favicon", s.requireSecondary(s.handleBrandFaviconUpload))
	mux.HandleFunc("DELETE /api/brand/favicon", s.requireSecondary(s.handleBrandFaviconReset))
	mux.HandleFunc("PUT /api/background/media", s.requireSecondary(s.handleBackgroundMediaUpload))
	mux.HandleFunc("DELETE /api/background/media", s.requireSecondary(s.handleBackgroundMediaReset))

	mux.HandleFunc("GET /api/search", s.requireAuth(s.handleSearch))
	mux.HandleFunc("GET /api/license/status", s.requireAuth(s.handleLicenseStatus))
	mux.HandleFunc("GET /api/license/machine", s.requireAuth(s.handleLicenseMachine))
	mux.HandleFunc("POST /api/license/activate", s.requireAuth(s.handleLicenseActivate))
	mux.HandleFunc("GET /api/source-exes", s.requireAuth(s.handleSourceExes))
	mux.HandleFunc("GET /api/state", s.requireAuth(s.handleState))
	mux.HandleFunc("POST /api/pause", s.requireAuth(s.handlePause))
	mux.HandleFunc("DELETE /api/items", s.requireAuth(s.handleItems))
	mux.HandleFunc("PATCH /api/items", s.requireAuth(s.handleStarItem))
	mux.HandleFunc("POST /api/clear", s.requireSecondary(s.handleClear))
	mux.HandleFunc("GET /api/config", s.requireAuth(s.handleGetConfig))
	mux.HandleFunc("PUT /api/config", s.requireSecondary(s.handlePutConfig))
	mux.HandleFunc("GET /api/storage/status", s.requireAuth(s.handleStorageStatus))
	mux.HandleFunc("POST /api/storage/relocate", s.requireSecondary(s.handleStorageRelocate))
	mux.HandleFunc("POST /api/storage/open", s.requireAuth(s.handleStorageOpen))
	mux.HandleFunc("GET /api/diagnostics/export", s.requireSecondary(s.handleDiagnosticExport))
	mux.HandleFunc("GET /api/sensitive/rules", s.requireSecondary(s.handleSensitiveRules))
	mux.HandleFunc("GET /api/search/sensitive", s.requireSecondary(s.handleSensitiveSearch))
	mux.HandleFunc("GET /api/items/sensitive", s.requireSecondary(s.handleSensitiveItem))
	mux.HandleFunc("GET /api/items/position", s.requireAuth(s.handleItemPosition))
	mux.HandleFunc("GET /api/file/check", s.requireAuth(s.handleFileCheck))
	mux.HandleFunc("GET /api/file", s.requireAuth(s.handleFileServe))
	mux.HandleFunc("POST /api/file/opendir", s.requireAuth(s.handleFileOpenDir))
	mux.HandleFunc("GET /api/file/hash", s.requireAuth(s.handleFileHash))

	mux.HandleFunc("GET /api/groups", s.requireAuth(s.handleGroupsList))
	mux.HandleFunc("POST /api/groups", s.requireAuth(s.handleGroupsCreate))
	mux.HandleFunc("PUT /api/groups", s.requireAuth(s.handleGroupsUpdate))
	mux.HandleFunc("DELETE /api/groups", s.requireAuth(s.handleGroupsDelete))
	mux.HandleFunc("POST /api/items/group", s.requireAuth(s.handleItemAddToGroup))
	mux.HandleFunc("DELETE /api/items/group", s.requireAuth(s.handleItemRemoveFromGroup))
	mux.HandleFunc("GET /api/items/groups", s.requireAuth(s.handleItemGroups))

	mux.HandleFunc("GET /api/export/data", s.requireSecondary(s.handleExportData))
	mux.HandleFunc("POST /api/export/data", s.requireSecondary(s.handleExportData))
	mux.HandleFunc("POST /api/import/data", s.requireSecondary(s.handleImportData))

	mux.HandleFunc("GET /api/recovery/status", s.requireSecondary(s.handleRecoveryStatus))
	mux.HandleFunc("POST /api/recovery/change", s.requireSecondary(s.handleRecoveryChange))
	mux.HandleFunc("POST /api/recovery/reset", s.requireSecondary(s.handleRecoveryReset))
	mux.HandleFunc("POST /api/recovery/isolated/switch", s.requireSecondary(s.handleIsolatedDatabaseSwitch))
	mux.HandleFunc("DELETE /api/recovery/isolated/switch", s.requireSecondary(s.handleIsolatedDatabaseSwitchCancel))

	mux.HandleFunc("GET /api/backup/status", s.requireAuth(s.handleBackupStatus))
	mux.HandleFunc("GET /api/backup/email/history", s.requireSecondary(s.handleEmailBackupHistory))
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
		Handler:           s.secureHTTP(mux, host, ln.Addr().(*net.TCPAddr).Port),
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

func (s *SearchServer) URL() string {
	return s.url
}

func (s *SearchServer) Close(ctx context.Context) error {

	if s.auth != nil {
		s.auth.Close()
	}
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
