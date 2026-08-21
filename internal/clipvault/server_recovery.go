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
		s.writeAPIError(w, http.StatusForbidden, "fresh_secondary_required", s.i18nT("api.error.switch_needs_secondary", nil), nil)
		return
	}
	var req struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	if err := ScheduleIsolatedDatabaseSwitch(s.env.DataDir, strings.TrimSpace(req.ID), req.Password); err != nil {
		s.logger.Error("isolated_database_switch_failed", err, nil)
		switch {
		case errors.Is(err, ErrIsolatedDatabaseNotFound):
			s.writeAPIError(w, http.StatusNotFound, "isolated_database_not_found", s.i18nT("api.error.isolated_gone", nil), err)
		case errors.Is(err, ErrIsolatedDatabasePending):
			s.writeAPIError(w, http.StatusConflict, "isolated_database_switch_pending", s.i18nT("api.error.switch_pending", nil), err)
		case errors.Is(err, ErrIsolatedDatabaseUnavailable):
			s.writeAPIError(w, http.StatusBadRequest, "isolated_database_unavailable", s.i18nT("api.error.isolated_verify_failed", nil), err)
		default:
			s.writeInternalError(w, err)
		}
		return
	}
	s.logger.Info("isolated_database_switch_scheduled", map[string]any{"isolationId": strings.TrimSpace(req.ID)})
	s.writeJSON(w, map[string]any{
		"ok":      true,
		"message": s.i18nT("api.error.switch_ready", nil),
	})
}

func (s *SearchServer) handleIsolatedDatabaseSwitchCancel(w http.ResponseWriter, r *http.Request) {
	fresh, _ := r.Context().Value(freshSecondaryContextKey{}).(bool)
	if !fresh {
		s.writeAPIError(w, http.StatusForbidden, "fresh_secondary_required", s.i18nT("api.error.cancel_switch_needs_secondary", nil), nil)
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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	if err := ChangeRecoveryPassword(s.env.DataDir, s.store.DBKey(), req.OldPassword, req.NewPassword); err != nil {
		s.logger.Error("recovery_change_failed", err, nil)
		var rpe *RecoveryPasswordError
		if errors.As(err, &rpe) {
			message := s.i18nT("api.error.recovery_change_failed", nil)
			switch rpe.Code {
			case "wrong_password":
				message = s.i18nT("api.error.wrong_recovery_password", nil)
			case "not_set":
				message = s.i18nT("api.error.recovery_not_set", nil)
			case "weak_password":
				message = s.i18nT("api.error.recovery_rule", map[string]any{"n": 12})
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
		s.writeAPIError(w, http.StatusForbidden, "fresh_secondary_required", s.i18nT("api.error.recovery_reset_needs_secondary", nil), nil)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	if err := ResetRecoveryPassword(s.env.DataDir, s.store.DBKey(), req.Password); err != nil {
		s.logger.Error("recovery_reset_failed", err, nil)
		var rpe *RecoveryPasswordError
		if errors.As(err, &rpe) && rpe.Code == "weak_password" {
			s.writeAPIError(w, http.StatusBadRequest, "recovery_reset_failed", s.i18nT("api.error.recovery_reset_rule", map[string]any{"n": 12}), err)
		} else {
			s.writeInternalError(w, err)
		}
		return
	}
	s.logger.Info("recovery_password_reset", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}
