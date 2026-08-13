package clipvault

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"clipboard_ext/internal/licensing"
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

	// 公开端点（不需要锁屏认证）
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

	// 需要锁屏认证的端点
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

	// 分组管理端点
	mux.HandleFunc("GET /api/groups", s.requireAuth(s.handleGroupsList))
	mux.HandleFunc("POST /api/groups", s.requireAuth(s.handleGroupsCreate))
	mux.HandleFunc("PUT /api/groups", s.requireAuth(s.handleGroupsUpdate))
	mux.HandleFunc("DELETE /api/groups", s.requireAuth(s.handleGroupsDelete))
	mux.HandleFunc("POST /api/items/group", s.requireAuth(s.handleItemAddToGroup))
	mux.HandleFunc("DELETE /api/items/group", s.requireAuth(s.handleItemRemoveFromGroup))
	mux.HandleFunc("GET /api/items/groups", s.requireAuth(s.handleItemGroups))

	// 导出/导入端点（需二次认证）
	mux.HandleFunc("GET /api/export/data", s.requireSecondary(s.handleExportData))
	mux.HandleFunc("POST /api/export/data", s.requireSecondary(s.handleExportData))
	mux.HandleFunc("POST /api/import/data", s.requireSecondary(s.handleImportData))

	// 恢复密码管理端点（需二次认证）
	mux.HandleFunc("GET /api/recovery/status", s.requireSecondary(s.handleRecoveryStatus))
	mux.HandleFunc("POST /api/recovery/change", s.requireSecondary(s.handleRecoveryChange))
	mux.HandleFunc("POST /api/recovery/reset", s.requireSecondary(s.handleRecoveryReset))
	mux.HandleFunc("POST /api/recovery/isolated/switch", s.requireSecondary(s.handleIsolatedDatabaseSwitch))
	mux.HandleFunc("DELETE /api/recovery/isolated/switch", s.requireSecondary(s.handleIsolatedDatabaseSwitchCancel))

	// 备份管理端点（需二次认证）
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
	// 先停 cleaner goroutine，再关 HTTP server（#23：防 Web 重启泄漏 goroutine）
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

func (s *SearchServer) handleStorageStatus(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, s.env.StorageStatus())
}

func (s *SearchServer) handleStorageRelocate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DataDir string `json:"dataDir"`
	}
	if err := decodeJSONRequest(w, r, &req, 4<<10); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]any{"ok": false, "error": "数据目录请求无效"})
		return
	}
	if err := ScheduleDataDirectoryMove(s.env, req.DataDir); err != nil {
		// 原始 err（文件系统/权限）只进日志，客户端给可读文案
		s.writeAPIError(w, http.StatusBadRequest, "relocate_failed", "数据目录迁移失败："+err.Error(), err)
		return
	}
	s.logger.Info("storage_relocation_scheduled", nil)
	s.writeJSON(w, map[string]any{"ok": true, "message": "数据目录迁移已准备完成，请退出应用后重新启动"})
}

// handleStorageOpen opens the current data directory in the system file explorer.
func (s *SearchServer) handleStorageOpen(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(s.env.DataDir)
	if dir == "" {
		s.writeError(w, fmt.Errorf("数据目录不可用"))
		return
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		s.writeError(w, fmt.Errorf("数据目录不存在或不可访问: %s", dir))
		return
	}
	if err := openDirectory(dir); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) handleDiagnosticExport(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	archive, err := os.CreateTemp("", "clipvault-logs-*.zip")
	if err != nil {
		s.writeError(w, err)
		return
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	defer archive.Close()

	if err := WriteDiagnosticArchive(archive, s.env, now); err != nil {
		s.writeError(w, err)
		return
	}
	if err := archive.Close(); err != nil {
		s.writeError(w, err)
		return
	}

	download, err := os.Open(archivePath)
	if err != nil {
		s.writeError(w, err)
		return
	}
	defer download.Close()
	info, err := download.Stat()
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", diagnosticArchiveName(now)))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if _, err := io.Copy(w, download); err != nil {
		s.logger.Warn("diagnostics_export_write_failed", map[string]any{"error": err.Error()})
		return
	}
	s.logger.Info("diagnostics_exported", nil)
}

func (s *SearchServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, "web/index.html", "text/html; charset=utf-8")
}

const maxBrandAssetBytes = 10 << 20

func (s *SearchServer) handleBrandLogo(w http.ResponseWriter, r *http.Request) {
	s.serveBrandAsset(w, r, "logo")
}

func (s *SearchServer) handleBrandFavicon(w http.ResponseWriter, r *http.Request) {
	s.serveBrandAsset(w, r, "favicon")
}

func (s *SearchServer) handleBackgroundMedia(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get().Background
	if !isBackgroundContentType(cfg.ContentType) || !assetVersionMatches(r, cfg.Version) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	fileName := assetFileName(backgroundMediaAsset, cfg.Version)
	file, err := os.Open(filepath.Join(s.env.DataDir, fileName))
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", cfg.ContentType)
	w.Header().Set("Cache-Control", assetCacheControl(r))
	http.ServeContent(w, r, fileName, time.Time{}, file)
}

func (s *SearchServer) serveBrandAsset(w http.ResponseWriter, r *http.Request, kind string) {
	cfg := s.cfgMgr.Get().Brand
	contentType := cfg.LogoContentType
	assetKind := brandLogoAsset
	version := cfg.LogoVersion
	if kind == "favicon" {
		contentType = cfg.FaviconContentType
		assetKind = brandFaviconAsset
		version = cfg.FaviconVersion
	}
	if !isBrandContentType(contentType, kind) || !assetVersionMatches(r, version) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	data, err := os.ReadFile(filepath.Join(s.env.DataDir, assetFileName(assetKind, version)))
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", assetCacheControl(r))
	_, _ = w.Write(data)
}

func (s *SearchServer) handleBrandLogoUpload(w http.ResponseWriter, r *http.Request) {
	s.handleBrandAssetUpload(w, r, "logo")
}

func (s *SearchServer) handleBrandFaviconUpload(w http.ResponseWriter, r *http.Request) {
	s.handleBrandAssetUpload(w, r, "favicon")
}

func (s *SearchServer) handleBackgroundMediaUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBrandAssetBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_media", "无效的背景媒体", nil)
		return
	}
	contentType := detectBackgroundContentType(data)
	if contentType == "" {
		s.writeAPIError(w, http.StatusBadRequest, "unsupported_media", "不支持的背景媒体格式", nil)
		return
	}
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	cfg := s.cfgMgr.Get()
	previousVersion := cfg.Background.Version
	version := nextAssetVersion(previousVersion)
	fileName := assetFileName(backgroundMediaAsset, version)
	if err := writeBrandAsset(filepath.Join(s.env.DataDir, fileName), data); err != nil {
		s.writeError(w, err)
		return
	}
	cfg.Background.ContentType = contentType
	cfg.Background.Version = version
	runtimeError, hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		_ = os.Remove(filepath.Join(s.env.DataDir, fileName))
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(backgroundMediaAsset, previousVersion)))
	}
	response := map[string]any{"ok": true, "background": s.cfgMgr.Get().Background}
	addRuntimeWarnings(response, runtimeError, hotkeyError)
	s.writeJSON(w, response)
}

func (s *SearchServer) handleBrandAssetUpload(w http.ResponseWriter, r *http.Request, kind string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBrandAssetBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_image", "无效的图片", nil)
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if contentType == "image/vnd.microsoft.icon" {
		contentType = "image/x-icon"
	}
	if !isBrandContentType(contentType, kind) || !isValidBrandImage(data, contentType) {
		s.writeAPIError(w, http.StatusBadRequest, "unsupported_image", "不支持的图片格式", nil)
		return
	}
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	cfg := s.cfgMgr.Get()
	assetKind := brandLogoAsset
	previousVersion := cfg.Brand.LogoVersion
	if kind == "favicon" {
		assetKind = brandFaviconAsset
		previousVersion = cfg.Brand.FaviconVersion
	}
	version := nextAssetVersion(previousVersion)
	fileName := assetFileName(assetKind, version)
	if err := writeBrandAsset(filepath.Join(s.env.DataDir, fileName), data); err != nil {
		s.writeError(w, err)
		return
	}
	if kind == "favicon" {
		cfg.Brand.FaviconContentType = contentType
		cfg.Brand.FaviconVersion = version
	} else {
		cfg.Brand.LogoContentType = contentType
		cfg.Brand.LogoVersion = version
	}
	runtimeError, hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		_ = os.Remove(filepath.Join(s.env.DataDir, fileName))
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(assetKind, previousVersion)))
	}
	response := map[string]any{"ok": true, "brand": s.cfgMgr.Get().Brand}
	addRuntimeWarnings(response, runtimeError, hotkeyError)
	s.writeJSON(w, response)
}

func (s *SearchServer) handleBrandLogoReset(w http.ResponseWriter, r *http.Request) {
	s.handleBrandAssetReset(w, "logo")
}

func (s *SearchServer) handleBrandFaviconReset(w http.ResponseWriter, r *http.Request) {
	s.handleBrandAssetReset(w, "favicon")
}

