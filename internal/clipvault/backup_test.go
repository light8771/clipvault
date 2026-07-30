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
	"os"
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
			data, extension, err := backup.exportBackupData(test.format, test.password, ExportScope{Mode: "full"})
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
	_, _, err := (&BackupService{}).exportBackupData("csv", "", ExportScope{})
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

func TestSMTPDeliveryValidationRequiresEmailUsername(t *testing.T) {
	cfg := EmailBackupConfig{
		SMTPHost: "smtp.example.com",
		SMTPPort: 587,
		SMTPUser: "clipvalue",
		SMTPPass: "smtp-password",
		From:     "from@example.com",
		To:       "to@example.com",
	}
	problems := smtpDeliveryErrors(cfg)
	if !strings.Contains(strings.Join(problems, "、"), "SMTP 用户名必须为邮箱") {
		t.Fatalf("SMTP username validation = %v", problems)
	}
	if err := sendSMTPMessage(cfg, nil); err == nil || !strings.Contains(err.Error(), "SMTP 用户名必须为邮箱") {
		t.Fatalf("send validation error = %v", err)
	}

	cfg.SMTPUser = ""
	cfg.SMTPPass = ""
	if problems := smtpDeliveryErrors(cfg); len(problems) != 0 {
		t.Fatalf("unauthenticated relay validation = %v", problems)
	}
}

func TestBackupReloadIgnoresUnrelatedConfigChanges(t *testing.T) {
	cfg := completeScheduledBackupConfig()
	backup := &BackupService{}
	backup.Reload(cfg)

	backup.mu.Lock()
	dirGeneration := backup.dirGeneration
	emailGeneration := backup.emailGeneration
	dirTimer := backup.dirTimer
	emailTimer := backup.emailTimer
	backup.mu.Unlock()

	changed := cfg
	changed.Brand.Title = "ClipVault Pro"
	backup.Reload(changed)

	backup.mu.Lock()
	defer backup.mu.Unlock()
	if backup.dirGeneration != dirGeneration || backup.emailGeneration != emailGeneration {
		t.Fatalf("timer generations changed for unrelated config save: dir=%d->%d email=%d->%d", dirGeneration, backup.dirGeneration, emailGeneration, backup.emailGeneration)
	}
	if backup.dirTimer != dirTimer || backup.emailTimer != emailTimer {
		t.Fatalf("timers were reset by unrelated config save: dir=%p/%p email=%p/%p", dirTimer, backup.dirTimer, emailTimer, backup.emailTimer)
	}
}

func TestBackupReloadReschedulesOnlyChangedTarget(t *testing.T) {
	cfg := completeScheduledBackupConfig()
	backup := &BackupService{}
	backup.Reload(cfg)

	backup.mu.Lock()
	dirGeneration := backup.dirGeneration
	emailGeneration := backup.emailGeneration
	dirTimer := backup.dirTimer
	emailTimer := backup.emailTimer
	backup.mu.Unlock()

	changed := cfg
	changed.Backup.DirBackup.Interval = 2
	backup.Reload(changed)

	backup.mu.Lock()
	defer backup.mu.Unlock()
	if backup.dirGeneration == dirGeneration {
		t.Fatalf("dir timer generation did not change: %d", backup.dirGeneration)
	}
	if backup.emailGeneration != emailGeneration {
		t.Fatalf("email timer generation changed unexpectedly: %d -> %d", emailGeneration, backup.emailGeneration)
	}
	if backup.dirTimer == nil || backup.emailTimer == nil {
		t.Fatalf("timers not scheduled after reload: dir=%v email=%v", backup.dirTimer, backup.emailTimer)
	}
	if backup.emailTimer != emailTimer {
		t.Fatalf("email timer was reset unexpectedly: %p -> %p", emailTimer, backup.emailTimer)
	}
	if backup.dirTimer == dirTimer {
		t.Fatalf("dir timer was not replaced: %p", dirTimer)
	}
}

