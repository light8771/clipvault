package clipvault

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// handleExportData exports complete data as JSON, XLSX, or encrypted cvexport.
func (s *SearchServer) handleExportData(w http.ResponseWriter, r *http.Request) {

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

	s.writeAPIError(w, http.StatusBadRequest, "import_error", "导入失败，请检查文件格式、密码或数据完整性", err)
}
