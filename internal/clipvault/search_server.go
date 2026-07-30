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

const maxBackupIntervalHours = 720

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
	assetMu   sync.Mutex
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
	mux.HandleFunc("GET /favicon.ico", s.handleBrandFavicon)
	mux.HandleFunc("GET /brand/logo", s.handleBrandLogo)
	mux.HandleFunc("GET /background/media", s.handleBackgroundMedia)
	mux.HandleFunc("GET /style.css", s.handleStatic)
	mux.HandleFunc("GET /theme-utils.js", s.handleStatic)
	mux.HandleFunc("GET /script.js", s.handleStatic)
	mux.HandleFunc("GET /api/csrf", s.handleCSRF)
	mux.HandleFunc("GET /api/appearance", s.handlePublicAppearance)
	mux.HandleFunc("GET /api/license/status", s.handleLicenseStatus)
	mux.HandleFunc("GET /api/license/machine", s.handleLicenseMachine)
	mux.HandleFunc("POST /api/license/activate", s.handleLicenseActivate)
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
	mux.HandleFunc("POST /api/recovery/set", s.requireSecondary(s.handleRecoverySet))
	mux.HandleFunc("POST /api/recovery/change", s.requireSecondary(s.handleRecoveryChange))
	mux.HandleFunc("POST /api/recovery/remove", s.requireSecondary(s.handleRecoveryRemove))

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

func (s *SearchServer) handleStorageStatus(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, s.env.StorageStatus())
}

func (s *SearchServer) handleStorageRelocate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DataDir string `json:"dataDir"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]any{"ok": false, "error": "数据目录请求无效"})
		return
	}
	if err := ScheduleDataDirectoryMove(s.env, req.DataDir); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
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
		http.Error(w, "invalid background media", http.StatusBadRequest)
		return
	}
	contentType := detectBackgroundContentType(data)
	if contentType == "" {
		http.Error(w, "unsupported background media", http.StatusBadRequest)
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
	hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		_ = os.Remove(filepath.Join(s.env.DataDir, fileName))
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(backgroundMediaAsset, previousVersion)))
	}
	s.writeJSON(w, map[string]any{"ok": true, "background": s.cfgMgr.Get().Background, "hotkeyError": hotkeyError})
}

func (s *SearchServer) handleBrandAssetUpload(w http.ResponseWriter, r *http.Request, kind string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBrandAssetBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 {
		http.Error(w, "invalid image", http.StatusBadRequest)
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if contentType == "image/vnd.microsoft.icon" {
		contentType = "image/x-icon"
	}
	if !isBrandContentType(contentType, kind) || !isValidBrandImage(data, contentType) {
		http.Error(w, "unsupported image", http.StatusBadRequest)
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
	hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		_ = os.Remove(filepath.Join(s.env.DataDir, fileName))
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(assetKind, previousVersion)))
	}
	s.writeJSON(w, map[string]any{"ok": true, "brand": s.cfgMgr.Get().Brand, "hotkeyError": hotkeyError})
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
	hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(backgroundMediaAsset, previousVersion)))
	}
	s.writeJSON(w, map[string]any{"ok": true, "background": s.cfgMgr.Get().Background, "hotkeyError": hotkeyError})
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
	hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(assetKind, previousVersion)))
	}
	s.writeJSON(w, map[string]any{"ok": true, "brand": s.cfgMgr.Get().Brand, "hotkeyError": hotkeyError})
}

// updateAssetConfig keeps the new asset when persistence succeeds but Windows
// cannot immediately re-register a hotkey.
func (s *SearchServer) updateAssetConfig(cfg Config) (string, error) {
	err := s.cfgMgr.Update(cfg)
	if err == nil {
		return "", nil
	}
	var hotkeyError *HotkeyError
	if errors.As(err, &hotkeyError) {
		return hotkeyError.Error(), nil
	}
	return "", err
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
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	var cfg Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	curCfg := s.cfgMgr.Get()
	preserveAssetMetadata(&cfg, curCfg)
	listenHost, _, err := validateListenAddr(cfg.Search.ListenAddr)
	if err != nil {
		http.Error(w, "invalid search.listenAddr: "+err.Error(), http.StatusBadRequest)
		return
	}
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
	if err := validateSensitiveRules(cfg.Sensitive); err != nil {
		http.Error(w, "invalid sensitive rule: "+err.Error(), http.StatusBadRequest)
		return
	}
	// 保留当前密码哈希，不被前端回传的空值覆盖
	if cfg.Security.LockPasswordHash == "" {
		cfg.Security.LockPasswordHash = curCfg.Security.LockPasswordHash
	}
	if cfg.Security.SecondaryPasswordHash == "" {
		cfg.Security.SecondaryPasswordHash = curCfg.Security.SecondaryPasswordHash
	}
	// 保留备份相关敏感密码，不被前端回传的占位符或空值覆盖。
	cfg.Backup.DirBackup.Password = resolveSecretField(cfg.Backup.DirBackup.Password, curCfg.Backup.DirBackup.Password)
	cfg.Backup.EmailBackup.SMTPPass = resolveSecretField(cfg.Backup.EmailBackup.SMTPPass, curCfg.Backup.EmailBackup.SMTPPass)
	cfg.Backup.EmailBackup.Password = resolveSecretField(cfg.Backup.EmailBackup.Password, curCfg.Backup.EmailBackup.Password)
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
	// 超时设置变更后立即对当前会话生效，避免要等到下次登录/验证才应用。
	token := GetSessionToken(r)
	if curCfg.Security.LockSessionTimeout != cfg.Security.LockSessionTimeout {
		if s.auth.RefreshSessionExpiry(token, cfg.Security.LockSessionTimeout) {
			SetSessionCookie(w, token, cfg.Security.LockSessionTimeout)
		}
	}
	if curCfg.Security.SecondaryAuthTimeout != cfg.Security.SecondaryAuthTimeout {
		s.auth.RefreshSecondaryExpiry(token, cfg.Security.SecondaryAuthTimeout)
	}
	s.logger.Info("config_updated", nil)
	s.writeJSON(w, map[string]any{"ok": true, "config": stripSecrets(s.cfgMgr.Get())})
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
		if id, err := strconv.ParseInt(sinceID, 10, 64); err == nil {
			scope.SinceID = id
		}
	}
	switch scope.Mode {
	case "range":
		if scope.From == "" || scope.To == "" {
			s.writeError(w, errors.New("range export requires from and to"))
			return
		}
	case "incremental":
		if scope.SinceID <= 0 {
			s.writeError(w, errors.New("incremental export requires sinceId"))
			return
		}
	}

	switch format {
	case "xlsx":
		// XLSX 暂保留全量导出（流式改造在后续阶段）
		data, err := s.store.ExportXLSX(r.Context())
		if err != nil {
			s.writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="clipvault-data.xlsx"`)
		_, _ = w.Write(data)
		s.logger.Info("data_exported", map[string]any{"format": "xlsx", "mode": scope.Mode})
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="clipvault-data.json"`)
		if err := s.store.StreamExport(r.Context(), scope, w); err != nil {
			s.logger.Error("data_export_failed", err, map[string]any{"format": "json", "mode": scope.Mode})
			return
		}
		s.logger.Info("data_exported", map[string]any{"format": "json", "mode": scope.Mode})
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
		// AES-256-GCM 需要完整明文：先流式收集到 buffer 再加密
		var buf bytes.Buffer
		if err := s.store.StreamExport(r.Context(), scope, &buf); err != nil {
			s.writeError(w, err)
			return
		}
		encData, err := EncryptExport(buf.Bytes(), password)
		if err != nil {
			s.writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="clipvault-data.cvexport"`)
		_, _ = w.Write(encData)
		s.logger.Info("data_exported", map[string]any{"format": "cvexport", "mode": scope.Mode, "size": len(encData)})
	default:
		http.Error(w, "unsupported export format", http.StatusBadRequest)
	}
}

