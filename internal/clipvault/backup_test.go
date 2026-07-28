package clipvault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestBackupDataCanBeImported(t *testing.T) {
	ctx := context.Background()
	source, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	if err := source.Save(ctx, ClipboardItem{
		Kind:     "text",
		Format:   "CF_UNICODETEXT",
		Text:     "backup record",
		ByteLen:  13,
		CharLen:  13,
		SHA256:   "backup-record",
		MetaJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Sensitive.CustomPatterns = []string{"(?i)backup-secret"}
	if err := source.SaveConfig(config); err != nil {
		t.Fatal(err)
	}

	backup := &BackupService{store: source}
	for _, test := range []struct {
		name     string
		format   string
		password string
	}{
		{name: "JSON", format: "json"},
		{name: "XLSX", format: "xlsx"},
		{name: "Encrypted", format: "cvexport", password: "backup-password"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, extension, err := backup.exportBackupData(test.format, test.password)
			if err != nil {
				t.Fatal(err)
			}
			if extension != test.format {
				t.Fatalf("backup extension = %q, want %q", extension, test.format)
			}
			if test.format == "cvexport" {
				if !IsCVExport(data) {
					t.Fatal("encrypted backup is not a cvexport file")
				}
				data, err = DecryptExport(data, test.password)
				if err != nil {
					t.Fatal(err)
				}
			}

			target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if test.format == "xlsx" {
				if err := target.ImportXLSX(ctx, data); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := target.ImportFullJSON(ctx, data); err != nil {
					t.Fatal(err)
				}
			}

			items, total, err := target.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
			if err != nil {
				t.Fatal(err)
			}
			if total != 1 || items[0].Text != "backup record" {
				t.Fatalf("imported backup records = %#v, total = %d", items, total)
			}
			importedConfig, err := target.GetConfig()
			if err != nil {
				t.Fatal(err)
			}
			if !sameStrings(importedConfig.Sensitive.CustomPatterns, config.Sensitive.CustomPatterns) {
				t.Fatalf("imported sensitive rules = %#v", importedConfig.Sensitive.CustomPatterns)
			}
		})
	}
}

func TestBackupDataRejectsUnsupportedFormat(t *testing.T) {
	_, _, err := (&BackupService{}).exportBackupData("csv", "")
	if err == nil {
		t.Fatal("unsupported backup format should fail")
	}
}

func TestEmailBackupHistoryIsReadOnlyAndNewestFirst(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	first := EmailBackupHistory{
		SentAt: time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC),
		Mode:   emailBackupModeAutomatic,
		From:   "from@example.com",
		To:     "to@example.com",
		Format: "cvexport",
	}
	second := first
	second.SentAt = first.SentAt.Add(time.Nanosecond)
	second.Mode = emailBackupModeManual
	second.Format = "xlsx"
	if err := store.AddEmailBackupHistory(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEmailBackupHistory(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	history, err := store.ListEmailBackupHistory(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Mode != emailBackupModeManual || history[0].Format != "xlsx" {
		t.Fatalf("limited history = %#v", history)
	}

	server := &SearchServer{store: store}
	result := httptest.NewRecorder()
	server.handleEmailBackupHistory(result, httptest.NewRequest(http.MethodGet, "/api/backup/email/history", nil))
	if result.Code != http.StatusOK {
		t.Fatalf("history status = %d, body=%s", result.Code, result.Body.String())
	}
	var response struct {
		History []EmailBackupHistory `json:"history"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.History) != 2 || response.History[0].Mode != emailBackupModeManual || response.History[1].Mode != emailBackupModeAutomatic {
		t.Fatalf("history response = %#v", response.History)
	}
}

func TestRunStartupBackups(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Backup.DirBackup.Enabled = true
	cfg.Backup.DirBackup.RunOnStartup = true
	cfg.Backup.EmailBackup.Enabled = true
	cfg.Backup.EmailBackup.RunOnStartup = true
	var calls []string
	runStartupBackups(cfg,
		func(Config) error { calls = append(calls, "dir"); return errors.New("dir failed") },
		func(Config) error { calls = append(calls, "email"); return nil },
	)
	if got := strings.Join(calls, ","); got != "dir,email" {
		t.Fatalf("startup backup calls = %q, want dir,email", got)
	}

	cfg.Backup.DirBackup.RunOnStartup = false
	cfg.Backup.EmailBackup.Enabled = false
	calls = nil
	runStartupBackups(cfg,
		func(Config) error { calls = append(calls, "dir"); return nil },
		func(Config) error { calls = append(calls, "email"); return nil },
	)
	if len(calls) != 0 {
		t.Fatalf("disabled startup backup calls = %v", calls)
	}
}

func TestRenderEmailTemplateEscapesContent(t *testing.T) {
	cfg := DefaultConfig().Backup.EmailBackup
	cfg.Subject = "备份 {time}"
	cfg.Body = "<script>alert('x')</script>\n{filename} {format}"
	now := time.Date(2026, time.July, 28, 12, 34, 56, 0, time.Local)
	subject, body, htmlBody := renderEmailTemplate(cfg, "backup.cvexport", "cvexport", now)
	if subject != "备份 2026-07-28 12:34:56" {
		t.Fatalf("subject = %q", subject)
	}
	if body != "<script>alert('x')</script>\nbackup.cvexport cvexport" {
		t.Fatalf("body = %q", body)
	}
	if strings.Contains(htmlBody, "<script>") || !strings.Contains(htmlBody, "&lt;script&gt;") {
		t.Fatalf("HTML body did not escape content: %s", htmlBody)
	}
	for _, style := range []string{"plain", "card", "accent"} {
		cfg.Style = style
		_, _, rendered := renderEmailTemplate(cfg, "backup.cvexport", "cvexport", now)
		if rendered == "" {
			t.Fatalf("style %q did not render", style)
		}
	}
}

func TestBuildMIMEWithAttachmentPreservesBinaryContent(t *testing.T) {
	attachment := []byte{0x50, 0x4B, 0x03, 0x04, 0x00, 0xFF, 0x80, 0x7F, 0xC0}
	msg := buildMIMEWithAttachment("a@x.com", "b@y.com", "subj", "body", "<p>body</p>", "f.xlsx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", attachment)
	message, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("outer content type = %q, %v", mediaType, err)
	}
	mixed := multipart.NewReader(message.Body, params["boundary"])
	alternativePart, err := mixed.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err = mime.ParseMediaType(alternativePart.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("alternative content type = %q, %v", mediaType, err)
	}
	alternative := multipart.NewReader(alternativePart, params["boundary"])
	plainPart, err := alternative.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(plainPart)
	if err != nil || string(plain) != "body" {
		t.Fatalf("plain body = %q, err=%v", plain, err)
	}
	htmlPart, err := alternative.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	htmlBody, err := io.ReadAll(htmlPart)
	if err != nil || string(htmlBody) != "<p>body</p>" {
		t.Fatalf("HTML body = %q, err=%v", htmlBody, err)
	}

	attachmentPart, err := mixed.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	_, disposition, err := mime.ParseMediaType(attachmentPart.Header.Get("Content-Disposition"))
	if err != nil || disposition["filename"] != "f.xlsx" {
		t.Fatalf("attachment disposition = %#v, err=%v", disposition, err)
	}
	encoded, err := io.ReadAll(attachmentPart)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(encoded), "\r\n", ""))
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if !bytes.Equal(decoded, attachment) {
		t.Fatalf("attachment mismatch: got %v, want %v", decoded, attachment)
	}
}
