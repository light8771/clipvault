package clipvault

import (
	"clipboard_ext/internal/licensing"
	"errors"
	"net/http"
	"strings"
)

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
