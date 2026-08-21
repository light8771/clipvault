package clipvault

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

const (
	maxStreamImportBytes = 520 << 20
	maxXLSXImportBytes   = 64 << 20
)

// handleExportData exports complete data as JSON, XLSX, or encrypted cvexport.
func (s *SearchServer) handleExportData(w http.ResponseWriter, r *http.Request) {

	if s.license != nil && !s.license.CanUsePaidFeatures() {
		s.writeAPIError(w, http.StatusPaymentRequired, "license_required", s.i18nT("transfer.paywall_full", nil), nil)
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
			s.writeAPIError(w, http.StatusBadRequest, "invalid_since_id", s.i18nT("api.error.sinceid_incremental_only", nil), nil)
			return
		}
		id, err := strconv.ParseInt(sinceID, 10, 64)
		if err != nil || id <= 0 {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_since_id", s.i18nT("api.error.sinceid_invalid", nil), err)
			return
		}
		scope.SinceID = id
	}
	// 统一校验（与备份 resolveBackupScope 共用 validateExportScope），
	// 修复 range 模式只查非空不查 From>=To 的静默空导出问题。
	var err error
	scope, err = normalizeExportRange(scope, s.configuredTimeZone())
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_export_scope", s.i18nT("api.error.invalid_export_scope", nil), err)
		return
	}
	if err := validateExportScope(scope); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_export_scope", s.i18nT("api.error.invalid_export_scope", nil), err)
		return
	}
	if err := setExportWriteDeadline(w, time.Now()); err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "export_deadline_failed", s.i18nT("api.error.export_deadline_failed", nil), err)
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
			s.writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", s.i18nT("api.error.cvexport_post_only", nil), nil)
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_request", s.i18nT("api.error.invalid_request", nil), nil)
			return
		}
		password := req.Password
		if password == "" {
			s.writeAPIError(w, http.StatusBadRequest, "password_required", s.i18nT("api.error.password_required", nil), nil)
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
		s.writeAPIError(w, http.StatusBadRequest, "unsupported_format", s.i18nT("api.error.unsupported_export_format", nil), nil)
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
			importErr = s.importDecryptedExport(r.Context(), plain)
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

// importDecryptedExport 导入解密后的 cvexport 载荷：
// 纯 JSON（目录备份 / cvexport 邮件备份）直接导入；zip（邮件备份的
// json/xlsx 加密打包）解包出 clipvault-export.json/.xlsx 后按内层格式导入。
func (s *SearchServer) importDecryptedExport(ctx context.Context, plain *os.File) error {
	info, err := plain.Stat()
	if err != nil {
		return err
	}
	var header [4]byte
	if _, err := plain.ReadAt(header[:], 0); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if !bytes.Equal(header[:], []byte{'P', 'K', 0x03, 0x04}) {
		if _, err := plain.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return s.store.ImportFullJSONReader(ctx, plain)
	}
	zr, err := zip.NewReader(plain, info.Size())
	if err != nil {
		return fmt.Errorf("open zipped export payload: %w", err)
	}
	for _, entry := range zr.File {
		switch entry.Name {
		case "clipvault-export.json":
			rc, err := entry.Open()
			if err != nil {
				return fmt.Errorf("open zipped JSON payload: %w", err)
			}
			defer rc.Close()
			return s.store.ImportFullJSONReader(ctx, rc)
		case "clipvault-export.xlsx":
			rc, err := entry.Open()
			if err != nil {
				return fmt.Errorf("open zipped XLSX payload: %w", err)
			}
			data, err := io.ReadAll(io.LimitReader(rc, maxXLSXImportBytes+1))
			rc.Close()
			if err != nil {
				return fmt.Errorf("read zipped XLSX payload: %w", err)
			}
			if len(data) > maxXLSXImportBytes {
				return errors.New("zipped XLSX payload exceeds size limit")
			}
			return s.store.ImportXLSX(ctx, data)
		}
	}
	return errors.New("zipped export payload has no clipvault-export entry")
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

	s.writeAPIError(w, http.StatusBadRequest, "import_error", s.i18nT("api.error.import_failed_generic", nil), err)
}