func (s *SearchServer) handleBackgroundMediaReset(w http.ResponseWriter, r *http.Request) {
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	cfg := s.cfgMgr.Get()
	previousVersion := cfg.Background.Version
	cfg.Background.ContentType = ""
	cfg.Background.Version = 0
	runtimeError, hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(backgroundMediaAsset, previousVersion)))
	}
	response := map[string]any{"ok": true, "background": s.cfgMgr.Get().Background}
	addRuntimeWarnings(response, runtimeError, hotkeyError)
	s.writeJSON(w, response)
}

func (s *SearchServer) handleBrandAssetReset(w http.ResponseWriter, kind string) {
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	cfg := s.cfgMgr.Get()
	assetKind := brandLogoAsset
	previousVersion := cfg.Brand.LogoVersion
	if kind == "favicon" {
		assetKind = brandFaviconAsset
		previousVersion = cfg.Brand.FaviconVersion
		cfg.Brand.FaviconContentType = ""
		cfg.Brand.FaviconVersion = 0
	} else {
		cfg.Brand.LogoContentType = ""
		cfg.Brand.LogoVersion = 0
	}
	runtimeError, hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(assetKind, previousVersion)))
	}
	response := map[string]any{"ok": true, "brand": s.cfgMgr.Get().Brand}
	addRuntimeWarnings(response, runtimeError, hotkeyError)
	s.writeJSON(w, response)
}

// updateAssetConfig distinguishes persistence failures from post-commit runtime
// listener failures so callers never delete an asset referenced by saved config.
func (s *SearchServer) updateAssetConfig(cfg Config) (runtimeError, hotkeyError string, err error) {
	err = s.cfgMgr.Update(cfg)
	if err == nil {
		return "", "", nil
	}
	var applyError *ConfigRuntimeApplyError
	if !errors.As(err, &applyError) {
		return "", "", err
	}
	var hkErr *HotkeyError
	if errors.As(applyError, &hkErr) {
		hotkeyError = hkErr.Error()
	}
	return applyError.Error(), hotkeyError, nil
}

func addRuntimeWarnings(response map[string]any, runtimeError, hotkeyError string) {
	if runtimeError != "" {
		response["runtimeError"] = runtimeError
	}
	if hotkeyError != "" {
		response["hotkeyError"] = hotkeyError
	}
}

func isValidBrandImage(data []byte, contentType string) bool {
	if contentType == "image/x-icon" {
		return isValidICO(data)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 4096 || config.Height > 4096 {
		return false
	}
	return (contentType == "image/png" && format == "png") || (contentType == "image/jpeg" && format == "jpeg")
}

func isValidBackgroundMedia(data []byte, contentType string) bool {
	return detectBackgroundContentType(data) == contentType
}

// detectBackgroundContentType trusts the media bytes rather than a browser's
// optional MIME hint. Some Windows file pickers report PNG files as generic
// application/octet-stream even when the file itself is valid.
func detectBackgroundContentType(data []byte) string {
	if config, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil && config.Width > 0 && config.Height > 0 && config.Width <= 4096 && config.Height <= 4096 {
		switch format {
		case "png":
			return "image/png"
		case "jpeg":
			return "image/jpeg"
		}
	}
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		return "video/mp4"
	}
	if len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		return "video/webm"
	}
	return ""
}

func isValidICO(data []byte) bool {
	if len(data) < 22 || !bytes.Equal(data[:4], []byte{0, 0, 1, 0}) {
		return false
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || len(data) < 6+count*16 {
		return false
	}
	for index := 0; index < count; index++ {
		entry := data[6+index*16 : 22+index*16]
		size := int(binary.LittleEndian.Uint32(entry[8:12]))
		offset := int(binary.LittleEndian.Uint32(entry[12:16]))
		if size > 0 && offset >= 6+count*16 && offset <= len(data)-size {
			return true
		}
	}
	return false
}

func writeBrandAsset(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".brand-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func nextAssetVersion(previousVersion int64) int64 {
	next := time.Now().UnixNano()
	if next <= previousVersion {
		return previousVersion + 1
	}
	return next
}

func (s *SearchServer) handleCSRF(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{"token": s.csrfToken})
}

func (s *SearchServer) handleLicenseStatus(w http.ResponseWriter, r *http.Request) {
	if s.license == nil {
		s.writeJSON(w, map[string]any{"licensed": true, "status": "disabled", "enforced": false, "freeLimit": FreeTierVisibleLimit})
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
		"freeLimit":   FreeTierVisibleLimit,
	})
}

func (s *SearchServer) handleLicenseMachine(w http.ResponseWriter, r *http.Request) {
	if s.license == nil {
		s.writeAPIError(w, http.StatusServiceUnavailable, "license_unavailable", "授权管理器不可用", nil)
		return
	}
	ctx := s.license.Context()
	if ctx.MachineID == "" {
		s.writeAPIError(w, http.StatusServiceUnavailable, "machine_id_unavailable", "无法获取机器标识", nil)
		return
	}
	s.writeJSON(w, map[string]any{"machineId": ctx.MachineID, "machineCode": ctx.MachineCode})
}

func (s *SearchServer) handleLicenseActivate(w http.ResponseWriter, r *http.Request) {
	if s.license == nil {
		s.writeAPIError(w, http.StatusServiceUnavailable, "license_unavailable", "授权管理器不可用", nil)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSONRequest(w, r, &req, 8<<10); err != nil || strings.TrimSpace(req.Token) == "" {
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
		s.writeAPIError(w, http.StatusNotFound, "not_found", "资源不存在", nil)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *SearchServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	limit, offset, sourceExeNames, err := parseSearchParameters(r)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_search_parameters", "搜索参数无效", err)
		return
	}
	groupID, err := parseOptionalPositiveInt64(r.URL.Query().Get("group_id"), "group_id")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_id", "分组 id 无效", err)
		return
	}
	cfg := s.cfgMgr.Get()
	token := GetSessionToken(r)
	secondaryReadable := !s.auth.HasSecondaryPassword(cfg) || s.auth.HasSecondaryAuth(token)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := r.URL.Query().Get("kind")
	starredOnly, err := parseOptionalFlag(r.URL.Query().Get("starred"), "starred")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_starred_filter", "收藏筛选参数无效", err)
		return
	}
	dateFrom := r.URL.Query().Get("date_from")
	dateTo := r.URL.Query().Get("date_to")
	createdFrom, createdBefore, err := localDateFilterBounds(r.URL.Query().Get("date_from"), r.URL.Query().Get("date_to"), s.configuredTimeZone())
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_date_range", "日期范围无效", err)
		return
	}
	hasContentOracleFilter := query != "" || kind != "" || starredOnly || dateFrom != "" || dateTo != "" || groupID != 0 || len(sourceExeNames) != 0
	opts := SearchOptions{
		Query:         query,
		Limit:         limit,
		Offset:        offset,
		StarredOnly:   starredOnly,
		Kind:          kind,
		CreatedFrom:   createdFrom,
		CreatedBefore: createdBefore,
		// 未完成二次认证时，无筛选列表仍显示遮蔽占位；任何搜索或筛选
		// 都排除敏感记录，避免通过“是否命中”或计数推断敏感内容。
		IncludeSensitive: secondaryReadable || !hasContentOracleFilter,
		GroupID:          groupID,
		SourceExeNames:   sourceExeNames,        // 可重复参数，值为真实 exe 名；限制条数防超长 IN
		ExeAliases:       cfg.Source.ExeAliases, // 注入当前别名
	}
	resp := map[string]any{}
	// 未激活（免费版）：读取窗口限制为最近 FreeTierVisibleLimit 条，
	// 并额外返回全量总数 fullTotal 供前端 badge 展示（转化钩子）。
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		opts.RestrictToLatestN = FreeTierVisibleLimit
		if ft, err := s.store.CountAll(r.Context()); err == nil {
			resp["fullTotal"] = ft
		}
	}
	items, total, err := s.store.Search(r.Context(), opts)
	if err != nil {
		s.writeError(w, err)
		return
	}
	// 未设置二次密码时不需要遮蔽；已通过二次认证时也不遮蔽
	if s.auth.HasSecondaryPassword(cfg) && !s.auth.HasSecondaryAuth(token) {
		for i := range items {
			if items[i].Sensitive {
				items[i].Text = "【敏感内容，点击查看】"
				// 遮蔽衍生字段：MetaJSON（文件类含完整本地路径）、SHA256、
				// Format、ByteLen 一并清空，防止绕过 Text 遮蔽泄漏敏感信息
				// （#20：原先只遮蔽 Text，meta_json 直接暴露文件路径）。
				// 来源窗口标题常含文档名、exe 路径含用户名，同属内容衍生信息，
				// 一并遮蔽；exe 名只是应用标识（如同 CharLen），保留用于展示。
				// 注：前端文件类型判断用 it.kind（isNonTextKind），不消费 it.format，
				// 清 Format 不影响渲染；CharLen 保留（前端显示"xx 字"）。
				items[i].MetaJSON = ""
				items[i].SHA256 = ""
				items[i].Format = ""
				items[i].ByteLen = 0
				items[i].SourceWindowTitle = ""
				items[i].SourceExePath = ""
			}
		}
	}
	resp["items"] = items
	resp["total"] = total
	s.writeJSON(w, resp)
}

