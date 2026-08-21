package clipvault

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_id", nil), nil)
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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_id", nil), nil)
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

// cleanupRequest 是 /items/cleanup-preview 与 /items/cleanup 共用的请求体。
// from/to 支持两种精度：
//   - 本地日期（2006-01-02）：按配置时区解释为天级范围（to 当天闭区间）；
//   - RFC3339 瞬间：精确到秒，to 按「结束秒 +1000ms」转为开区间上界。
type cleanupRequest struct {
	Mode            string `json:"mode"`
	From            string `json:"from"`
	To              string `json:"to"`
	PreserveStarred bool   `json:"preserveStarred"`
}

// parseCleanupOptions 把 HTTP 请求体解析为存储层清理条件。
// preview 与 delete 走同一解析 + 同一 cleanupWhere，保证命中集合一致。
func (s *SearchServer) parseCleanupOptions(w http.ResponseWriter, r *http.Request) (CleanupOptions, bool) {
	var req cleanupRequest
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return CleanupOptions{}, false
	}
	opts := CleanupOptions{Mode: req.Mode, PreserveStarred: req.PreserveStarred}
	switch req.Mode {
	case "all":
		if req.From != "" || req.To != "" {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_cleanup_range", s.i18nT("api.error.cleanup_all_no_range", nil), nil)
			return CleanupOptions{}, false
		}
	case "range":
		if req.From == "" || req.To == "" {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_cleanup_range", s.i18nT("api.error.cleanup_range_need_both", nil), nil)
			return CleanupOptions{}, false
		}
		_, fromDateErr := time.Parse("2006-01-02", req.From)
		_, toDateErr := time.Parse("2006-01-02", req.To)
		if fromDateErr == nil && toDateErr == nil {
			fromMS, beforeMS, err := localDateFilterBounds(req.From, req.To, s.configuredTimeZone())
			if err != nil {
				s.writeAPIError(w, http.StatusBadRequest, "invalid_cleanup_range", s.i18nT("api.error.cleanup_range_invalid", nil), err)
				return CleanupOptions{}, false
			}
			opts.FromMS, opts.ToExclusiveMS = fromMS, beforeMS
		} else {
			if (fromDateErr == nil) != (toDateErr == nil) {
				s.writeAPIError(w, http.StatusBadRequest, "invalid_cleanup_range", s.i18nT("api.error.cleanup_range_boundary_mixed", nil), nil)
				return CleanupOptions{}, false
			}
			from, err := parseExportInstant(req.From)
			if err != nil {
				s.writeAPIError(w, http.StatusBadRequest, "invalid_cleanup_range", s.i18nT("api.error.cleanup_range_from_invalid", nil), err)
				return CleanupOptions{}, false
			}
			to, err := parseExportInstant(req.To)
			if err != nil {
				s.writeAPIError(w, http.StatusBadRequest, "invalid_cleanup_range", s.i18nT("api.error.cleanup_range_to_invalid", nil), err)
				return CleanupOptions{}, false
			}
			// 范围精确到秒：起始向下取整（含该秒），结束秒 +1000ms 作为开区间上界。
			opts.FromMS = from.UTC().Truncate(time.Second).UnixMilli()
			opts.ToExclusiveMS = to.UTC().Truncate(time.Second).Add(time.Second).UnixMilli()
		}
	default:
		s.writeAPIError(w, http.StatusBadRequest, "invalid_cleanup_mode", s.i18nT("api.error.cleanup_mode_unsupported", nil), nil)
		return CleanupOptions{}, false
	}
	if err := validateCleanupOptions(opts); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_cleanup_range", s.i18nT("api.error.cleanup_condition_invalid", nil), err)
		return CleanupOptions{}, false
	}
	return opts, true
}

// handleCleanupPreview 预览将删除的记录数，不执行删除。
func (s *SearchServer) handleCleanupPreview(w http.ResponseWriter, r *http.Request) {
	opts, ok := s.parseCleanupOptions(w, r)
	if !ok {
		return
	}
	n, err := s.store.PreviewCleanup(r.Context(), opts)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "matched": n, "preserveStarred": opts.PreserveStarred})
}

// handleCleanup 按与预览完全相同的条件执行删除。
// PreserveStarred=true 时收藏记录一定保留。
func (s *SearchServer) handleCleanup(w http.ResponseWriter, r *http.Request) {
	opts, ok := s.parseCleanupOptions(w, r)
	if !ok {
		return
	}
	n, err := s.store.Cleanup(r.Context(), opts)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.logger.Info("history_cleanup", map[string]any{
		"deleted":         n,
		"mode":            opts.Mode,
		"preserveStarred": opts.PreserveStarred,
	})
	s.writeJSON(w, map[string]any{"ok": true, "deleted": n})
}

