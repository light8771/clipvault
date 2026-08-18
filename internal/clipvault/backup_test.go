package clipvault

import (
	"archive/zip"
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
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmailPartCandidateUpperBoundUsesAllRemainingRows(t *testing.T) {
	total := maxEmailParts*5000 + 1
	if got := emailPartCandidateUpperBound(total, 0); got != total {
		t.Fatalf("candidate upper bound = %d, want %d", got, total)
	}
	if got := emailPartCandidateUpperBound(total, 1234); got != total-1234 {
		t.Fatalf("remaining candidate upper bound = %d, want %d", got, total-1234)
	}
}

func TestNextEmailPartProbeGrowsBeyondFormerRowCap(t *testing.T) {
	remaining := maxEmailParts*initialEmailPartProbeRows + 1
	probe := initialEmailPartProbeRows
	for probe < remaining {
		next := nextEmailPartProbe(probe, remaining)
		if next <= probe || next > remaining {
			t.Fatalf("next probe = %d after %d with %d remaining", next, probe, remaining)
		}
		probe = next
	}
	if probe != remaining {
		t.Fatalf("final probe = %d, want %d", probe, remaining)
	}
}

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
		{name: "JSON", format: "json", password: "backup-password"},
		{name: "XLSX", format: "xlsx", password: "backup-password"},
		{name: "Encrypted", format: "cvexport", password: "backup-password"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, extension, exportedMaxID, err := backup.exportBackupData(test.format, test.password, ExportScope{Mode: "full"})
			if err != nil {
				t.Fatal(err)
			}
			// 所有格式的备份文件都是 .cvexport 加密容器
			if extension != "cvexport" {
				t.Fatalf("backup extension = %q, want %q", extension, "cvexport")
			}
			_ = exportedMaxID
			if !IsCVExport(data) {
				t.Fatal("backup is not a cvexport file")
			}
			data, err = DecryptExport(data, test.password)
			if err != nil {
				t.Fatal(err)
			}
			// json/xlsx 格式的明文载荷会先打进 zip 再加密，导入前需解包
			if test.format != "cvexport" {
				entry := "clipvault-export.json"
				if test.format == "xlsx" {
					entry = "clipvault-export.xlsx"
				}
				data = unzipSingleEntry(t, data, entry)
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
	_, _, _, err := (&BackupService{}).exportBackupData("csv", "", ExportScope{})
	if err == nil {
		t.Fatal("unsupported backup format should fail")
	}
}

func TestBackupHistoryIsReadOnlyNewestFirstAndPerKind(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	first := BackupHistory{
		CreatedAt: time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC),
		Kind:      "email",
		Trigger:   backupTriggerScheduled,
		Mode:      "full",
		OK:        true,
		Target:    "to@example.com",
		Format:    "cvexport",
		Parts:     2,
		Items:     100,
	}
	second := first
	second.CreatedAt = first.CreatedAt.Add(time.Nanosecond)
	second.Trigger = backupTriggerManual
	second.Mode = "incremental"
	second.Format = "xlsx"
	if err := store.AddBackupHistory(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := store.AddBackupHistory(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	// 目录通道独立留痕，失败也记录
	if err := store.AddBackupHistory(context.Background(), BackupHistory{
		Kind: "dir", Trigger: backupTriggerScheduled, Mode: "full",
		OK: false, Error: "磁盘已满", Target: `D:\backups`, Format: "cvexport",
	}); err != nil {
		t.Fatal(err)
	}

	history, err := store.ListBackupHistory(context.Background(), "email", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Trigger != backupTriggerManual || history[0].Format != "xlsx" {
		t.Fatalf("limited history = %#v", history)
	}

	dirHistory, err := store.ListBackupHistory(context.Background(), "dir", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirHistory) != 1 || dirHistory[0].OK || dirHistory[0].Error != "磁盘已满" {
		t.Fatalf("dir history = %#v", dirHistory)
	}

	server := &SearchServer{store: store}
	result := httptest.NewRecorder()
	server.handleBackupHistory(result, httptest.NewRequest(http.MethodGet, "/api/v1/backup/history?kind=email", nil))
	if result.Code != http.StatusOK {
		t.Fatalf("history status = %d, body=%s", result.Code, result.Body.String())
	}
	var response struct {
		History []BackupHistory `json:"history"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.History) != 2 || response.History[0].Trigger != backupTriggerManual || response.History[1].Trigger != backupTriggerScheduled {
		t.Fatalf("history response = %#v", response.History)
	}

	// 非法 kind 返回 400
	bad := httptest.NewRecorder()
	server.handleBackupHistory(bad, httptest.NewRequest(http.MethodGet, "/api/v1/backup/history?kind=bogus", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid kind status = %d", bad.Code)
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
		Password: "scheduled-backup-password",
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
		Password:    "scheduled-backup-password",
		Subject:     "ClipVault 备份 - {time}",
		Body:        "已生成 {format} 备份文件：{filename}",
		Style:       "card",
		AccentColor: "#16A34A",
		MaxPartSize: 20 << 20,
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

func TestEmailBackupMessageUsesPartFilenameAndCountsRenderedBody(t *testing.T) {
	cfg := completeScheduledBackupConfig().Backup.EmailBackup
	cfg.Subject = "Backup {filename}"
	cfg.Body = "Attached: {filename}"
	now := time.Date(2026, time.August, 7, 12, 34, 56, 0, time.UTC)
	message := buildEmailBackupMessage(cfg, "json", now, 1, 3, []byte("payload"))
	const filename = "clipvault-backup-20260807-123456-2of3.json"
	if message.filename != filename || !bytes.Contains(message.data, []byte(filename)) {
		t.Fatalf("part filename was not used consistently: filename=%q", message.filename)
	}

	shortSize := emailBackupMessageSize(cfg, "json", []byte("payload"))
	cfg.Body += strings.Repeat(" rendered-body", 1000)
	longSize := emailBackupMessageSize(cfg, "json", []byte("payload"))
	if longSize <= shortSize {
		t.Fatalf("rendered body was not included in MIME size: short=%d long=%d", shortSize, longSize)
	}
}

func TestStreamingEmailAttachmentSizeMatchesWrittenMessage(t *testing.T) {
	cfg := completeScheduledBackupConfig().Backup.EmailBackup
	attachmentPath := filepath.Join(t.TempDir(), "part.json")
	if err := os.WriteFile(attachmentPath, bytes.Repeat([]byte("streamed attachment\n"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	var message bytes.Buffer
	if err := writeEmailBackupMessage(&message, cfg, "json", now, 0, 1, attachmentPath); err != nil {
		t.Fatal(err)
	}
	size, err := emailBackupFileMessageSize(cfg, "json", now, 0, 1, attachmentPath)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(message.Len()) {
		t.Fatalf("streaming MIME size=%d, written=%d", size, message.Len())
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
		{name: "JSONZipEncrypted", format: "json", password: "empty-part-password"},
		{name: "Encrypted", format: "cvexport", password: "empty-part-password"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig().Backup.EmailBackup
			cfg.Format = test.format
			cfg.Password = test.password
			cfg.MaxPartSize = 64 << 20
			parts, ext, exportedMaxID, err := backup.exportEmailPartFilesSized(cfg, ExportScope{Mode: "full"})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanupEmailPartFiles(parts)
			if len(parts) != 1 {
				t.Fatalf("empty store should still produce 1 part, got %d", len(parts))
			}
			if exportedMaxID != 0 {
				t.Fatalf("empty store should report exportedMaxID=0, got %d", exportedMaxID)
			}
			if ext != "cvexport" {
				t.Fatalf("email attachment ext = %q, want cvexport", ext)
			}
			raw, err := os.ReadFile(parts[0].path)
			if err != nil {
				t.Fatal(err)
			}
			// 邮件备份永远是加密容器：解密后 cvexport 直接是 JSON；
			// json/xlsx 格式的明文文件在 zip 内。
			raw, err = DecryptExport(raw, test.password)
			if err != nil {
				t.Fatalf("decrypt empty part: %v", err)
			}
			if test.format == "json" {
				raw = unzipSingleEntry(t, raw, "clipvault-export.json")
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
			wm, err := updateBackupWatermark(store, "email", ExportScope{Mode: "incremental", SinceID: 0}, exportedMaxID)
			if err != nil {
				t.Fatalf("updateBackupWatermark: %v", err)
			}
			if wm.LastItemID != 0 {
				t.Fatalf("watermark should not advance on empty backup, got %#v", wm)
			}
			t.Logf("[OK] empty store produced 1 importable %s part (%d bytes)", ext, len(raw))
		})
	}
}

// TestEmailBackupZipPayloadRoundtrip 验证邮件备份的强制加密语义：
// 任何格式都产出 .cvexport 容器；json/xlsx 的明文文件在容器内的 zip 中；
// 解密解包后可完整导入。
func TestEmailBackupZipPayloadRoundtrip(t *testing.T) {
	ctx := context.Background()
	src, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.Save(ctx, ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "zip-payload-record"}); err != nil {
		t.Fatal(err)
	}
	backup := &BackupService{store: src}

	// 未设置密码时任何格式都拒绝导出
	for _, format := range []string{"json", "xlsx", "cvexport"} {
		if _, err := backup.exportEmailBackupTo(io.Discard, format, "", ExportScope{Mode: "full"}); err == nil {
			t.Fatalf("email backup without password was accepted for %s", format)
		}
	}

	for _, format := range []string{"json", "xlsx", "cvexport"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			maxID, err := backup.exportEmailBackupTo(&buf, format, "zip-roundtrip-pw", ExportScope{Mode: "full"})
			if err != nil {
				t.Fatal(err)
			}
			if maxID == 0 {
				t.Fatal("expected exported max id > 0")
			}
			if !IsCVExport(buf.Bytes()) {
				t.Fatal("email backup is not an encrypted container")
			}
			plain, err := DecryptExport(buf.Bytes(), "zip-roundtrip-pw")
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if format == "cvexport" {
				if bytes.HasPrefix(plain, []byte("PK")) {
					t.Fatal("cvexport payload should be plain JSON, not zip")
				}
			} else {
				entry := "clipvault-export.json"
				if format == "xlsx" {
					entry = "clipvault-export.xlsx"
				}
				payload := unzipSingleEntry(t, plain, entry)
				if len(payload) == 0 {
					t.Fatal("empty zipped payload")
				}
			}
			// 通过导入端点同款路径还原（自动识别 zip 载荷）
			tmp, err := os.CreateTemp(t.TempDir(), "payload-*")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tmp.Write(plain); err != nil {
				t.Fatal(err)
			}
			if _, err := tmp.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			defer tmp.Close()
			dst, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close()
			server := &SearchServer{store: dst}
			if err := server.importDecryptedExport(ctx, tmp); err != nil {
				t.Fatalf("importDecryptedExport: %v", err)
			}
			_, total, err := dst.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
			if err != nil || total != 1 {
				t.Fatalf("restored items=%d err=%v", total, err)
			}
		})
	}
}

// unzipSingleEntry 从内存 zip 中读取指定条目（邮件备份的明文载荷打包格式）。
func unzipSingleEntry(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open payload zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", name, err)
		}
		defer rc.Close()
		content, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read zip entry %s: %v", name, err)
		}
		return content
	}
	t.Fatalf("zip entry %s not found", name)
	return nil
}