// handleSourceExes 返回所有来源应用（原始 exe 名）及其记录数，供搜索下拉与别名/白名单编辑使用。
func (s *SearchServer) handleSourceExes(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	includeSensitive := !s.auth.HasSecondaryPassword(cfg) || s.auth.HasSecondaryAuth(GetSessionToken(r))
	restrictToLatestN := 0
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		restrictToLatestN = FreeTierVisibleLimit
	}
	stats, err := s.store.SourceExeStatsFiltered(r.Context(), includeSensitive, restrictToLatestN)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if stats == nil {
		stats = []ExeStat{}
	}
	s.writeJSON(w, stats)
}

// maxSourceExeFilters 限制来源应用多选过滤参数的条数，防止构造超长 IN 列表。
const maxSourceExeFilters = 50

const (
	maxSearchQueryBytes    = 4096
	maxSourceExeFilterSize = 260
)

func parseSearchParameters(r *http.Request) (limit, offset int, sourceExeNames []string, err error) {
	query := r.URL.Query().Get("q")
	if len(query) > maxSearchQueryBytes {
		return 0, 0, nil, fmt.Errorf("q exceeds %d bytes", maxSearchQueryBytes)
	}
	if _, err := splitSearchTerms(query); err != nil {
		return 0, 0, nil, err
	}
	limit, err = parseOptionalInt(r.URL.Query().Get("limit"), "limit", 1, 500)
	if err != nil {
		return 0, 0, nil, err
	}
	offset, err = parseOptionalInt(r.URL.Query().Get("offset"), "offset", 0, 0)
	if err != nil {
		return 0, 0, nil, err
	}
	sourceExeNames = r.URL.Query()["source_exe"]
	if len(sourceExeNames) > maxSourceExeFilters {
		return 0, 0, nil, fmt.Errorf("source_exe exceeds %d values", maxSourceExeFilters)
	}
	for _, name := range sourceExeNames {
		if name == "" || len(name) > maxSourceExeFilterSize {
			return 0, 0, nil, fmt.Errorf("invalid source_exe value")
		}
	}
	return limit, offset, sourceExeNames, nil
}

func parseOptionalInt(raw, name string, minValue, maxValue int) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || (maxValue > 0 && value > maxValue) {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

func parseOptionalPositiveInt64(raw, name string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

func parseOptionalFlag(raw, name string) (bool, error) {
	switch raw {
	case "", "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("invalid %s", name)
	}
}

// handleSensitiveSearch 仅返回敏感记录，需要二次认证。
func (s *SearchServer) handleSensitiveSearch(w http.ResponseWriter, r *http.Request) {
	limit, offset, sourceExeNames, err := parseSearchParameters(r)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_search_parameters", "搜索参数无效", err)
		return
	}
	cfg := s.cfgMgr.Get()
	groupID, err := parseOptionalPositiveInt64(r.URL.Query().Get("group_id"), "group_id")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_id", "分组 id 无效", err)
		return
	}
	starredOnly, err := parseOptionalFlag(r.URL.Query().Get("starred"), "starred")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_starred_filter", "收藏筛选参数无效", err)
		return
	}
	createdFrom, createdBefore, err := localDateFilterBounds(r.URL.Query().Get("date_from"), r.URL.Query().Get("date_to"), s.configuredTimeZone())
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_date_range", "日期范围无效", err)
		return
	}
	opts := SearchOptions{
		Query:         r.URL.Query().Get("q"),
		Limit:         limit,
		Offset:        offset,
		Kind:          r.URL.Query().Get("kind"),
		StarredOnly:   starredOnly,
		CreatedFrom:   createdFrom,
		CreatedBefore: createdBefore,
		SensitiveOnly: true,
		GroupID:       groupID,
		// 敏感搜索同样支持来源应用过滤（#8）：显式 source_exe 参数按原始名精确筛选，
		// 并注入当前别名配置，与 /api/search 行为一致。
		SourceExeNames: sourceExeNames,
		ExeAliases:     cfg.Source.ExeAliases,
	}
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		opts.RestrictToLatestN = FreeTierVisibleLimit
	}
	items, total, err := s.store.Search(r.Context(), opts)
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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的 id", nil)
		return
	}
	item, err := s.store.GetByID(r.Context(), id)
	if err != nil {
		s.writeItemLookupError(w, err)
		return
	}
	if !item.Sensitive {
		s.writeAPIError(w, http.StatusBadRequest, "not_sensitive", "该记录不是敏感内容", nil)
		return
	}
	// 免费版读窗口：未激活时仅允许查看最近 FreeTierVisibleLimit 条，
	// 与 handleItemPosition 的门控保持一致，防止按 id 枚举窗口外敏感记录。
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		pos, perr := s.store.GetPositionByID(r.Context(), id)
		if perr != nil {
			s.writeError(w, perr)
			return
		}
		if pos >= FreeTierVisibleLimit {
			s.writeAPIError(w, http.StatusPaymentRequired, "window_outside",
				"未激活仅可查看最近 "+strconv.Itoa(FreeTierVisibleLimit)+" 条记录，该记录不在可查看范围内", nil)
			return
		}
	}
	s.writeJSON(w, map[string]any{"item": item})
}

func (s *SearchServer) handleItemPosition(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的 id", nil)
		return
	}
	pos, err := s.store.GetPositionByID(r.Context(), id)
	if err != nil {
		s.writeItemLookupError(w, err)
		return
	}
	// 未激活时，窗口外的记录无法定位（前端列表里根本看不到）
	// 统一 402 语义（与 handleSensitiveItem/enforceFreeWindow 一致，#19）
	if s.license != nil && !s.license.CanUsePaidFeatures() && pos >= FreeTierVisibleLimit {
		s.writeAPIError(w, http.StatusPaymentRequired, "window_outside",
			"未激活仅可查看最近 "+strconv.Itoa(FreeTierVisibleLimit)+" 条记录，该记录不在可查看范围内", nil)
		return
	}
	s.writeJSON(w, map[string]any{"position": pos})
}

// enforceFreeWindow 免费版（未激活）写操作门控：与读窗口语义一致，
// 窗口外（最近 FreeTierVisibleLimit 条之外）的记录不允许删除/收藏等写操作。
// 返回 true 表示已拦截并写出 402 响应，调用方应直接 return。
// 与 handleSensitiveItem/handleItemPosition 的读窗口门控共用 GetPositionByID 判定。
func (s *SearchServer) enforceFreeWindow(w http.ResponseWriter, r *http.Request, id int64) bool {
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		pos, perr := s.store.GetPositionByID(r.Context(), id)
		if perr != nil {
			s.writeItemLookupError(w, perr)
			return true
		}
		if pos >= FreeTierVisibleLimit {
			s.writeAPIError(w, http.StatusPaymentRequired, "window_outside",
				"未激活仅可操作最近 "+strconv.Itoa(FreeTierVisibleLimit)+" 条记录，该记录不在可操作范围内", nil)
			return true
		}
	}
	return false
}

