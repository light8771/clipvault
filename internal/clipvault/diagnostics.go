package clipvault

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const diagnosticLogDays = 7

const (
	maxDiagnosticLogLineBytes    = 64 << 10
	maxDiagnosticArchiveLogBytes = 8 << 20
	diagnosticLogReadBufferBytes = 8 << 10
)

const (
	redactedInvalidDiagnosticLogEntry   = `{"event":"redacted_invalid_log_entry"}`
	redactedOversizedDiagnosticLogEntry = `{"event":"redacted_oversized_log_entry"}`
	truncatedDiagnosticLogEntry         = `{"event":"diagnostic_log_archive_truncated"}`
)

var diagnosticLevels = diagnosticStringSet("error", "info", "warn")

var diagnosticEvents = diagnosticStringSet(
	"app_run_failed",
	"app_started",
	"app_stopped",
	"auth_login",
	"auth_password_removed",
	"auth_password_set",
	"auth_secondary",
	"autostart_status_failed",
	"autostart_sync_failed",
	"autostart_toggled",
	"clipboard_listener_failed",
	"clipboard_read_failed",
	"clipboard_save_failed",
	"clipboard_saved",
	"clipboard_skipped",
	"config_load_failed",
	"config_save_failed",
	"config_updated",
	"data_exported",
	"data_imported",
	"diagnostics_export_write_failed",
	"diagnostics_exported",
	"dir_backup_failed",
	"dir_backup_manual_failed",
	"dir_backup_manual_ok",
	"dir_backup_ok",
	"dir_backup_startup_failed",
	"email_backup_failed",
	"email_backup_history_record_failed",
	"email_backup_manual_failed",
	"email_backup_manual_ok",
	"email_backup_ok",
	"email_backup_startup_failed",
	"email_test_failed",
	"email_test_ok",
	"group_created",
	"group_deleted",
	"group_updated",
	"history_cleared",
	"hotkey_disabled",
	"hotkey_register_failed",
	"hotkey_registered",
	"import_failed",
	"import_runtime_refresh_failed",
	"license_activated",
	"license_activation_failed",
	"license_loaded",
	"pause_toggled",
	"quick_search_skipped",
	"recovery_appearance_sync_failed",
	"recovery_change_failed",
	"recovery_password_changed",
	"recovery_password_removed",
	"recovery_password_set",
	"recovery_remove_failed",
	"recovery_server_start_failed",
	"recovery_server_started",
	"recovery_set_failed",
	"restart_begin",
	"restart_done",
	"restart_requested",
	"restart_search_failed",
	"search_open_skipped",
	"search_server_failed",
	"search_server_port_fallback",
	"search_server_started",
	"search_server_start_failed",
	"search_window_class_register_failed",
	"search_window_query_failed",
	"single_instance_blocked",
	"startup_mode",
	"state_update_failed",
	"storage_relocation_cleanup_pending",
	"storage_relocation_failed",
	"storage_relocation_scheduled",
	"store_open_after_recovery_failed",
	"store_open_failed",
	"store_opened_after_recovery",
	"tray_add_failed",
)

var diagnosticStringFieldValues = map[string]map[string]struct{}{
	"action": diagnosticStringSet("set", "change", "remove"),
	"format": diagnosticStringSet("CF_HDROP", "CF_UNICODETEXT", "cvexport", "json", "xlsx"),
	"kind":   diagnosticStringSet("archive", "files", "image", "office", "text", "video"),
	"mode":   diagnosticStringSet("first_install", "normal", "recovery_needed", "state_corrupt"),
	"reason": diagnosticStringSet("max_text_chars", "search_server_disabled", "store_or_url_missing"),
	"source": diagnosticStringSet("hotkey", "tray"),
	"status": diagnosticStringSet(
		"corrupt",
		"invalid",
		"licensed",
		"machine_error",
		"machine_mismatch",
		"missing",
		"unconfigured",
	),
	"type": diagnosticStringSet("lock", "secondary"),
}

