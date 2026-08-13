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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_storage_request", "数据目录请求无效", err)
		return
	}
	if err := ScheduleDataDirectoryMove(s.env, req.DataDir); err != nil {

		s.writeAPIError(w, http.StatusBadRequest, "relocate_failed", "数据目录迁移失败："+err.Error(), err)
		return
	}
	s.logger.Info("storage_relocation_scheduled", nil)
	s.writeJSON(w, map[string]any{"ok": true, "message": "数据目录迁移已准备完成，请退出应用后重新启动"})
}

// handleStorageOpen opens the current data directory in the system file explorer.
func (s *SearchServer) handleStorageOpen(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(s.env.DataDir)
	if dir == "" {
		s.writeError(w, fmt.Errorf("数据目录不可用"))
		return
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		s.writeError(w, fmt.Errorf("数据目录不存在或不可访问: %s", dir))
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