// FreeTierRestriction 返回免费版（未激活）可见窗口限制是否生效：未激活用户只能
// 浏览/检索最近 FreeTierVisibleLimit 条记录。CLI（cmd/clipvault/main.go 的 search）
// 通过本函数判定，与 Web handler 基于 license.CanUsePaidFeatures 的判定等价，
// 避免 CLI 各自硬编码而绕过窗口。receipt 路径与 app_windows.go 的 license 加载一致：
// <DataDir>/license/clipvault.lic。注意：本函数会从磁盘重新加载 license 状态。
func FreeTierRestriction(env Environment) bool {
	receiptPath := filepath.Join(env.DataDir, "license", "clipvault.lic")
	lm := licensing.NewManager(receiptPath, licensing.BuiltinTrustedKeys())
	lm.Load()
	return !lm.CanUsePaidFeatures()
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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的 id", nil)
		return
	}
	if !s.authorizeItemAccess(w, r, id) {
		return
	}
	if err := s.store.Delete(r.Context(), id); err != nil {
		s.writeItemLookupError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) handleStarItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的 id", nil)
		return
	}
	if !s.authorizeItemAccess(w, r, id) {
		return
	}
	starred := r.URL.Query().Get("starred") != "0"
	if err := s.store.Star(r.Context(), id, starred); err != nil {
		s.writeItemLookupError(w, err)
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

// handlePublicAppearance exposes only visual settings required before a
// lock-screen session has been authenticated.
func (s *SearchServer) handlePublicAppearance(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	s.writeJSON(w, map[string]any{
		"brand":      cfg.Brand,
		"background": cfg.Background,
		"theme":      cfg.Theme,
	})
}

// stripSecrets clears password hashes before returning configuration to the client.
func stripSecrets(cfg Config) Config {
	cfg.Security.LockPasswordHash = ""
	cfg.Security.SecondaryPasswordHash = ""
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

// resolveSecretField 解析前端回传的敏感密码字段。
//   - "" / "__set__"：保留当前值（前端未修改或占位符回传）
//   - "__clear__"：用户主动清除，置空
//   - 其他值：作为新密码写入
func resolveSecretField(newVal, curVal string) string {
	switch newVal {
	case "", "__set__":
		return curVal
	case "__clear__":
		return ""
	default:
		return newVal
	}
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
	if err := decodeJSONRequest(w, r, &cfg, 1<<20); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "配置格式错误", err)
		return
	}
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	curCfg := s.cfgMgr.Get()
	preserveAssetMetadata(&cfg, curCfg)
	listenHost, _, err := validateListenAddr(cfg.Search.ListenAddr)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_listen_addr", "监听地址无效: "+err.Error(), err)
		return
	}
	if _, err := configuredLocation(cfg.Search.TimeZone); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_time_zone", "日期筛选时区无效，请填写 system 或有效 IANA 时区", err)
		return
	}
	if (!isLoopbackHost(listenHost) || cfg.Search.ReverseProxy.Enabled) && !s.auth.HasLockPassword(curCfg) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]any{
			"ok":    false,
			"error": "开放局域网或反向代理访问前，必须先设置锁屏密码",
		})
		return
	}
	// 前端可能在未通过二次认证时提交遮蔽后的 patterns（__masked__），保留原值
	for _, p := range cfg.Sensitive.CustomPatterns {
		if p == "__masked__" {
			cfg.Sensitive.CustomPatterns = s.cfgMgr.Get().Sensitive.CustomPatterns
			break
		}
	}
	if err := validateSensitiveRules(cfg.Sensitive); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_sensitive_rule", "敏感规则无效: "+err.Error(), err)
		return
	}
	// 密码只能通过专用密码端点修改，通用配置请求无权写入凭据哈希。
	cfg.Security.LockPasswordHash = curCfg.Security.LockPasswordHash
	cfg.Security.SecondaryPasswordHash = curCfg.Security.SecondaryPasswordHash
	// 保留备份相关敏感密码，不被前端回传的占位符或空值覆盖。
	cfg.Backup.DirBackup.Password = resolveSecretField(cfg.Backup.DirBackup.Password, curCfg.Backup.DirBackup.Password)
	cfg.Backup.EmailBackup.SMTPPass = resolveSecretField(cfg.Backup.EmailBackup.SMTPPass, curCfg.Backup.EmailBackup.SMTPPass)
	cfg.Backup.EmailBackup.Password = resolveSecretField(cfg.Backup.EmailBackup.Password, curCfg.Backup.EmailBackup.Password)
	normalizeConfig(&cfg)
	if err := validateConfig(cfg); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_config", "配置参数无效: "+err.Error(), err)
		return
	}
	// 启用定时备份时校验配置完整性，避免启用后无法实际执行备份。
	if cfg.Backup.DirBackup.Enabled {
		if msg := validateDirBackupEnabled(cfg.Backup.DirBackup); msg != "" {
			w.WriteHeader(http.StatusBadRequest)
			s.writeJSON(w, map[string]any{"ok": false, "error": "目录备份配置不完整：" + msg})
			return
		}
	}
	if cfg.Backup.EmailBackup.Enabled {
		if msg := validateEmailBackupEnabled(cfg.Backup.EmailBackup); msg != "" {
			w.WriteHeader(http.StatusBadRequest)
			s.writeJSON(w, map[string]any{"ok": false, "error": "邮件备份配置不完整：" + msg})
			return
		}
	}
	response := map[string]any{"ok": true}
	if err := s.cfgMgr.Update(cfg); err != nil {
		var runtimeErr *ConfigRuntimeApplyError
		if !errors.As(err, &runtimeErr) {
			s.writeError(w, err)
			return
		}
		response["runtimeError"] = runtimeErr.Error()
		var hkErr *HotkeyError
		if errors.As(runtimeErr, &hkErr) {
			response["hotkeyError"] = hkErr.Error()
		}
		s.logger.Warn("config_runtime_apply_failed", map[string]any{"error": runtimeErr.Error()})
	}
	// 超时设置变更后立即对当前会话生效，避免要等到下次登录/验证才应用。
	token := GetSessionToken(r)
	if curCfg.Security.LockSessionTimeout != cfg.Security.LockSessionTimeout {
		if s.auth.RefreshSessionExpiry(token, cfg.Security.LockSessionTimeout) {
			SetSessionCookie(w, token, cfg.Security.LockSessionTimeout, s.requestUsesHTTPS(r))
		}
	}
	if curCfg.Security.SecondaryAuthTimeout != cfg.Security.SecondaryAuthTimeout {
		if cfg.Security.SecondaryAuthTimeout > 0 {
			// PUT /api/config 已经通过 requireSecondary。即使旧配置为 0，
			// 当前请求也是靠刚验证得到的一次性 token 放行的，因此应当
			// 立即把当前会话升级为按新时长缓存，不能等用户下次再验证。
			s.auth.GrantSecondaryAuth(token, cfg.Security.SecondaryAuthTimeout)
		} else {
			s.auth.RefreshSecondaryExpiry(token, cfg.Security.SecondaryAuthTimeout)
		}
	}
	s.logger.Info("config_updated", nil)
	response["config"] = stripSecrets(s.cfgMgr.Get())
	s.writeJSON(w, response)
}

// Asset bytes and their metadata are owned by the dedicated media endpoints.
// General settings updates may change the visible title and visual controls,
// but never select an arbitrary asset file or version.
func preserveAssetMetadata(cfg *Config, current Config) {
	cfg.Brand.LogoContentType = current.Brand.LogoContentType
	cfg.Brand.LogoVersion = current.Brand.LogoVersion
	cfg.Brand.FaviconContentType = current.Brand.FaviconContentType
	cfg.Brand.FaviconVersion = current.Brand.FaviconVersion
	cfg.Background.ContentType = current.Background.ContentType
	cfg.Background.Version = current.Background.Version
}

// handleExportData exports complete data as JSON, XLSX, or encrypted cvexport.
func (s *SearchServer) handleExportData(w http.ResponseWriter, r *http.Request) {
	// 导出是付费功能：未激活时禁止导出（数据出口付费墙）
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusPaymentRequired)
		s.writeJSON(w, map[string]any{
			"ok":      false,
			"error":   "license_required",
			"message": "导出为付费功能，激活后可导出全部数据",
		})
		return
	}
	format := r.URL.Query().Get("format")
	scope := ExportScope{
		Mode: r.URL.Query().Get("mode"),
		From: r.URL.Query().Get("from"),
		To:   r.URL.Query().Get("to"),
	}
	if scope.Mode == "" {
		scope.Mode = "full"
	}
	if sinceID := r.URL.Query().Get("sinceId"); sinceID != "" {
		if scope.Mode != "incremental" {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_since_id", "sinceId 仅适用于 incremental 模式", nil)
			return
		}
		id, err := strconv.ParseInt(sinceID, 10, 64)
		if err != nil || id <= 0 {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_since_id", "sinceId 必须是正整数", err)
			return
		}
		scope.SinceID = id
	}
	// 统一校验（与备份 resolveBackupScope 共用 validateExportScope），
	// 修复 range 模式只查非空不查 From>=To 的静默空导出问题。
	var err error
	scope, err = normalizeExportRange(scope, s.configuredTimeZone())
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_export_scope", "导出范围无效", err)
		return
	}
	if err := validateExportScope(scope); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_export_scope", "导出范围无效", err)
		return
	}
	if err := setExportWriteDeadline(w, time.Now()); err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "export_deadline_failed", "无法为大型导出设置传输期限", err)
		return
	}

	switch format {
	case "xlsx":
		file, err := os.CreateTemp("", "clipvault-export-*.xlsx")
		if err != nil {
			s.writeError(w, err)
			return
		}
		defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
		if _, err := s.store.WriteXLSXScope(r.Context(), scope, file); err != nil {
			s.writeError(w, err)
			return
		}
		if err := serveExportFile(w, r, file, "clipvault-data.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"); err != nil {
			s.logger.Error("data_export_failed", err, map[string]any{"format": "xlsx", "mode": scope.Mode})
			return
		}
		s.logger.Info("data_exported", map[string]any{"format": "xlsx", "mode": scope.Mode})
	case "json":
		file, err := os.CreateTemp("", "clipvault-export-*.json")
		if err != nil {
			s.writeError(w, err)
			return
		}
		defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
		if _, err := s.store.StreamExport(r.Context(), scope, file); err != nil {
			s.logger.Error("data_export_failed", err, map[string]any{"format": "json", "mode": scope.Mode})
			s.writeError(w, err)
			return
		}
		if err := serveExportFile(w, r, file, "clipvault-data.json", "application/json; charset=utf-8"); err != nil {
			s.logger.Error("data_export_failed", err, map[string]any{"format": "json", "mode": scope.Mode})
			return
		}
		s.logger.Info("data_exported", map[string]any{"format": "json", "mode": scope.Mode})
	case "cvexport":
		if r.Method != http.MethodPost {
			s.writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "cvexport 导出需要 POST 请求", nil)
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_request", "无效的请求", nil)
			return
		}
		password := req.Password
		if password == "" {
			s.writeAPIError(w, http.StatusBadRequest, "password_required", "cvexport 格式需要加密密码", nil)
			return
		}
		file, err := os.CreateTemp("", "clipvault-export-*.cvexport")
		if err != nil {
			s.writeError(w, err)
			return
		}
		defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
		var maxID int64
		err = WriteCVExport(file, password, func(plain io.Writer) error {
			var streamErr error
			maxID, streamErr = s.store.StreamExport(r.Context(), scope, plain)
			return streamErr
		})
		if err != nil {
			s.writeError(w, err)
			return
		}
		if err := serveExportFile(w, r, file, "clipvault-data.cvexport", "application/octet-stream"); err != nil {
			s.logger.Error("data_export_failed", err, map[string]any{"format": "cvexport", "mode": scope.Mode})
			return
		}
		s.logger.Info("data_exported", map[string]any{"format": "cvexport", "mode": scope.Mode, "maxID": maxID})
	default:
		s.writeAPIError(w, http.StatusBadRequest, "unsupported_format", "不支持的导出格式", nil)
	}
}

