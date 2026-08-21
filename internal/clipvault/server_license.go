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
		s.writeAPIError(w, http.StatusServiceUnavailable, "license_unavailable", s.i18nT("api.error.license_unavailable", nil), nil)
		return
	}
	ctx := s.license.Context()
	if ctx.MachineID == "" {
		s.writeAPIError(w, http.StatusServiceUnavailable, "machine_id_unavailable", s.i18nT("api.error.machine_id_unavailable", nil), nil)
		return
	}
	s.writeJSON(w, map[string]any{"machineId": ctx.MachineID, "machineCode": ctx.MachineCode})
}

func (s *SearchServer) handleLicenseActivate(w http.ResponseWriter, r *http.Request) {
	if s.license == nil {
		s.writeAPIError(w, http.StatusServiceUnavailable, "license_unavailable", s.i18nT("api.error.license_unavailable", nil), nil)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSONRequest(w, r, &req, 8<<10); err != nil || strings.TrimSpace(req.Token) == "" {
		s.writeAPIError(w, http.StatusBadRequest, "license_bad_format", s.i18nT("api.error.license_bad_format", nil), err)
		return
	}
	ctx, err := s.license.Activate(req.Token)
	if err != nil {
		code := licenseErrorCode(err)
		s.logger.Warn("license_activation_failed", map[string]any{"error": code, "machineCode": ctx.MachineCode})
		s.writeAPIError(w, http.StatusBadRequest, code, licenseErrorMessage(code), err)
		return
	}
	if s.backup != nil {
		s.backup.Reload(s.cfgMgr.Get())
	}
	s.logger.Info("license_activated", map[string]any{"licenseId": ctx.LicenseID, "machineCode": ctx.MachineCode})
	s.writeJSON(w, map[string]any{"ok": true, "licenseId": ctx.LicenseID, "edition": ctx.Edition})
}

func licenseErrorMessage(code string) string {
	switch code {
	case "license_bad_format":
		return templateT("api.error.license_bad_format", nil)
	case "license_bad_signature":
		return templateT("api.error.license_bad_signature", nil)
	case "license_machine_mismatch":
		return templateT("api.error.license_machine_mismatch", nil)
	case "license_unsupported":
		return templateT("api.error.license_unsupported", nil)
	case "license_machine_unavailable":
		return templateT("api.error.machine_id_unavailable", nil)
	default:
		return templateT("api.error.license_save_failed", nil)
	}
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