var diagnosticEventStringFieldValues = map[string]map[string]map[string]struct{}{
	"license_activation_failed": {
		"error": diagnosticStringSet(
			"license_bad_format",
			"license_bad_signature",
			"license_machine_mismatch",
			"license_machine_unavailable",
			"license_save_failed",
			"license_unsupported",
		),
	},
}

var diagnosticIntegerFields = []string{
	"byteLen",
	"charLen",
	"deleted",
	"id",
	"key",
	"modifiers",
	"parentId",
	"pid",
	"size",
	"total",
}

var diagnosticBooleanFields = []string{
	"enabled",
	"enforced",
	"paused",
	"sensitive",
}

// WriteDiagnosticArchive writes a support bundle containing only redacted
// recent logs and basic runtime metadata. It intentionally excludes database,
// clipboard, configuration, and key files.
func WriteDiagnosticArchive(w io.Writer, env Environment, now time.Time) error {
	return writeDiagnosticArchive(w, env, now, maxDiagnosticArchiveLogBytes)
}

func writeDiagnosticArchive(w io.Writer, env Environment, now time.Time, maxLogBytes int) error {
	zw := zip.NewWriter(w)
	budget := newDiagnosticLogBudget(maxLogBytes)
	metadata := map[string]any{
		"appVersion":  AppVersion,
		"generatedAt": now.UTC().Format(time.RFC3339),
		"goos":        runtime.GOOS,
		"goarch":      runtime.GOARCH,
	}
	if err := writeDiagnosticJSON(zw, "diagnostics.json", metadata); err != nil {
		return err
	}
	for day := 0; day < diagnosticLogDays; day++ {
		if budget.truncated {
			break
		}
		name := now.AddDate(0, 0, -day).Format(time.DateOnly) + ".log"
		file, err := os.Open(filepath.Join(env.LogDirectory(), name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read diagnostic log %s: %w", name, err)
		}
		entry, err := zw.Create(filepath.ToSlash(filepath.Join("logs", name)))
		if err != nil {
			_ = file.Close()
			return err
		}
		if err := writeRedactedDiagnosticLog(entry, file, budget); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close diagnostic log %s: %w", name, err)
		}
	}
	return zw.Close()
}