// handleImportData imports a JSON, XLSX, or encrypted .cvexport export file.
func (s *SearchServer) handleImportData(w http.ResponseWriter, r *http.Request) {
	format := r.Header.Get("X-ClipVault-Import-Format")
	if format == "" {
		s.writeImportError(w, errors.New("import format is required"))
		return
	}
	if format != "json" && format != "xlsx" && format != "cvexport" {
		s.writeImportError(w, errors.New("unsupported import format"))
		return
	}
	if err := setImportTransferDeadlines(w, time.Now()); err != nil {
		s.writeImportError(w, fmt.Errorf("extend import transfer deadlines: %w", err))
		return
	}

	const (
		maxStreamImportBytes = 520 << 20
		maxXLSXImportBytes   = 64 << 20
	)
	maxBytes := int64(maxStreamImportBytes)
	if format == "xlsx" {
		maxBytes = maxXLSXImportBytes
	}
	file, err := spoolImportBody(w, r, maxBytes)
	if err != nil {
		s.writeImportError(w, err)
		return
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err := setImportProcessingDeadline(w, time.Now()); err != nil {
		s.writeImportError(w, fmt.Errorf("extend import processing deadline: %w", err))
		return
	}

	prefix := make([]byte, cvexportMagicLen)
	n, readErr := io.ReadFull(file, prefix)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		s.writeImportError(w, fmt.Errorf("inspect import: %w", readErr))
		return
	}
	isCVExport := IsCVExport(prefix[:n])
	if isCVExport && format != "cvexport" {
		s.writeImportError(w, errors.New("import format does not match encrypted export"))
		return
	}

	var importErr error
	switch format {
	case "cvexport":
		if !isCVExport {
			s.writeImportError(w, errors.New("invalid encrypted export"))
			return
		}
		password := r.Header.Get("X-ClipVault-Import-Password")
		if password == "" {
			s.writeImportError(w, errors.New("password is required to decrypt .cvexport file"))
			return
		}
		plain, err := os.CreateTemp("", "clipvault-import-*.json")
		if err != nil {
			s.writeImportError(w, err)
			return
		}
		defer func() { _ = plain.Close(); _ = os.Remove(plain.Name()) }()
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			importErr = err
		} else {
			importErr = DecryptCVExport(plain, file, password)
		}
		if importErr != nil {
			importErr = fmt.Errorf("decrypt cvexport: %w", importErr)
			s.writeImportError(w, importErr)
			s.logger.Error("import_failed", importErr, map[string]any{"format": format})
			return
		}
		if _, err := plain.Seek(0, io.SeekStart); err != nil {
			importErr = err
		} else {
			importErr = s.store.ImportFullJSONReader(r.Context(), plain)
		}
	case "xlsx":
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			importErr = err
			break
		}
		data, err := io.ReadAll(file)
		if err != nil {
			importErr = err
			break
		}
		importErr = s.store.ImportXLSX(r.Context(), data)
	case "json":
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			importErr = err
		} else {
			importErr = s.store.ImportFullJSONReader(r.Context(), file)
		}
	}
	if importErr != nil {
		s.writeImportError(w, importErr)
		s.logger.Error("import_failed", importErr, map[string]any{"format": format})
		return
	}

	response := map[string]any{"ok": true}
	if err := s.cfgMgr.Reload(); err != nil {
		response["runtimeError"] = err.Error()
		var hotkeyError *HotkeyError
		if errors.As(err, &hotkeyError) {
			response["hotkeyError"] = hotkeyError.Error()
		}
		s.logger.Warn("import_runtime_refresh_failed", map[string]any{"format": format, "error": err.Error()})
	}
	s.logger.Info("data_imported", map[string]any{"format": format})
	s.writeJSON(w, response)
}

func serveExportFile(w http.ResponseWriter, _ *http.Request, file *os.File, downloadName, contentType string) error {
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync export: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat export: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind export: %w", err)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, downloadName))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if _, err := io.Copy(w, file); err != nil {
		return fmt.Errorf("send export: %w", err)
	}
	return nil
}

func setImportTransferDeadlines(w http.ResponseWriter, now time.Time) error {
	// The upload and database-processing phases each get their own 60-minute
	// window. handleImportData refreshes the write deadline after spooling.
	controller := http.NewResponseController(w)
	if err := setReadDeadlineIfSupported(controller, now.Add(importReadTimeout)); err != nil {
		return err
	}
	return setWriteDeadlineIfSupported(controller, now.Add(importReadTimeout))
}

func setImportProcessingDeadline(w http.ResponseWriter, now time.Time) error {
	return setWriteDeadlineIfSupported(http.NewResponseController(w), now.Add(importProcessingTimeout))
}

func setExportWriteDeadline(w http.ResponseWriter, now time.Time) error {
	return setWriteDeadlineIfSupported(http.NewResponseController(w), now.Add(exportWriteTimeout))
}

