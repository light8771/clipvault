package clipvault

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

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
		resp["message"] = fmt.Sprintf("二次验证成功，%d 分钟内无需重复验证", cfg.Security.SecondaryAuthTimeout)
	} else {
		resp["message"] = "二次验证成功，当前会话内无需重复验证"
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

		if s.auth.HasLockPassword(cfg) {
			s.writeAPIError(w, http.StatusConflict, "password_already_set", "锁屏密码已设置，请使用修改功能", nil)
			return
		}
	}

	if req.Action == "remove" {
		listenHost, _, listenErr := validateListenAddr(cfg.Search.ListenAddr)
		if listenErr == nil && !isLoopbackHost(listenHost) {
			s.writeAPIError(w, http.StatusConflict, "password_required_for_lan", "当前已开放局域网监听，请先将监听地址改回 127.0.0.1 后再删除密码", nil)
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
