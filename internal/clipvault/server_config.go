package clipvault

import (
	"errors"
	"net/http"
	"strconv"
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

	cfg.Security.LockPasswordHash = curCfg.Security.LockPasswordHash
	cfg.Security.SecondaryPasswordHash = curCfg.Security.SecondaryPasswordHash

	cfg.Backup.DirBackup.Password = resolveSecretField(cfg.Backup.DirBackup.Password, curCfg.Backup.DirBackup.Password)
	cfg.Backup.EmailBackup.SMTPPass = resolveSecretField(cfg.Backup.EmailBackup.SMTPPass, curCfg.Backup.EmailBackup.SMTPPass)
	cfg.Backup.EmailBackup.Password = resolveSecretField(cfg.Backup.EmailBackup.Password, curCfg.Backup.EmailBackup.Password)
	normalizeConfig(&cfg)
	if err := validateConfig(cfg); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_config", "配置参数无效: "+err.Error(), err)
		return
	}

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

	token := GetSessionToken(r)
	if curCfg.Security.LockSessionTimeout != cfg.Security.LockSessionTimeout {
		if s.auth.RefreshSessionExpiry(token, cfg.Security.LockSessionTimeout) {
			SetSessionCookie(w, token, cfg.Security.LockSessionTimeout, s.requestUsesHTTPS(r))
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
