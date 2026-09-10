package clipvault

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"time"
)

// authClientIP uses only the connection peer, never untrusted proxy headers.
// Unmap IPv4-mapped IPv6 so alternate address spellings share an allowance.
func authClientIP(r *http.Request) string {
	if addr, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return addr.Addr().Unmap().WithZone("").String()
	}
	if addr, err := netip.ParseAddr(r.RemoteAddr); err == nil {
		return addr.Unmap().WithZone("").String()
	}
	return "unknown"
}

// verifyRequestPassword is the single HTTP password-verification boundary.
func (s *SearchServer) verifyRequestPassword(w http.ResponseWriter, r *http.Request, purpose, hash, password, errorKey string) bool {
	valid, status := s.auth.verifyPasswordAttempt(purpose, authClientIP(r), func() bool {
		return hash == "" || verifyPasswordHash(hash, password)
	})
	if !valid {
		s.writePasswordFailure(w, s.i18nT(errorKey, nil), status)
	}
	return valid
}

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
			s.writeAPIError(w, http.StatusUnauthorized, "unauthorized", s.i18nT("api.error.unauthorized", nil), nil)
			return
		}
		next(w, r)
	}
}

// requireSecondary 确保请求已通过锁屏认证且拥有有效的二次认证。
// 支持两种二次认证方式：
//  1. 会话级缓存（timeout = 0 时跟随浏览器会话，timeout > 0 时 N 分钟内免重复）
//  2. 一次性 token（通过请求头携带，用于 fresh 验证等单次操作场景）
//
// 如果未设置二次密码，则只要求锁屏认证。
func (s *SearchServer) requireSecondary(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.cfgMgr.Get()
		if s.auth.HasLockPassword(cfg) {
			sess := s.auth.GetSession(GetSessionToken(r))
			if sess == nil || !sess.Authenticated {
				s.writeAPIError(w, http.StatusUnauthorized, "unauthorized", s.i18nT("api.error.unauthorized", nil), nil)
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
// 也供"仅当目标记录敏感时才保护"的文件端点使用。
func (s *SearchServer) authorizeSecondaryRequest(w http.ResponseWriter, r *http.Request) bool {
	cfg := s.cfgMgr.Get()
	if !s.auth.HasSecondaryPassword(cfg) {
		return true
	}
	token := GetSessionToken(r)
	sess := s.auth.GetSession(token)
	if sess == nil || !sess.Authenticated {
		if s.auth.HasLockPassword(cfg) {
			s.writeAPIError(w, http.StatusUnauthorized, "unauthorized", s.i18nT("api.error.unauthorized", nil), nil)
		} else {
			s.writeAPIError(w, http.StatusForbidden, "secondary_required", s.i18nT("apijs.secondary_required", nil), nil)
		}
		return false
	}
	if freshToken := r.Header.Get(freshSecondaryHeaderName); freshToken != "" {
		if s.auth.ConsumeSecondaryToken(freshToken, token) {

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
	s.writeAPIError(w, http.StatusForbidden, "secondary_required", s.i18nT("apijs.secondary_required", nil), nil)
	return false
}

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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	cfg := s.cfgMgr.Get()
	if !s.auth.HasLockPassword(cfg) {
		s.writeJSON(w, map[string]any{"ok": true, "message": "no lock password set"})
		return
	}
	if !s.verifyRequestPassword(w, r, ratePurposeLock, cfg.Security.LockPasswordHash, req.Password, "api.error.wrong_password") {
		return
	}
	token, err := s.auth.CreateSession(cfg.Security.LockSessionTimeout)
	if err != nil {
		s.writeError(w, err)
		return
	}
	SetSessionCookie(w, token, cfg.Security.LockSessionTimeout, requestUsesHTTPS(r))
	s.logger.Info("auth_login", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleAuthLogout 销毁当前会话。
func (s *SearchServer) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	token := GetSessionToken(r)
	s.auth.DestroySession(token)
	ClearSessionCookie(w, requestUsesHTTPS(r))
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleAuthSecondary 验证二次密码并授予二次认证权限。
func (s *SearchServer) handleAuthSecondary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	cfg := s.cfgMgr.Get()
	if !s.auth.HasSecondaryPassword(cfg) {
		s.writeJSON(w, map[string]any{"ok": true, "message": "no secondary password set"})
		return
	}
	token := GetSessionToken(r)
	sess := s.auth.GetSession(token)
	if sess == nil || !sess.Authenticated {
		if s.auth.HasLockPassword(cfg) {
			s.writeAPIError(w, http.StatusUnauthorized, "lock_session_expired", s.i18nT("auth.lock_session_expired", nil), nil)
			return
		}
	}
	if !s.verifyRequestPassword(w, r, ratePurposeSecondary, cfg.Security.SecondaryPasswordHash, req.Password, "api.error.wrong_secondary_password") {
		return
	}
	if sess == nil || !sess.Authenticated {
		// 无锁屏密码时，只在二次密码验证成功后创建会话。失败尝试不应
		// 留下最长存活 24 小时的无用会话。
		var err error
		token, err = s.auth.CreateSession(cfg.Security.LockSessionTimeout)
		if err != nil {
			s.writeError(w, err)
			return
		}
		SetSessionCookie(w, token, cfg.Security.LockSessionTimeout, requestUsesHTTPS(r))
	}
	s.auth.GrantSecondaryAuth(token, cfg.Security.SecondaryAuthTimeout)

	oneTimeToken, err := s.auth.GenerateSecondaryToken(token)
	if err != nil {
		s.writeError(w, err)
		return
	}

	resp := map[string]any{
		"ok":              true,
		"secondaryToken":  oneTimeToken,
		"secondaryCached": true,
	}
	if cfg.Security.SecondaryAuthTimeout > 0 {
		resp["message"] = s.i18nT("api.error.secondary_ok_minutes", map[string]any{"n": cfg.Security.SecondaryAuthTimeout})
	} else {
		resp["message"] = s.i18nT("api.error.secondary_ok_session", nil)
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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}

	if req.Type != "lock" && req.Type != "secondary" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_type", s.i18nT("api.error.invalid_type", nil), nil)
		return
	}
	if req.Action != "set" && req.Action != "change" && req.Action != "remove" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_action", s.i18nT("api.error.invalid_action", nil), nil)
		return
	}
	if req.Type == "secondary" && req.Action != "change" {
		s.writeAPIError(w, http.StatusBadRequest, "secondary_password_required", s.i18nT("api.error.secondary_password_mandatory", nil), nil)
		return
	}

	before := s.cfgMgr.Get()
	cfg := cloneConfig(before)

	if req.Action == "change" || req.Action == "remove" {
		hash, errorKey := cfg.Security.LockPasswordHash, "api.error.wrong_current_lock_password"
		if req.Type == ratePurposeSecondary {
			hash, errorKey = cfg.Security.SecondaryPasswordHash, "api.error.wrong_current_secondary_password"
		}
		if !s.verifyRequestPassword(w, r, req.Type, hash, req.OldPassword, errorKey) {
			return
		}
	} else if req.Action == "set" {

		if s.auth.HasLockPassword(cfg) {
			s.writeAPIError(w, http.StatusConflict, "password_already_set", s.i18nT("api.error.lock_password_already_set", nil), nil)
			return
		}
	}

	if req.Action == "remove" {
		listenHost, _, listenErr := validateListenAddr(cfg.Search.ListenAddr)
		if listenErr == nil && !isLoopbackHost(listenHost) {
			s.writeAPIError(w, http.StatusConflict, "password_required_for_lan", s.i18nT("api.error.lan_active_password_delete", nil), nil)
			return
		}
		cfg.Security.LockPasswordHash = ""
		updateErr := s.cfgMgr.UpdateFrom(before, cfg)
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
	updateErr := s.cfgMgr.UpdateFrom(before, cfg)
	var runtimeErr *ConfigRuntimeApplyError
	if updateErr != nil && !errors.As(updateErr, &runtimeErr) {
		s.writeError(w, updateErr)
		return
	}

	if req.Type == "lock" {

		s.auth.DestroyAllSessionsExcept(GetSessionToken(r))
	} else {

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
