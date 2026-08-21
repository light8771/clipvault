package clipvault

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func (s *SearchServer) handleStorageStatus(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, s.env.StorageStatus())
}

func (s *SearchServer) handleStorageRelocate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DataDir string `json:"dataDir"`
	}
	if err := decodeJSONRequest(w, r, &req, 4<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_storage_request", s.i18nT("api.error.invalid_storage_request", nil), err)
		return
	}
	if err := ScheduleDataDirectoryMove(s.env, req.DataDir); err != nil {

		s.writeAPIError(w, http.StatusBadRequest, "relocate_failed", s.i18nT("storage.relocate_failed_with", map[string]any{"detail": err.Error()}), err)
		return
	}
	s.logger.Info("storage_relocation_scheduled", nil)
	s.writeJSON(w, map[string]any{"ok": true, "message": s.i18nT("storage.relocate_ready", nil)})
}

// handleStorageOpen opens the current data directory in the system file explorer.
func (s *SearchServer) handleStorageOpen(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(s.env.DataDir)
	if dir == "" {
		s.writeError(w, fmt.Errorf("%s", s.i18nT("storage.unavailable", nil)))
		return
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		s.writeError(w, fmt.Errorf("%s: %s", s.i18nT("storage.dir_inaccessible", nil), dir))
		return
	}
	if err := openDirectory(dir); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *SearchServer) handleDiagnosticExport(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	archive, err := os.CreateTemp("", "clipvault-logs-*.zip")
	if err != nil {
		s.writeError(w, err)
		return
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	defer archive.Close()

	if err := WriteDiagnosticArchive(archive, s.env, now); err != nil {
		s.writeError(w, err)
		return
	}
	if err := archive.Close(); err != nil {
		s.writeError(w, err)
		return
	}

	download, err := os.Open(archivePath)
	if err != nil {
		s.writeError(w, err)
		return
	}
	defer download.Close()
	info, err := download.Stat()
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", diagnosticArchiveName(now)))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if _, err := io.Copy(w, download); err != nil {
		s.logger.Warn("diagnostics_export_write_failed", map[string]any{"error": err.Error()})
		return
	}
	s.logger.Info("diagnostics_exported", nil)
}