func TestBackupReloadPreventsStaleTimerRearm(t *testing.T) {
	cfg := completeScheduledBackupConfig()
	backup := &BackupService{}
	backup.Reload(cfg)

	backup.mu.Lock()
	dirGeneration := backup.dirGeneration
	emailGeneration := backup.emailGeneration
	dirTimer := backup.dirTimer
	emailTimer := backup.emailTimer
	backup.mu.Unlock()
	if dirTimer == nil || emailTimer == nil {
		t.Fatalf("timers were not scheduled: dir=%v email=%v", dirTimer, emailTimer)
	}

	disabled := cfg
	disabled.Backup.DirBackup.Enabled = false
	disabled.Backup.EmailBackup.Enabled = false
	backup.Reload(disabled)

	// A callback may already be running while Reload stops its timer. It must not
	// re-arm the old schedule after the new configuration has taken effect.
	backup.rearmDirTimer(cfg, dirGeneration)
	backup.rearmEmailTimer(cfg, emailGeneration)
	backup.dirBackupTick(dirGeneration)
	backup.emailBackupTick(emailGeneration)

	backup.mu.Lock()
	defer backup.mu.Unlock()
	if backup.dirGeneration == dirGeneration || backup.emailGeneration == emailGeneration {
		t.Fatalf("timer generations were not advanced: dir=%d email=%d", backup.dirGeneration, backup.emailGeneration)
	}
	if backup.dirTimer != nil || backup.emailTimer != nil {
		t.Fatalf("stale callbacks re-armed timers: dir=%v email=%v", backup.dirTimer, backup.emailTimer)
	}
}

func TestWriteBackupFileUsesUniqueNames(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(0, 0)
	first, err := writeBackupFile(dir, now, "json", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeBackupFile(dir, now, "json", []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("backup file names were reused")
	}
	if !strings.HasPrefix(first, dir) || !strings.HasPrefix(second, dir) {
		t.Fatalf("backup files were not written inside the target directory: %q %q", first, second)
	}
	if data, err := os.ReadFile(first); err != nil || string(data) != "first" {
		t.Fatalf("first backup file = %q, err=%v", data, err)
	}
	if data, err := os.ReadFile(second); err != nil || string(data) != "second" {
		t.Fatalf("second backup file = %q, err=%v", data, err)
	}
}

func completeScheduledBackupConfig() Config {
	cfg := DefaultConfig()
	cfg.Backup.DirBackup = DirBackupConfig{
		Enabled:  true,
		Interval: 1,
		Dir:      `D:\backups`,
		Format:   "json",
	}
	cfg.Backup.EmailBackup = EmailBackupConfig{
		Enabled:     true,
		Interval:    1,
		SMTPHost:    "smtp.example.com",
		SMTPPort:    587,
		SMTPUser:    "user@example.com",
		SMTPPass:    "smtp-password",
		From:        "from@example.com",
		To:          "to@example.com",
		Format:      "json",
		Subject:     "ClipVault 备份 - {time}",
		Body:        "已生成 {format} 备份文件：{filename}",
		Style:       "card",
		AccentColor: "#16A34A",
	}
	return cfg
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

// TestExportEmailPartsEmptyStoreProducesOneImportablePart 验证无新数据时邮件备份
// 仍产出一片可独立导入的空导出（与目录备份"总是写文件"的行为一致），
// 且水印不被推进。
func TestExportEmailPartsEmptyStoreProducesOneImportablePart(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveConfig(DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	backup := &BackupService{store: store}

	for _, test := range []struct {
		name     string
		format   string
		password string
	}{
		{name: "JSON", format: "json"},
		{name: "Encrypted", format: "cvexport", password: "empty-part-password"},
	} {
		t.Run(test.name, func(t *testing.T) {
			parts, ext, err := backup.exportEmailParts(test.format, test.password, ExportScope{Mode: "incremental", SinceID: 0})
			if err != nil {
				t.Fatal(err)
			}
			if len(parts) != 1 {
				t.Fatalf("empty store should still produce 1 part, got %d", len(parts))
			}
			raw := parts[0]
			if test.format == "cvexport" {
				if ext != "cvexport" {
					t.Fatalf("ext = %q", ext)
				}
				raw, err = DecryptExport(raw, test.password)
				if err != nil {
					t.Fatalf("decrypt empty part: %v", err)
				}
			}
			var data ExportData
			if err := json.Unmarshal(raw, &data); err != nil {
				t.Fatalf("empty part is not valid export JSON: %v", err)
			}
			if len(data.Items) != 0 {
				t.Fatalf("empty part should have no items, got %d", len(data.Items))
			}
			dst, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close()
			if err := dst.ImportFullJSON(ctx, raw); err != nil {
				t.Fatalf("empty part should be importable: %v", err)
			}

			// 空数据备份成功后水印不应推进（与目录备份一致）
			wm := updateBackupWatermark(store, "email", ExportScope{Mode: "incremental", SinceID: 0})
			if wm.LastItemID != 0 {
				t.Fatalf("watermark should not advance on empty backup, got %#v", wm)
			}
			t.Logf("[OK] empty store produced 1 importable %s part (%d bytes)", ext, len(parts[0]))
		})
	}
}
