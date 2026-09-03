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

//go:embed web/* web/app/* web/pages/*
var webFS embed.FS

const (
	importReadTimeout       = 60 * time.Minute
	importProcessingTimeout = 60 * time.Minute
	exportWriteTimeout      = 60 * time.Minute
)

type SearchServer struct {
	cfgMgr     *ConfigManager
	store      *Store
	state      *RuntimeState
	logger     *DailyLogger
	auth       *AuthManager
	env        Environment
	server     *http.Server
	url        string
	restartCh  chan struct{}
	backup     *BackupService
	csrfToken  string
	license    *licensing.Manager
	assetMu    sync.Mutex
	recoveryMu sync.Mutex
	startedAt  time.Time
	dataAPI    *DataAPIService
	mcp        *MCPService
	i18n       *I18nService
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
	return &SearchServer{cfgMgr: cfgMgr, store: store, state: state, logger: logger, auth: am, env: env, restartCh: make(chan struct{}, 1), backup: backup, license: license, startedAt: time.Now()}
}

func (s *SearchServer) Start() (string, error) {
	cfg := s.cfgMgr.Get()
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
	api := func(methodPath string, handler http.HandlerFunc) {
		mux.Handle(methodPath, s.envelopeJSON(handler))
	}

	mux.HandleFunc("GET /{$}", s.handleIndex)
	for path := range applicationPages {
		if path == "/" {
			continue
		}
		mux.HandleFunc("GET "+path, s.handleIndex)
	}
	mux.HandleFunc("GET /favicon.ico", s.handleBrandFavicon)
	mux.HandleFunc("GET /brand/logo", s.handleBrandLogo)
	mux.HandleFunc("GET /background/media", s.handleBackgroundMedia)
	for _, asset := range []string{"/tokens.css", "/history.css", "/settings.css", "/settings-data-api.css", "/settings-mcp.css", "/modals.css", "/responsive.css", "/components.css", "/security.css", "/groups.css", "/mobile.css", "/source-apps.css", "/utilities.css", "/logs.css", "/language.css"} {
		mux.HandleFunc("GET "+asset, s.handleStatic)
	}
	mux.HandleFunc("GET /theme-boot.css", s.handleThemeBootCSS)
	mux.HandleFunc("GET /i18n-dict.js", s.handleI18nDictJS)
	mux.HandleFunc("GET /theme-mode.js", s.handleStatic)
	mux.HandleFunc("GET /theme-utils.js", s.handleStatic)
	mux.HandleFunc("GET /hotkey-utils.js", s.handleStatic)
	mux.HandleFunc("GET /i18n.js", s.handleStatic)
	mux.HandleFunc("GET /api.js", s.handleStatic)
	mux.HandleFunc("GET /store.js", s.handleStatic)
	for _, asset := range applicationModuleAssets {
		mux.HandleFunc("GET "+asset, s.handleStatic)
	}
	api("GET /api/v1/session/csrf", s.handleCSRF)
	api("GET /api/v1/appearance", s.handlePublicAppearance)
	api("POST /api/v1/session/login", s.handleAuthLogin)
	api("POST /api/v1/session/logout", s.handleAuthLogout)
	api("GET /api/v1/session", s.handleAuthStatus)
	api("POST /api/v1/session/secondary", s.handleAuthSecondary)
	api("POST /api/v1/session/password", s.requireAuth(s.handleAuthSetPassword))
	api("POST /api/v1/system/restart", s.requireAuth(s.handleRestart))
	api("PUT /api/v1/appearance/logo", s.requireSecondary(s.handleBrandLogoUpload))
	api("DELETE /api/v1/appearance/logo", s.requireSecondary(s.handleBrandLogoReset))
	api("PUT /api/v1/appearance/favicon", s.requireSecondary(s.handleBrandFaviconUpload))
	api("DELETE /api/v1/appearance/favicon", s.requireSecondary(s.handleBrandFaviconReset))
	api("PUT /api/v1/appearance/background", s.requireSecondary(s.handleBackgroundMediaUpload))
	api("DELETE /api/v1/appearance/background", s.requireSecondary(s.handleBackgroundMediaReset))

	api("GET /api/v1/items", s.requireAuth(s.handleSearch))
	api("GET /api/v1/license", s.requireAuth(s.handleLicenseStatus))
	api("GET /api/v1/license/machine", s.requireAuth(s.handleLicenseMachine))
	api("POST /api/v1/license/activation", s.requireAuth(s.handleLicenseActivate))
	api("GET /api/v1/items/sources", s.requireAuth(s.handleSourceExes))
	api("GET /api/v1/runtime", s.requireAuth(s.handleState))
	api("GET /api/v1/system/info", s.requireAuth(s.handleSystemInfo))
	api("POST /api/v1/runtime/pause", s.requireAuth(s.handlePause))
	api("DELETE /api/v1/items", s.requireAuth(s.handleItems))
	api("PATCH /api/v1/items", s.requireAuth(s.handleStarItem))
	api("PATCH /api/v1/items/sensitive", s.requireAuth(s.handleSensitiveMark))
	api("POST /api/v1/items/cleanup-preview", s.requireSecondary(s.handleCleanupPreview))
	api("POST /api/v1/items/cleanup", s.requireSecondary(s.handleCleanup))
	api("GET /api/v1/config", s.requireAuth(s.handleGetConfig))
	api("PUT /api/v1/config", s.requireSecondary(s.handlePutConfig))
	api("GET /api/v1/storage", s.requireAuth(s.handleStorageStatus))
	api("POST /api/v1/storage/relocation", s.requireSecondary(s.handleStorageRelocate))
	api("POST /api/v1/storage/open", s.requireAuth(s.handleStorageOpen))
	api("POST /api/v1/storage/log-directory", s.requireSecondary(s.handleLogDirectoryConfigure))
	api("POST /api/v1/storage/log-directory/open", s.requireAuth(s.handleLogDirectoryOpen))
	api("GET /api/v1/diagnostics", s.requireSecondary(s.handleDiagnosticExport))
	api("GET /api/v1/config/sensitive-rules", s.requireSecondary(s.handleSensitiveRules))
	api("GET /api/v1/items/sensitive", s.requireSecondary(s.handleSensitiveSearch))
	api("GET /api/v1/items/sensitive/item", s.requireSecondary(s.handleSensitiveItem))
	api("GET /api/v1/items/position", s.requireAuth(s.handleItemPosition))
	api("GET /api/v1/items/image", s.requireAuth(s.handleItemImage))
	api("GET /api/v1/items/copy-text", s.requireAuth(s.handleItemCopyText))
	api("GET /api/v1/files/check", s.requireAuth(s.handleFileCheck))
	api("GET /api/v1/files/content", s.requireAuth(s.handleFileServe))
	api("POST /api/v1/files/open-directory", s.requireAuth(s.handleFileOpenDir))
	api("GET /api/v1/files/hash", s.requireAuth(s.handleFileHash))

	api("GET /api/v1/groups", s.requireAuth(s.handleGroupsList))
	api("POST /api/v1/groups", s.requireAuth(s.handleGroupsCreate))
	api("PUT /api/v1/groups", s.requireAuth(s.handleGroupsUpdate))
	api("DELETE /api/v1/groups", s.requireAuth(s.handleGroupsDelete))
	api("POST /api/v1/items/group", s.requireAuth(s.handleItemAddToGroup))
	api("DELETE /api/v1/items/group", s.requireAuth(s.handleItemRemoveFromGroup))
	api("GET /api/v1/items/groups", s.requireAuth(s.handleItemGroups))

	// 多语言：查看/切换为登录态（显示偏好）；创建/编辑/删除为变更操作
	// 需二次认证（与存储、备份等页的变更门控口径一致）。
	api("GET /api/v1/i18n/languages", s.requireAuth(s.handleI18nLanguagesGet))
	api("POST /api/v1/i18n/languages", s.requireSecondary(s.handleI18nLanguagesCreate))
	api("PUT /api/v1/i18n/languages", s.requireSecondary(s.handleI18nLanguagesUpdate))
	api("DELETE /api/v1/i18n/languages", s.requireSecondary(s.handleI18nLanguagesDelete))
	api("PUT /api/v1/i18n/current-language", s.requireAuth(s.handleI18nCurrentLanguagePut))
	api("GET /api/v1/i18n/entries", s.requireAuth(s.handleI18nEntriesGet))
	api("PUT /api/v1/i18n/entries", s.requireSecondary(s.handleI18nEntriesPut))

	api("GET /api/v1/transfer/export", s.requireSecondary(s.handleExportData))
	api("POST /api/v1/transfer/export", s.requireSecondary(s.handleExportData))
	api("POST /api/v1/transfer/import", s.requireSecondary(s.handleImportData))

	api("GET /api/v1/recovery", s.requireSecondary(s.handleRecoveryStatus))
	api("POST /api/v1/recovery/password", s.requireSecondary(s.handleRecoveryChange))
	api("POST /api/v1/recovery/reset", s.requireSecondary(s.handleRecoveryReset))
	api("POST /api/v1/recovery/isolated-switch", s.requireSecondary(s.handleIsolatedDatabaseSwitch))
	api("DELETE /api/v1/recovery/isolated-switch", s.requireSecondary(s.handleIsolatedDatabaseSwitchCancel))

	api("GET /api/v1/data-api", s.requireAuth(s.handleDataAPIStatus))
	api("PUT /api/v1/data-api/config", s.requireSecondary(s.handleDataAPIConfig))
	api("GET /api/v1/data-api/tokens", s.requireAuth(s.handleDataAPITokens))
	api("POST /api/v1/data-api/tokens", s.requireSecondary(s.handleDataAPITokenCreate))
	api("POST /api/v1/data-api/tokens/regenerate", s.requireSecondary(s.handleDataAPITokenRegenerate))
	api("POST /api/v1/data-api/tokens/revoke", s.requireSecondary(s.handleDataAPITokenRevoke))
	api("POST /api/v1/data-api/tokens/sensitive", s.requireSecondary(s.handleDataAPITokenSensitive))
	api("GET /api/v1/data-api/audit", s.requireAuth(s.handleDataAPIAudit))
	api("GET /api/v1/data-api/audit/export", s.requireAuth(s.handleDataAPIAuditExport))
	api("DELETE /api/v1/data-api/audit", s.requireSecondary(s.handleDataAPIAuditClear))

	api("GET /api/v1/mcp", s.requireAuth(s.handleMCPStatus))
	api("PUT /api/v1/mcp/config", s.requireSecondary(s.handleMCPConfig))
	api("GET /api/v1/mcp/tokens", s.requireAuth(s.handleMCPTokens))
	api("POST /api/v1/mcp/tokens", s.requireSecondary(s.handleMCPTokenCreate))
	api("POST /api/v1/mcp/tokens/regenerate", s.requireSecondary(s.handleMCPTokenRegenerate))
	api("POST /api/v1/mcp/tokens/revoke", s.requireSecondary(s.handleMCPTokenRevoke))
	api("POST /api/v1/mcp/tokens/sensitive", s.requireSecondary(s.handleMCPTokenSensitive))
	api("GET /api/v1/mcp/audit", s.requireAuth(s.handleMCPAudit))
	api("GET /api/v1/mcp/audit/export", s.requireAuth(s.handleMCPAuditExport))
	api("DELETE /api/v1/mcp/audit", s.requireSecondary(s.handleMCPAuditClear))

	api("GET /api/v1/logs/config-changes", s.requireAuth(s.handleConfigChangeLog))
	api("DELETE /api/v1/logs/config-changes", s.requireSecondary(s.handleConfigChangeLogClear))
	api("GET /api/v1/logs/searches", s.requireAuth(s.handleSearchHistory))
	api("DELETE /api/v1/logs/searches", s.requireSecondary(s.handleSearchHistoryClear))

	api("GET /api/v1/backup", s.requireAuth(s.handleBackupStatus))
	api("GET /api/v1/backup/history", s.requireSecondary(s.handleBackupHistory))
	api("POST /api/v1/backup/directory", s.requireSecondary(s.handleBackupDirNow))
	api("POST /api/v1/backup/email", s.requireSecondary(s.handleBackupEmailNow))
	api("POST /api/v1/backup/email-test", s.requireSecondary(s.handleBackupEmailTest))

	const maxAttempts = 5
	lastPort := port + maxAttempts - 1
	if lastPort > 65535 {
		lastPort = 65535
	}
	var ln net.Listener
	for p := port; p <= lastPort; p++ {
		addr := net.JoinHostPort(host, strconv.Itoa(p))
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			if p != port {
				s.logger.Warn("search_server_port_fallback", map[string]any{
					"original": cfg.Search.ListenAddr, "actual": addr,
				})
			}
			break
		}
	}
	if err != nil {
		return "", fmt.Errorf("could not bind any port (tried %d..%d): %w", port, lastPort, err)
	}

	addr := ln.Addr().String()
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok && tcp.IP.IsUnspecified() {
		addr = "127.0.0.1:" + strconv.Itoa(tcp.Port)
	}
	s.url = "http://" + addr + "/"
	s.server = &http.Server{
		Handler:           s.secureHTTP(ensureAPIErrorContract(mux), host, ln.Addr().(*net.TCPAddr).Port),
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