func writeDiagnosticJSON(zw *zip.Writer, name string, value any) error {
	entry, err := zw.Create(name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = entry.Write(append(data, '\n'))
	return err
}

func redactDiagnosticLog(data []byte) []byte {
	lines := bytes.Split(data, []byte{'\n'})
	for i, line := range lines {
		lines[i] = redactDiagnosticLogLine(line)
	}
	return bytes.Join(lines, []byte{'\n'})
}

type diagnosticLogBudget struct {
	limit     int
	written   int
	truncated bool
}

func newDiagnosticLogBudget(limit int) *diagnosticLogBudget {
	return &diagnosticLogBudget{limit: limit}
}

func (b *diagnosticLogBudget) writeRecord(w io.Writer, record []byte, hasNewline bool) error {
	recordBytes := len(record)
	if hasNewline {
		recordBytes++
	}
	truncationBytes := len(truncatedDiagnosticLogEntry) + 1
	if b.written+recordBytes+truncationBytes > b.limit {
		if _, err := io.WriteString(w, truncatedDiagnosticLogEntry+"\n"); err != nil {
			return err
		}
		b.written += truncationBytes
		b.truncated = true
		return nil
	}
	if _, err := w.Write(record); err != nil {
		return err
	}
	if hasNewline {
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	b.written += recordBytes
	return nil
}

func writeRedactedDiagnosticLog(w io.Writer, file io.Reader, budget *diagnosticLogBudget) error {
	reader := bufio.NewReaderSize(file, diagnosticLogReadBufferBytes)
	line := make([]byte, 0, maxDiagnosticLogLineBytes)
	for {
		if budget.truncated {
			return nil
		}
		// Pre-check before reading further: once the buffered partial line has
		// reached the limit without a newline, stop reading immediately. Checking
		// only after the next ReadSlice would let bufio drain the underlying
		// reader (potentially to EOF) before the oversized line is detected.
		if len(line) >= maxDiagnosticLogLineBytes {
			return writeRedactedDiagnosticLogRecord(w, nil, true, true, budget)
		}
		chunk, readErr := reader.ReadSlice('\n')
		hasNewline := len(chunk) > 0 && chunk[len(chunk)-1] == '\n'
		if hasNewline {
			chunk = chunk[:len(chunk)-1]
		}
		if len(line)+len(chunk) > maxDiagnosticLogLineBytes {
			return writeRedactedDiagnosticLogRecord(w, nil, true, true, budget)
		}
		line = append(line, chunk...)

		switch readErr {
		case nil:
			if err := writeRedactedDiagnosticLogRecord(w, line, false, hasNewline, budget); err != nil {
				return err
			}
			if budget.truncated {
				return nil
			}
			line = line[:0]
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(line) > 0 {
				return writeRedactedDiagnosticLogRecord(w, line, false, false, budget)
			}
			return nil
		default:
			return readErr
		}
	}
}

func writeRedactedDiagnosticLogRecord(w io.Writer, line []byte, oversized, hasNewline bool, budget *diagnosticLogBudget) error {
	record := redactDiagnosticLogLine(line)
	if oversized {
		record = []byte(redactedOversizedDiagnosticLogEntry)
	}
	return budget.writeRecord(w, record, hasNewline)
}

func redactDiagnosticLogLine(line []byte) []byte {
	if len(line) == 0 {
		return line
	}
	var entry map[string]json.RawMessage
	if json.Unmarshal(line, &entry) != nil {
		return []byte(redactedInvalidDiagnosticLogEntry)
	}
	redactedEntry, ok := sanitizeDiagnosticLogEntry(entry)
	if !ok {
		return []byte(redactedInvalidDiagnosticLogEntry)
	}
	redacted, err := json.Marshal(redactedEntry)
	if err != nil {
		return []byte(redactedInvalidDiagnosticLogEntry)
	}
	return redacted
}

func sanitizeDiagnosticLogEntry(fields map[string]json.RawMessage) (map[string]any, bool) {
	timestamp, ok := diagnosticTimestamp(fields["ts"])
	if !ok {
		return nil, false
	}
	level, ok := diagnosticEnum(fields["level"], diagnosticLevels)
	if !ok {
		return nil, false
	}
	event, ok := diagnosticEnum(fields["event"], diagnosticEvents)
	if !ok {
		return nil, false
	}

	sanitized := map[string]any{
		"ts":    timestamp,
		"level": level,
		"event": event,
	}
	for field, values := range diagnosticStringFieldValues {
		if value, ok := diagnosticEnum(fields[field], values); ok {
			sanitized[field] = value
		}
	}
	for field, values := range diagnosticEventStringFieldValues[event] {
		if value, ok := diagnosticEnum(fields[field], values); ok {
			sanitized[field] = value
		}
	}
	for _, field := range diagnosticIntegerFields {
		if value, ok := diagnosticNonNegativeInteger(fields[field]); ok {
			sanitized[field] = value
		}
	}
	for _, field := range diagnosticBooleanFields {
		if value, ok := diagnosticBoolean(fields[field]); ok {
			sanitized[field] = value
		}
	}
	return sanitized, true
}

func diagnosticTimestamp(raw json.RawMessage) (string, bool) {
	value, ok := diagnosticString(raw)
	if !ok {
		return "", false
	}
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", false
	}
	return timestamp.UTC().Format(time.RFC3339Nano), true
}

func diagnosticEnum(raw json.RawMessage, values map[string]struct{}) (string, bool) {
	value, ok := diagnosticString(raw)
	if !ok {
		return "", false
	}
	_, ok = values[value]
	return value, ok
}

func diagnosticString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func diagnosticNonNegativeInteger(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

func diagnosticBoolean(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}

func diagnosticStringSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func diagnosticArchiveName(now time.Time) string {
	return "clipvault-logs-" + now.Format("20060102") + ".zip"
}
