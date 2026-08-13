package clipvault

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedactDiagnosticLogKeepsOnlyValidatedOperationalFields(t *testing.T) {
	input := []byte(`{"ts":"2026-07-28T12:00:00+08:00","level":"info","event":"clipboard_saved","kind":"text","format":"CF_UNICODETEXT","charLen":42,"byteLen":42,"sensitive":false,"dataDir":"C:\\Users\\Alice\\ClipVault","licenseId":"LIC-1","to":"alice@example.com","name":"Personal Group","error":"invalid rule: clipboard secret","warning":"open C:\\Users\\Alice\\secret: alice@example.com","nested":{"secret":"clipboard secret"}}` + "\n")
	output := string(redactDiagnosticLog(input))
	for _, secret := range []string{"Alice", "alice@example.com", "LIC-1", "Personal Group", "clipboard secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("diagnostic log contains %q: %s", secret, output)
		}
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["ts"] != "2026-07-28T04:00:00Z" || entry["level"] != "info" || entry["event"] != "clipboard_saved" {
		t.Fatalf("unexpected base entry: %#v", entry)
	}
	if entry["kind"] != "text" || entry["format"] != "CF_UNICODETEXT" || entry["charLen"] != float64(42) || entry["byteLen"] != float64(42) || entry["sensitive"] != false {
		t.Fatalf("safe fields were not retained: %#v", entry)
	}
	if len(entry) != 8 {
		t.Fatalf("unexpected diagnostic fields: %#v", entry)
	}
}

func TestRedactDiagnosticLogRejectsTypedBypasses(t *testing.T) {
	input := []byte(`{"ts":"2026-07-28T04:00:00Z","level":"warn","event":"clipboard_saved","kind":{"value":"text","secret":"nested secret"},"format":["json"],"charLen":"42","byteLen":42.5,"sensitive":"false","status":"private status","error":"nested secret","extra":{"secret":"nested secret"}}`)
	output := string(redactDiagnosticLog(input))
	if strings.Contains(output, "nested secret") || strings.Contains(output, "private status") {
		t.Fatalf("diagnostic log leaked unsafe value: %s", output)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatal(err)
	}
	if len(entry) != 3 || entry["event"] != "clipboard_saved" {
		t.Fatalf("typed bypass fields survived sanitization: %#v", entry)
	}

	invalidEvent := redactDiagnosticLogLine([]byte(`{"ts":"2026-07-28T04:00:00Z","level":"info","event":"private event"}`))
	if string(invalidEvent) != redactedInvalidDiagnosticLogEntry {
		t.Fatalf("invalid event = %s", invalidEvent)
	}

	licenseFailure := redactDiagnosticLogLine([]byte(`{"ts":"2026-07-28T04:00:00Z","level":"warn","event":"license_activation_failed","error":"license_bad_signature"}`))
	if !strings.Contains(string(licenseFailure), `"error":"license_bad_signature"`) {
		t.Fatalf("fixed license error code was not retained: %s", licenseFailure)
	}
}

func TestWriteRedactedDiagnosticLogStopsOnOversizedLine(t *testing.T) {
	secret := strings.Repeat("x", maxDiagnosticLogLineBytes+1024)
	raw := `{"ts":"2026-07-28T04:00:00Z","level":"error","event":"import_failed","error":"` + secret + `"}`
	counting := &countingReader{r: strings.NewReader(raw)}
	var output bytes.Buffer
	if err := writeRedactedDiagnosticLog(&output, counting, newDiagnosticLogBudget(maxDiagnosticArchiveLogBytes)); err != nil {
		t.Fatal(err)
	}
	if output.String() != redactedOversizedDiagnosticLogEntry+"\n" {
		t.Fatalf("oversized line output = %q", output.String())
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("oversized diagnostic line leaked its contents")
	}
	if counting.n >= len(raw) {
		t.Fatalf("oversized line was read to EOF: %d", counting.n)
	}
}