func setReadDeadlineIfSupported(controller *http.ResponseController, deadline time.Time) error {
	if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

func setWriteDeadlineIfSupported(controller *http.ResponseController, deadline time.Time) error {
	if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

func spoolImportBody(w http.ResponseWriter, r *http.Request, maxBytes int64) (*os.File, error) {
	file, err := os.CreateTemp("", "clipvault-upload-*")
	if err != nil {
		return nil, fmt.Errorf("create import staging file: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}()
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if _, err := io.Copy(file, r.Body); err != nil {
		return nil, fmt.Errorf("read import body: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync import body: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind import body: %w", err)
	}
	success = true
	return file, nil
}

func (s *SearchServer) writeImportError(w http.ResponseWriter, err error) {
	// 导入失败：原始 err 只进日志，客户端给可读文案
	// （防解密/DB 内部错误细节泄漏，#19）
	s.writeAPIError(w, http.StatusBadRequest, "import_error", "导入失败，请检查文件格式、密码或数据完整性", err)
}

// ── Recovery password management ──────────────────────────────

func (s *SearchServer) handleRecoveryStatus(w http.ResponseWriter, r *http.Request) {
	hasRecovery, err := HasRecoveryPassword(s.env.DataDir)
	if err != nil {
		s.writeError(w, err)
		return
	}
	isolated, err := ListIsolatedDatabases(s.env.DataDir)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{
		"hasRecovery":       hasRecovery,
		"isolatedDatabases": isolated.Databases,
		"pendingSwitchId":   isolated.PendingID,
	})
}

func (s *SearchServer) handleIsolatedDatabaseSwitch(w http.ResponseWriter, r *http.Request) {
	fresh, _ := r.Context().Value(freshSecondaryContextKey{}).(bool)
	if !fresh {
		s.writeAPIError(w, http.StatusForbidden, "fresh_secondary_required", "切换数据库需要重新输入二次密码", nil)
		return
	}
	var req struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	if err := ScheduleIsolatedDatabaseSwitch(s.env.DataDir, strings.TrimSpace(req.ID), req.Password); err != nil {
		s.logger.Error("isolated_database_switch_failed", err, nil)
		switch {
		case errors.Is(err, ErrIsolatedDatabaseNotFound):
			s.writeAPIError(w, http.StatusNotFound, "isolated_database_not_found", "这份隔离数据已不存在，请刷新列表", err)
		case errors.Is(err, ErrIsolatedDatabasePending):
			s.writeAPIError(w, http.StatusConflict, "isolated_database_switch_pending", "已有数据库切换任务等待应用重启", err)
		case errors.Is(err, ErrIsolatedDatabaseUnavailable):
			s.writeAPIError(w, http.StatusBadRequest, "isolated_database_unavailable", "恢复密码不正确，或这份隔离数据已损坏", err)
		default:
			s.writeInternalError(w, err)
		}
		return
	}
	s.logger.Info("isolated_database_switch_scheduled", map[string]any{"isolationId": strings.TrimSpace(req.ID)})
	s.writeJSON(w, map[string]any{
		"ok":      true,
		"message": "切换任务已准备完成，请退出 ClipVault 并重新打开",
	})
}

func (s *SearchServer) handleIsolatedDatabaseSwitchCancel(w http.ResponseWriter, r *http.Request) {
	fresh, _ := r.Context().Value(freshSecondaryContextKey{}).(bool)
	if !fresh {
		s.writeAPIError(w, http.StatusForbidden, "fresh_secondary_required", "取消数据库切换需要重新输入二次密码", nil)
		return
	}
	if err := CancelIsolatedDatabaseSwitch(s.env.DataDir); err != nil {
		s.logger.Error("isolated_database_switch_cancel_failed", err, nil)
		s.writeInternalError(w, err)
		return
	}
	s.logger.Info("isolated_database_switch_cancelled", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) handleRecoveryChange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	if err := ChangeRecoveryPassword(s.env.DataDir, s.store.DBKey(), req.OldPassword, req.NewPassword); err != nil {
		s.logger.Error("recovery_change_failed", err, nil)
		var rpe *RecoveryPasswordError
		if errors.As(err, &rpe) {
			message := "修改恢复密码失败"
			switch rpe.Code {
			case "wrong_password":
				message = "当前恢复密码不正确"
			case "not_set":
				message = "尚未设置恢复密码"
			case "weak_password":
				message = "新恢复密码至少需要 12 个字符，且首尾不能有空格"
			}
			s.writeAPIError(w, http.StatusBadRequest, "recovery_change_failed", message, err)
		} else {
			s.writeInternalError(w, err)
		}
		return
	}
	s.logger.Info("recovery_password_changed", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) handleRecoveryReset(w http.ResponseWriter, r *http.Request) {
	fresh, _ := r.Context().Value(freshSecondaryContextKey{}).(bool)
	if !fresh {
		s.writeAPIError(w, http.StatusForbidden, "fresh_secondary_required", "重置恢复密码需要重新输入二次密码", nil)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	if err := ResetRecoveryPassword(s.env.DataDir, s.store.DBKey(), req.Password); err != nil {
		s.logger.Error("recovery_reset_failed", err, nil)
		var rpe *RecoveryPasswordError
		if errors.As(err, &rpe) && rpe.Code == "weak_password" {
			s.writeAPIError(w, http.StatusBadRequest, "recovery_reset_failed", "重置恢复密码失败：请使用至少 12 个字符且首尾无空格", err)
		} else {
			s.writeInternalError(w, err)
		}
		return
	}
	s.logger.Info("recovery_password_reset", nil)
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
			"lastTime":         ds.LastTime.Format(time.RFC3339),
			"lastOK":           ds.LastOK,
			"lastError":        ds.LastError,
			"backupFile":       ds.BackupFile,
			"actualMode":       ds.ActualMode,
			"autoFallback":     ds.AutoFallback,
			"incrementalCount": ds.IncrementalCount,
			"fullInterval":     ds.FullInterval,
			"lastFullAt":       ds.LastFullAt,
		}
		resp["emailStatus"] = map[string]any{
			"lastTime":         es.LastTime.Format(time.RFC3339),
			"lastOK":           es.LastOK,
			"lastError":        es.LastError,
			"backupFile":       es.BackupFile,
			"actualMode":       es.ActualMode,
			"autoFallback":     es.AutoFallback,
			"incrementalCount": es.IncrementalCount,
			"fullInterval":     es.FullInterval,
			"lastFullAt":       es.LastFullAt,
		}
	}
	s.writeJSON(w, resp)
}

// handleEmailBackupHistory returns a read-only list of recently sent backup emails.
func (s *SearchServer) handleEmailBackupHistory(w http.ResponseWriter, r *http.Request) {
	history, err := s.store.ListEmailBackupHistory(r.Context(), 100)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"history": history})
}

// validateDirBackupEnabled 校验已启用的目录备份配置是否完整。
// 返回非空字符串描述缺失项，空字符串表示通过。
// 仅 cvexport 格式需要加密密码；json/xlsx 为明文导出，无需密码。
func validateDirBackupEnabled(db DirBackupConfig) string {
	var missing []string
	if db.Interval < 1 || db.Interval > maxBackupIntervalHours {
		missing = append(missing, "备份间隔")
	}
	if strings.TrimSpace(db.Dir) == "" {
		missing = append(missing, "目标目录")
	}
	if !isBackupFormat(db.Format) {
		missing = append(missing, "备份格式")
	} else if db.Format == "cvexport" && db.Password == "" {
		missing = append(missing, "加密密码")
	}
	if len(missing) == 0 {
		return ""
	}
	return strings.Join(missing, "、")
}

// validateEmailBackupEnabled 校验已启用的邮件备份配置是否完整。
// 返回非空字符串描述缺失项，空字符串表示通过。
// 仅 cvexport 格式需要加密密码；json/xlsx 为明文导出，无需密码。
func validateEmailBackupEnabled(eb EmailBackupConfig) string {
	var missing []string
	if eb.Interval < 1 || eb.Interval > maxBackupIntervalHours {
		missing = append(missing, "备份间隔")
	}
	missing = append(missing, smtpDeliveryErrors(eb)...)
	if !isBackupFormat(eb.Format) {
		missing = append(missing, "备份格式")
	} else if eb.Format == "cvexport" && eb.Password == "" {
		missing = append(missing, "加密密码")
	}
	if eb.MaxPartSize < 1<<20 || eb.MaxPartSize > 50<<20 {
		missing = append(missing, "单封邮件上限")
	}
	missing = append(missing, emailTemplateErrors(eb)...)
	if len(missing) == 0 {
		return ""
	}
	return strings.Join(missing, "、")
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
	forceFull := r.URL.Query().Get("forceFull") == "1"
	if err := s.backup.RunDirBackup(forceFull); err != nil {
		s.logger.Error("dir_backup_manual_failed", err, nil)
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.logger.Info("dir_backup_manual_ok", map[string]any{"forceFull": forceFull})
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleBackupEmailNow 立即执行一次邮件备份。
func (s *SearchServer) handleBackupEmailNow(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		s.writeJSON(w, map[string]any{"ok": false, "error": "备份服务未启动"})
		return
	}
	forceFull := r.URL.Query().Get("forceFull") == "1"
	if err := s.backup.RunEmailBackup(forceFull); err != nil {
		s.logger.Error("email_backup_manual_failed", err, nil)
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.logger.Info("email_backup_manual_ok", map[string]any{"forceFull": forceFull})
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

// writeFileRefError 统一 resolveRecordedFilePath 三类失败的 HTTP 分层响应（#2b 错误码分层）：
//   - ErrFreeWindowOutside → 402 window_outside（免费版可见窗口之外，与 enforceFreeWindow / handleItemPosition 一致）；
//   - ErrFileRefLookup     → 5xx internal_error（存储层/IO 查询故障，复用本文件既有的 writeError 风格，
//     不把 DuckDB 内部细节回传客户端，#19）；
//   - 其余（无效 item_id / file_index、越界、非文件记录等）→ 400 invalid_file_ref（用户入参非法）。
func (s *SearchServer) writeFileRefError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFreeWindowOutside):
		s.writeAPIError(w, http.StatusPaymentRequired, "window_outside",
			"未激活仅可查看最近 "+strconv.Itoa(FreeTierVisibleLimit)+" 条记录，该文件不在可查看范围内", nil)
	case errors.Is(err, ErrFileRefLookup):
		s.writeError(w, err)
	default:
		s.writeAPIError(w, http.StatusBadRequest, "invalid_file_ref", "无效的文件引用", nil)
	}
}

// handleFileCheck checks if a file exists and returns its metadata.
func (s *SearchServer) handleFileCheck(w http.ResponseWriter, r *http.Request) {
	p, sensitive, err := s.resolveRecordedFile(r)
	if err != nil {
		s.writeFileRefError(w, err)
		return
	}
	if sensitive && !s.authorizeSecondaryRequest(w, r) {
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
	p, sensitive, err := s.resolveRecordedFile(r)
	if err != nil {
		s.writeFileRefError(w, err)
		return
	}
	if sensitive && !s.authorizeSecondaryRequest(w, r) {
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		s.writeAPIError(w, http.StatusNotFound, "file_not_found", "文件不存在", nil)
		return
	}
	if r.URL.Query().Get("download") == "1" || !safeInlineImageExt(filepath.Ext(p)) {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(p)}))
	}
	http.ServeFile(w, r, p)
}

