package clipvault

import (
	"errors"
	"net/http"
	"strings"
)

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