func TestRedactDiagnosticLogDropsIncompleteJSON(t *testing.T) {
	input := []byte(`{"event":"group_updated","name":"Private Group","machineCode":"secret`)
	output := string(redactDiagnosticLog(input))
	if strings.Contains(output, "Private Group") || strings.Contains(output, "secret") {
		t.Fatalf("incomplete log entry leaked private data: %s", output)
	}
	if output != `{"event":"redacted_invalid_log_entry"}` {
		t.Fatalf("incomplete log entry = %s", output)
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestWriteDiagnosticArchive(t *testing.T) {
	logDir := t.TempDir()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	name := filepath.Join(logDir, now.Format(time.DateOnly)+".log")
	if err := os.WriteFile(name, []byte(`{"ts":"2026-07-28T12:00:00Z","level":"info","event":"email_backup_ok","to":"user@example.com","size":42}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	if err := WriteDiagnosticArchive(&archive, Environment{LogDir: logDir}, now); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("archive files = %d, want 2", len(reader.File))
	}
	logFile, err := reader.Open("logs/2026-07-28.log")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	data, err := io.ReadAll(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "user@example.com") {
		t.Fatalf("archive log was not redacted: %s", data)
	}
	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	if len(entry) != 4 || entry["event"] != "email_backup_ok" || entry["size"] != float64(42) {
		t.Fatalf("archive log did not retain the safe entry: %#v", entry)
	}

	metadataFile, err := reader.Open("diagnostics.json")
	if err != nil {
		t.Fatal(err)
	}
	defer metadataFile.Close()
	metadata, err := io.ReadAll(metadataFile)
	if err != nil {
		t.Fatal(err)
	}
	var metadataEntry map[string]any
	if err := json.Unmarshal(metadata, &metadataEntry); err != nil {
		t.Fatal(err)
	}
	if len(metadataEntry) != 4 || metadataEntry["generatedAt"] != now.Format(time.RFC3339) {
		t.Fatalf("unexpected archive metadata: %#v", metadataEntry)
	}
}

func TestWriteDiagnosticArchiveLimitsTotalRedactedLogBytes(t *testing.T) {
	logDir := t.TempDir()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	record := []byte(`{"ts":"2026-07-28T12:00:00Z","level":"info","event":"clipboard_saved","kind":"text"}` + "\n")
	redactedRecord := append(redactDiagnosticLogLine(record[:len(record)-1]), '\n')
	limit := 2*len(redactedRecord) + len(truncatedDiagnosticLogEntry) + 1

	if err := os.WriteFile(filepath.Join(logDir, now.Format(time.DateOnly)+".log"), bytes.Repeat(record, 2), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := "must-not-appear-in-diagnostic-archive"
	previousName := filepath.Join(logDir, now.AddDate(0, 0, -1).Format(time.DateOnly)+".log")
	if err := os.WriteFile(previousName, []byte(`{"ts":"2026-07-27T12:00:00Z","level":"info","event":"clipboard_saved","kind":"text","private":"`+secret+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	olderName := filepath.Join(logDir, now.AddDate(0, 0, -2).Format(time.DateOnly)+".log")
	if err := os.WriteFile(olderName, []byte(`{"ts":"2026-07-26T12:00:00Z","level":"info","event":"clipboard_saved","kind":"text","private":"`+secret+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	if err := writeDiagnosticArchive(&archive, Environment{LogDir: logDir}, now, limit); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 3 {
		t.Fatalf("archive files = %d, want 3", len(reader.File))
	}
	readLog := func(name string) []byte {
		t.Helper()
		file, err := reader.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(file)
		closeErr := file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		return data
	}

	today := readLog("logs/2026-07-28.log")
	previous := readLog("logs/2026-07-27.log")
	if got := len(today) + len(previous); got != limit {
		t.Fatalf("redacted log bytes = %d, want %d", got, limit)
	}
	if string(previous) != truncatedDiagnosticLogEntry+"\n" {
		t.Fatalf("truncation marker = %q", previous)
	}
	if strings.Contains(string(previous), secret) {
		t.Fatalf("truncated log leaked private content: %s", previous)
	}
	var marker map[string]any
	if err := json.Unmarshal(previous, &marker); err != nil {
		t.Fatalf("truncation marker is not JSONL: %v", err)
	}
	if marker["event"] != "diagnostic_log_archive_truncated" {
		t.Fatalf("unexpected truncation marker: %#v", marker)
	}
}

func TestHandleDiagnosticExportCleansTemporaryArchive(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMP", tempDir)
	t.Setenv("TEMP", tempDir)
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{env: Environment{LogDir: t.TempDir()}, logger: logger}
	result := httptest.NewRecorder()
	server.handleDiagnosticExport(result, httptest.NewRequest("GET", "/api/v1/diagnostics", nil))
	if result.Code != 200 {
		t.Fatalf("diagnostic export status = %d", result.Code)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary diagnostic archives were not removed: %v", entries)
	}
}