// handleFileHash computes and returns the MD5 and SHA256 hashes of a file on demand.
func (s *SearchServer) handleFileHash(w http.ResponseWriter, r *http.Request) {
	p, sensitive, err := s.resolveRecordedFile(r)
	if err != nil {
		s.writeFileRefError(w, err)
		return
	}
	if sensitive && !s.authorizeSecondaryRequest(w, r) {
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		s.writeJSON(w, map[string]any{"ok": false, "error": "file not found"})
		return
	}
	f, err := os.Open(p)
	if err != nil {
		// 系统错误（权限/磁盘）只进日志，客户端给可读文案
		s.writeAPIError(w, http.StatusInternalServerError, "file_open_failed", "无法读取文件", err)
		return
	}
	defer f.Close()
	m := md5.New()
	sh := sha256.New()
	if _, err := io.Copy(io.MultiWriter(m, sh), f); err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "file_read_failed", "读取文件失败", err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "md5": hex.EncodeToString(m.Sum(nil)), "sha256": hex.EncodeToString(sh.Sum(nil))})
}

// handleFileOpenDir opens the file's containing directory in the system file explorer.
func (s *SearchServer) handleFileOpenDir(w http.ResponseWriter, r *http.Request) {
	p, sensitive, err := s.resolveRecordedFile(r)
	if err != nil {
		s.writeFileRefError(w, err)
		return
	}
	if sensitive && !s.authorizeSecondaryRequest(w, r) {
		return
	}
	if err := openFolderAndSelectItem(p); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// ErrFreeWindowOutside 表示请求的文件所属记录在免费版可见窗口之外。
// 调用方据此返回 402（与 enforceFreeWindow / handleItemPosition 语义一致，#2b）。
var ErrFreeWindowOutside = errors.New("file record is outside the free-tier visible window")

// ErrFileRefLookup 表示 resolveRecordedFile 在存储层/IO 查询（GetPositionByID、GetByID）
// 失败时返回。调用方据此返回 5xx（与 enforceFreeWindow / handleItemPosition 对同类错误的处理
// 对齐），避免把 DuckDB 查询故障误报成 400 客户端参数错误。原始错误通过 %v 保留在链中便于日志。
var ErrFileRefLookup = errors.New("file reference lookup failed")

// resolveRecordedFile 返回路径以及该记录是否为敏感记录。敏感属性必须和
// 路径在同一次记录读取中取得，避免授权检查与实际读取之间发生错配。
func (s *SearchServer) resolveRecordedFile(r *http.Request) (string, bool, error) {
	itemID, err := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64)
	if err != nil || itemID <= 0 {
		return "", false, fmt.Errorf("invalid item_id")
	}
	fileIndex, err := strconv.Atoi(r.URL.Query().Get("file_index"))
	if err != nil || fileIndex < 0 {
		return "", false, fmt.Errorf("invalid file_index")
	}
	item, err := s.store.GetByID(r.Context(), itemID)
	if errors.Is(err, ErrItemNotFound) {
		return "", false, fmt.Errorf("item not found")
	}
	if err != nil {
		return "", false, fmt.Errorf("%w: load item: %v", ErrFileRefLookup, err)
	}
	// 免费版读窗口门控：未激活用户只能访问最近 FreeTierVisibleLimit 条记录，
	// 窗口外的文件记录不允许定位/下载/校验/打开（与 enforceFreeWindow 一致，#2b）。
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		pos, perr := s.store.GetPositionByID(r.Context(), itemID)
		if perr != nil {
			return "", false, fmt.Errorf("%w: resolve position: %v", ErrFileRefLookup, perr)
		}
		if pos >= FreeTierVisibleLimit {
			return "", false, ErrFreeWindowOutside
		}
	}
	if item.Format != "CF_HDROP" || !isStoredFileKind(item.Kind) {
		return "", false, fmt.Errorf("item is not a file record")
	}
	lines := strings.Split(strings.ReplaceAll(item.Text, "\r\n", "\n"), "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			paths = append(paths, line)
		}
	}
	if fileIndex >= len(paths) {
		return "", false, fmt.Errorf("file_index out of range")
	}
	return paths[fileIndex], item.Sensitive, nil
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
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_name", "分组名称不能为空", nil)
		return
	}
	g, err := s.store.CreateGroup(r.Context(), strings.TrimSpace(req.Name), req.ParentID)
	if err != nil {
		if errors.Is(err, ErrInvalidGroupName) {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_group_name", "分组名称不能超过 100 个字符", err)
			return
		}
		if errors.Is(err, ErrInvalidGroupParent) {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_parent_id", "父分组 ID 无效", err)
			return
		}
		if errors.Is(err, ErrGroupParentMissing) {
			s.writeAPIError(w, http.StatusNotFound, "parent_not_found", "父分组不存在", err)
			return
		}
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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的 id", nil)
		return
	}
	var req struct {
		Name     string `json:"name"`
		ParentID int64  `json:"parentId"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_name", "分组名称不能为空", nil)
		return
	}
	if err := s.store.UpdateGroup(r.Context(), id, strings.TrimSpace(req.Name), req.ParentID); err != nil {
		switch {
		case errors.Is(err, ErrInvalidGroupName):
			s.writeAPIError(w, http.StatusBadRequest, "invalid_group_name", "分组名称不能超过 100 个字符", err)
		case errors.Is(err, ErrGroupNotFound), errors.Is(err, ErrGroupParentMissing):
			s.writeAPIError(w, http.StatusNotFound, "group_not_found", "分组或父分组不存在", err)
		case errors.Is(err, ErrInvalidGroupParent), errors.Is(err, ErrGroupCycle):
			s.writeAPIError(w, http.StatusBadRequest, "invalid_parent_id", "父分组无效或会形成循环", err)
		default:
			s.writeError(w, err)
		}
		return
	}
	s.logger.Info("group_updated", map[string]any{"id": id, "name": req.Name, "parentId": req.ParentID})
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleGroupsDelete 删除分组（含后代）。
func (s *SearchServer) handleGroupsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的 id", nil)
		return
	}
	if err := s.store.DeleteGroup(r.Context(), id); err != nil {
		if errors.Is(err, ErrGroupNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "group_not_found", "分组不存在", err)
			return
		}
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
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	if req.ItemID <= 0 || req.GroupID <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的条目或分组 id", nil)
		return
	}
	if !s.authorizeItemAccess(w, r, req.ItemID) {
		return
	}
	if err := s.store.AddItemToGroup(r.Context(), req.ItemID, req.GroupID); err != nil {
		if errors.Is(err, errItemOrGroupNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "not_found", "条目或分组不存在", err)
			return
		}
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleItemRemoveFromGroup 将条目从分组移除。
func (s *SearchServer) handleItemRemoveFromGroup(w http.ResponseWriter, r *http.Request) {
	itemID, err := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64)
	if err != nil || itemID <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_item_id", "无效的条目 id", nil)
		return
	}
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group_id"), 10, 64)
	if err != nil || groupID <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_id", "无效的分组 id", nil)
		return
	}
	if !s.authorizeItemAccess(w, r, itemID) {
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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_item_id", "无效的条目 id", nil)
		return
	}
	if !s.authorizeItemAccess(w, r, itemID) {
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

func (s *SearchServer) authorizeItemAccess(w http.ResponseWriter, r *http.Request, itemID int64) bool {
	item, err := s.store.GetByID(r.Context(), itemID)
	if err != nil {
		s.writeItemLookupError(w, err)
		return false
	}
	if s.enforceFreeWindow(w, r, itemID) {
		return false
	}
	return !item.Sensitive || s.authorizeSecondaryRequest(w, r)
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func (s *SearchServer) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// writeAPIError 统一 JSON 错误契约：{ok:false, code, message}。
// 原始 err 只写日志，不回传客户端（防 SQL/DuckDB 内部细节泄漏，#19）。
// 前端以 d.error 字段消费错误，message 保留为可读文案；code 供程序化判断。
func (s *SearchServer) writeAPIError(w http.ResponseWriter, status int, code, userMsg string, err error) {
	if err != nil && s.logger != nil {
		s.logger.Warn("api_error", map[string]any{"code": code, "error": err.Error()})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": code, "error": userMsg})
}

// writeInternalError 内部错误便捷封装：不透传原始 err（仅日志），统一 500。
func (s *SearchServer) writeInternalError(w http.ResponseWriter, err error) {
	s.writeAPIError(w, http.StatusInternalServerError, "internal_error", "操作失败，请查看日志", err)
}

func (s *SearchServer) writeError(w http.ResponseWriter, err error) {
	s.writeInternalError(w, err)
}

func (s *SearchServer) writeItemLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrItemNotFound) {
		s.writeAPIError(w, http.StatusNotFound, "not_found", "记录不存在", nil)
		return
	}
	s.writeInternalError(w, err)
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
			s.writeAPIError(w, http.StatusUnauthorized, "unauthorized", "未认证", nil)
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
		if s.auth.HasLockPassword(cfg) {
			sess := s.auth.GetSession(GetSessionToken(r))
			if sess == nil || !sess.Authenticated {
				s.writeAPIError(w, http.StatusUnauthorized, "unauthorized", "未认证", nil)
				return
			}
		}
		if !s.authorizeSecondaryRequest(w, r) {
			return
		}
		next(w, r)
	}
}

// authorizeSecondaryRequest 对单个请求执行二次认证检查。它既供中间件使用，
// 也供“仅当目标记录敏感时才保护”的文件端点使用。
func (s *SearchServer) authorizeSecondaryRequest(w http.ResponseWriter, r *http.Request) bool {
	cfg := s.cfgMgr.Get()
	if !s.auth.HasSecondaryPassword(cfg) {
		return true
	}
	token := GetSessionToken(r)
	sess := s.auth.GetSession(token)
	if sess == nil || !sess.Authenticated {
		if s.auth.HasLockPassword(cfg) {
			s.writeAPIError(w, http.StatusUnauthorized, "unauthorized", "未认证", nil)
		} else {
			s.writeAPIError(w, http.StatusForbidden, "secondary_required", "需要二次认证", nil)
		}
		return false
	}
	if freshToken := r.Header.Get(freshSecondaryHeaderName); freshToken != "" {
		if s.auth.ConsumeSecondaryToken(freshToken, token) {
			// WithContext 无法在 bool helper 中替换调用方的请求指针；fresh 语义
			// 由独立 helper 判断请求头是否已成功消费，见 requireFreshSecondary。
			*r = *r.WithContext(context.WithValue(r.Context(), freshSecondaryContextKey{}, true))
			return true
		}
	}
	if s.auth.HasSecondaryAuth(token) {
		return true
	}
	oneTimeToken := r.Header.Get("X-ClipVault-Secondary-Token")
	if s.auth.ConsumeSecondaryToken(oneTimeToken, token) {
		return true
	}
	s.writeAPIError(w, http.StatusForbidden, "secondary_required", "需要二次认证", nil)
	return false
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
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	cfg := s.cfgMgr.Get()
	if !s.auth.HasLockPassword(cfg) {
		s.writeJSON(w, map[string]any{"ok": true, "message": "no lock password set"})
		return
	}
	if status := s.auth.CheckRateLimitFor(ratePurposeLock); !status.Allowed {
		s.writeRateLimitBlocked(w, status)
		return
	}
	if !s.auth.VerifyLockPassword(cfg, req.Password) {
		s.writePasswordFailure(w, "密码错误", s.auth.RecordFailedAttemptFor(ratePurposeLock))
		return
	}
	s.auth.ResetRateLimitFor(ratePurposeLock)
	token, err := s.auth.CreateSession(cfg.Security.LockSessionTimeout)
	if err != nil {
		s.writeError(w, err)
		return
	}
	SetSessionCookie(w, token, cfg.Security.LockSessionTimeout, s.requestUsesHTTPS(r))
	s.logger.Info("auth_login", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleAuthLogout 销毁当前会话。
func (s *SearchServer) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	token := GetSessionToken(r)
	s.auth.DestroySession(token)
	ClearSessionCookie(w, s.requestUsesHTTPS(r))
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleAuthSecondary 验证二次密码并授予二次认证权限。
func (s *SearchServer) handleAuthSecondary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	cfg := s.cfgMgr.Get()
	if !s.auth.HasSecondaryPassword(cfg) {
		s.writeJSON(w, map[string]any{"ok": true, "message": "no secondary password set"})
		return
	}
	if status := s.auth.CheckRateLimitFor(ratePurposeSecondary); !status.Allowed {
		s.writeRateLimitBlocked(w, status)
		return
	}
	token := GetSessionToken(r)
	sess := s.auth.GetSession(token)
	if sess == nil || !sess.Authenticated {
		if s.auth.HasLockPassword(cfg) {
			s.writeAPIError(w, http.StatusUnauthorized, "unauthorized", "未认证", nil)
			return
		}
	}
	if !s.auth.VerifySecondaryPassword(cfg, req.Password) {
		s.writePasswordFailure(w, "二次密码错误", s.auth.RecordFailedAttemptFor(ratePurposeSecondary))
		return
	}
	s.auth.ResetRateLimitFor(ratePurposeSecondary)
	if sess == nil || !sess.Authenticated {
		// 无锁屏密码时，只在二次密码验证成功后创建会话。失败尝试不应
		// 留下最长存活 24 小时的无用会话。
		var err error
		token, err = s.auth.CreateSession(cfg.Security.LockSessionTimeout)
		if err != nil {
			s.writeError(w, err)
			return
		}
		SetSessionCookie(w, token, cfg.Security.LockSessionTimeout, s.requestUsesHTTPS(r))
	}
	s.auth.GrantSecondaryAuth(token, cfg.Security.SecondaryAuthTimeout)

	// 无论 timeout 是否为 0，都生成一个一次性 token 供当前操作使用
	oneTimeToken, err := s.auth.GenerateSecondaryToken(token)
	if err != nil {
		s.writeError(w, err)
		return
	}

	resp := map[string]any{
		"ok":              true,
		"secondaryToken":  oneTimeToken,
		"secondaryCached": cfg.Security.SecondaryAuthTimeout > 0,
	}
	if cfg.Security.SecondaryAuthTimeout > 0 {
		resp["message"] = fmt.Sprintf("二次验证成功，%d 分钟内无需重复验证", cfg.Security.SecondaryAuthTimeout)
	} else {
		resp["message"] = "二次验证成功"
	}
	s.logger.Info("auth_secondary", nil)
	s.writeJSON(w, resp)
}

// handleAuthSetPassword 设置、修改或删除锁屏密码，以及修改二次密码。
// 请求体格式:
//
//	{ "type": "lock", "action": "set"|"change"|"remove", "oldPassword": "", "newPassword": "" }
//	{ "type": "secondary", "action": "change", "oldPassword": "", "newPassword": "" }
func (s *SearchServer) handleAuthSetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type        string `json:"type"`        // "lock" or "secondary"
		Action      string `json:"action"`      // "set", "change" or "remove"
		OldPassword string `json:"oldPassword"` // 旧密码（change/remove 时必填）
		NewPassword string `json:"newPassword"` // 新密码
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}

	if req.Type != "lock" && req.Type != "secondary" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_type", "无效的类型", nil)
		return
	}
	if req.Action != "set" && req.Action != "change" && req.Action != "remove" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_action", "无效的操作", nil)
		return
	}
	if req.Type == "secondary" && req.Action != "change" {
		s.writeAPIError(w, http.StatusBadRequest, "secondary_password_required", "二次密码只能修改，无法重新设置或删除", nil)
		return
	}

	cfg := s.cfgMgr.Get()

	// 修改或删除前必须校验现有密码。
	if req.Action == "change" || req.Action == "remove" {
		purpose := req.Type
		if status := s.auth.CheckRateLimitFor(purpose); !status.Allowed {
			s.writeRateLimitBlocked(w, status)
			return
		}
		if req.Type == "lock" {
			if !s.auth.VerifyLockPassword(cfg, req.OldPassword) {
				s.writePasswordFailure(w, "当前锁屏密码错误", s.auth.RecordFailedAttemptFor(purpose))
				return
			}
		} else {
			if !s.auth.VerifySecondaryPassword(cfg, req.OldPassword) {
				s.writePasswordFailure(w, "当前二次密码错误", s.auth.RecordFailedAttemptFor(purpose))
				return
			}
		}
		s.auth.ResetRateLimitFor(purpose)
	} else if req.Action == "set" {
		// set 操作要求该密码尚未设置
		if s.auth.HasLockPassword(cfg) {
			s.writeJSON(w, map[string]any{"ok": false, "error": "锁屏密码已设置，请使用修改功能"})
			return
		}
	}

	if req.Action == "remove" {
		listenHost, _, listenErr := validateListenAddr(cfg.Search.ListenAddr)
		if listenErr == nil && !isLoopbackHost(listenHost) {
			s.writeJSON(w, map[string]any{"ok": false, "error": "当前已开放局域网监听，请先将监听地址改回 127.0.0.1 后再删除密码"})
			return
		}
		cfg.Security.LockPasswordHash = ""
		updateErr := s.cfgMgr.Update(cfg)
		var runtimeErr *ConfigRuntimeApplyError
		if updateErr != nil && !errors.As(updateErr, &runtimeErr) {
			s.writeError(w, updateErr)
			return
		}
		s.auth.DestroyAllSessionsExcept(GetSessionToken(r))
		s.logger.Info("auth_password_removed", map[string]any{"type": req.Type})
		response := map[string]any{"ok": true}
		if runtimeErr != nil {
			response["runtimeError"] = runtimeErr.Error()
			s.logger.Warn("auth_password_runtime_apply_failed", map[string]any{"type": req.Type, "error": runtimeErr.Error()})
		}
		s.writeJSON(w, response)
		return
	}

	if err := validateAuthPassword(req.NewPassword); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "weak_password", err.Error(), nil)
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
	updateErr := s.cfgMgr.Update(cfg)
	var runtimeErr *ConfigRuntimeApplyError
	if updateErr != nil && !errors.As(updateErr, &runtimeErr) {
		s.writeError(w, updateErr)
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
	response := map[string]any{"ok": true}
	if runtimeErr != nil {
		response["runtimeError"] = runtimeErr.Error()
		s.logger.Warn("auth_password_runtime_apply_failed", map[string]any{"type": req.Type, "error": runtimeErr.Error()})
	}
	s.writeJSON(w, response)
}