// handleSensitiveMark 手动标记/取消标记一条记录的敏感属性。
// 取消标记会使内容重新明文可见，因此需要二次认证；标记操作只需登录。
func (s *SearchServer) handleSensitiveMark(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_id", nil), nil)
		return
	}
	sensitive := r.URL.Query().Get("sensitive") != "0"
	if !s.authorizeItemAccess(w, r, id) {
		return
	}
	if !sensitive && !s.authorizeSecondaryRequest(w, r) {
		return
	}
	if err := s.store.SetSensitive(r.Context(), id, sensitive); err != nil {
		s.writeItemLookupError(w, err)
		return
	}
	s.logger.Info("item_sensitive_marked", map[string]any{"id": id, "sensitive": sensitive})
	s.writeJSON(w, map[string]any{"ok": true, "sensitive": sensitive})
}

// handleGetConfig 返回当前完整配置（用于设置页面初始化）。
// 敏感过滤规则（customPatterns）在未通过二次认证时被遮蔽，需要二次认证后通过 /api/v1/config/sensitive-rules 获取。
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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_config_json", nil), err)
		return
	}
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	curCfg := s.cfgMgr.Get()
	preserveAssetMetadata(&cfg, curCfg)
	listenHost, listenPort, err := validateListenAddr(cfg.Search.ListenAddr)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_listen_addr", s.i18nT("api.error.invalid_listen_addr", map[string]any{"detail": err.Error()}), err)
		return
	}
	// 监听地址变更需重启生效，保存时提前探测端口可用性，避免重启后静默顺延或启动失败。
	// 地址未变更时跳过：当前端口正被本服务占用属于正常情况。
	if strings.TrimSpace(cfg.Search.ListenAddr) != strings.TrimSpace(curCfg.Search.ListenAddr) {
		if err := listenAddrAvailable(listenHost, listenPort); err != nil {
			s.writeAPIError(w, http.StatusBadRequest, "listen_addr_unavailable", s.i18nT("api.error.listen_addr_unavailable", map[string]any{"addr": cfg.Search.ListenAddr}), err)
			return
		}
	}
	if _, err := configuredLocation(cfg.Search.TimeZone); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_time_zone", s.i18nT("api.error.invalid_time_zone", nil), err)
		return
	}
	if !isLoopbackHost(listenHost) && !s.auth.HasLockPassword(curCfg) {
		s.writeAPIError(w, http.StatusBadRequest, "lock_password_required", s.i18nT("api.error.lan_needs_lock_password", nil), nil)
		return
	}

	for _, p := range cfg.Sensitive.CustomPatterns {
		if p == "__masked__" {
			cfg.Sensitive.CustomPatterns = s.cfgMgr.Get().Sensitive.CustomPatterns
			break
		}
	}
	if err := validateSensitiveRules(cfg.Sensitive); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_sensitive_rule", s.i18nT("api.error.invalid_sensitive_rule", map[string]any{"detail": err.Error()}), err)
		return
	}

	cfg.Security.LockPasswordHash = curCfg.Security.LockPasswordHash
	cfg.Security.SecondaryPasswordHash = curCfg.Security.SecondaryPasswordHash

	cfg.Backup.DirBackup.Password = resolveSecretField(cfg.Backup.DirBackup.Password, curCfg.Backup.DirBackup.Password)
	cfg.Backup.EmailBackup.SMTPPass = resolveSecretField(cfg.Backup.EmailBackup.SMTPPass, curCfg.Backup.EmailBackup.SMTPPass)
	cfg.Backup.EmailBackup.Password = resolveSecretField(cfg.Backup.EmailBackup.Password, curCfg.Backup.EmailBackup.Password)
	normalizeConfig(&cfg)
	if err := validateConfig(cfg); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_config", s.i18nT("api.error.invalid_config", map[string]any{"detail": err.Error()}), err)
		return
	}

	if cfg.Backup.DirBackup.Enabled {
		if msg := validateDirBackupEnabled(cfg.Backup.DirBackup); msg != "" {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_backup_config", s.i18nT("backup.incomplete_dir", map[string]any{"items": msg}), nil)
			return
		}
	}
	if cfg.Backup.EmailBackup.Enabled {
		if msg := validateEmailBackupEnabled(cfg.Backup.EmailBackup); msg != "" {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_backup_config", s.i18nT("backup.incomplete_email", map[string]any{"items": msg}), nil)
			return
		}
	}
	changes := diffConfigChanges(curCfg, cfg)
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

	token := GetSessionToken(r)
	if curCfg.Security.LockSessionTimeout != cfg.Security.LockSessionTimeout {
		if s.auth.RefreshSessionExpiry(token, cfg.Security.LockSessionTimeout) {
			SetSessionCookie(w, token, cfg.Security.LockSessionTimeout, requestUsesHTTPS(r))
		}
	}
	if curCfg.Security.SecondaryAuthTimeout != cfg.Security.SecondaryAuthTimeout {
		if cfg.Security.SecondaryAuthTimeout > 0 {

			s.auth.GrantSecondaryAuth(token, cfg.Security.SecondaryAuthTimeout)
		} else {
			s.auth.RefreshSecondaryExpiry(token, cfg.Security.SecondaryAuthTimeout)
		}
	}
	s.logger.Info("config_updated", nil)
	if s.store != nil {
		if err := s.store.LogConfigChanges(r.Context(), changes); err != nil {
			s.logger.Warn("config_change_log_failed", map[string]any{"error": err.Error()})
		}
	}
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