// handleImportData imports a JSON, XLSX, or encrypted .cvexport export file.
func (s *SearchServer) handleImportData(w http.ResponseWriter, r *http.Request) {
	// Limit upload to 500MB (batched import can handle large files now)
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeImportError(w, fmt.Errorf("read body: %w", err))
		return
	}

	format := r.Header.Get("X-ClipVault-Import-Format")
	if format == "" {
		s.writeImportError(w, errors.New("import format is required"))
		return
	}
	if format != "json" && format != "xlsx" && format != "cvexport" {
		s.writeImportError(w, errors.New("unsupported import format"))
		return
	}

	var importErr error
	isCVExport := IsCVExport(data)
	if isCVExport && format != "cvexport" {
		s.writeImportError(w, errors.New("import format does not match encrypted export"))
		return
	}

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
		data, importErr = DecryptExport(data, password)
		if importErr != nil {
			importErr = fmt.Errorf("decrypt cvexport: %w", importErr)
			s.writeImportError(w, importErr)
			s.logger.Error("import_failed", importErr, map[string]any{"format": format})
			return
		}
		importErr = s.store.ImportFullJSON(r.Context(), data)
	case "xlsx":
		importErr = s.store.ImportXLSX(r.Context(), data)
	case "json":
		importErr = s.store.ImportFullJSON(r.Context(), data)
	}
	if importErr != nil {
		s.writeImportError(w, importErr)
		s.logger.Error("import_failed", importErr, map[string]any{"format": format})
		return
	}

	response := map[string]any{"ok": true}
	if err := s.cfgMgr.Reload(); err != nil {
		var hotkeyError *HotkeyError
		if errors.As(err, &hotkeyError) {
			response["hotkeyError"] = hotkeyError.Error()
			s.logger.Warn("import_runtime_refresh_failed", map[string]any{"format": format, "error": hotkeyError.Error()})
		} else {
			response["runtimeError"] = err.Error()
			s.logger.Warn("import_runtime_refresh_failed", map[string]any{"format": format, "error": err.Error()})
		}
	}
	s.logger.Info("data_imported", map[string]any{"format": format})
	s.writeJSON(w, response)
}

func (s *SearchServer) writeImportError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
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
	if HasRecoveryPassword(s.env.DataDir) {
		s.writeJSON(w, map[string]any{"ok": false, "error": "恢复密码已设置，请使用修改功能"})
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
		if errors.Is(err, errItemOrGroupNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
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
