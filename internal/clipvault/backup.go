package clipvault

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"clipboard_ext/internal/licensing"
)

var errBackupLicenseRequired = errors.New("需要先激活 ClipVault 才能使用备份功能")

const (
	emailBackupModeAutomatic = "automatic"
	emailBackupModeManual    = "manual"
)

// BackupStatus 记录最近一次备份的状态。
type BackupStatus struct {
	LastTime   time.Time
	LastOK     bool
	LastError  string
	BackupFile string
}

// BackupService 管理定时目录备份和邮件备份。
type BackupService struct {
	cfgMgr  *ConfigManager
	store   *Store
	logger  *DailyLogger
	license *licensing.Manager

	mu          sync.Mutex
	dirTimer    *time.Timer
	emailTimer  *time.Timer
	startupOnce sync.Once

	statusMu    sync.RWMutex
	dirStatus   BackupStatus
	emailStatus BackupStatus
}

// NewBackupService 创建备份服务，并注册配置变更监听。
func NewBackupService(cfgMgr *ConfigManager, store *Store, logger *DailyLogger, license *licensing.Manager) *BackupService {
	bs := &BackupService{
		cfgMgr:  cfgMgr,
		store:   store,
		logger:  logger,
		license: license,
	}
	cfgMgr.OnChange(func(cfg Config) error {
		bs.Reload(cfg)
		return nil
	})
	return bs
}

// Start 根据当前配置启动定时器。
func (bs *BackupService) Start() {
	cfg := bs.cfgMgr.Get()
	bs.Reload(cfg)
	bs.startupOnce.Do(func() {
		go bs.runStartupBackups(cfg)
	})
}

func (bs *BackupService) runStartupBackups(cfg Config) {
	runStartupBackups(cfg,
		func(c Config) error {
			err := bs.doDirBackup(c)
			if err != nil {
				bs.logger.Error("dir_backup_startup_failed", err, nil)
			}
			return err
		},
		func(c Config) error {
			err := bs.doEmailBackup(c, emailBackupModeAutomatic)
			if err != nil {
				bs.logger.Error("email_backup_startup_failed", err, nil)
			}
			return err
		},
	)
}

func runStartupBackups(cfg Config, runDir, runEmail func(Config) error) {
	if cfg.Backup.DirBackup.Enabled && cfg.Backup.DirBackup.RunOnStartup {
		_ = runDir(cfg)
	}
	if cfg.Backup.EmailBackup.Enabled && cfg.Backup.EmailBackup.RunOnStartup {
		_ = runEmail(cfg)
	}
}

// Reload 根据新配置重置定时器。
func (bs *BackupService) Reload(cfg Config) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	if bs.dirTimer != nil {
		bs.dirTimer.Stop()
		bs.dirTimer = nil
	}
	if bs.emailTimer != nil {
		bs.emailTimer.Stop()
		bs.emailTimer = nil
	}

	licensed := bs.license == nil || bs.license.CanUsePaidFeatures()
	if licensed && cfg.Backup.DirBackup.Enabled && cfg.Backup.DirBackup.Interval > 0 && cfg.Backup.DirBackup.Dir != "" {
		interval := time.Duration(cfg.Backup.DirBackup.Interval) * time.Hour
		bs.dirTimer = time.AfterFunc(interval, bs.dirBackupTick)
	}

	if licensed && cfg.Backup.EmailBackup.Enabled && cfg.Backup.EmailBackup.Interval > 0 &&
		cfg.Backup.EmailBackup.SMTPHost != "" && cfg.Backup.EmailBackup.To != "" {
		interval := time.Duration(cfg.Backup.EmailBackup.Interval) * time.Hour
		bs.emailTimer = time.AfterFunc(interval, bs.emailBackupTick)
	}
}

// DirStatus 返回目录备份的最近状态。
func (bs *BackupService) DirStatus() BackupStatus {
	bs.statusMu.RLock()
	defer bs.statusMu.RUnlock()
	return bs.dirStatus
}

// EmailStatus 返回邮件备份的最近状态。
func (bs *BackupService) EmailStatus() BackupStatus {
	bs.statusMu.RLock()
	defer bs.statusMu.RUnlock()
	return bs.emailStatus
}

func (bs *BackupService) setDirStatus(s BackupStatus) {
	bs.statusMu.Lock()
	bs.dirStatus = s
	bs.statusMu.Unlock()
}

func (bs *BackupService) setEmailStatus(s BackupStatus) {
	bs.statusMu.Lock()
	bs.emailStatus = s
	bs.statusMu.Unlock()
}

// RunDirBackup 立即执行一次目录备份（由 API 端点调用）。
func (bs *BackupService) RunDirBackup() error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	cfg := bs.cfgMgr.Get()
	return bs.doDirBackup(cfg)
}

// RunEmailBackup 立即执行一次邮件备份（由 API 端点调用）。
func (bs *BackupService) RunEmailBackup() error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	cfg := bs.cfgMgr.Get()
	return bs.doEmailBackup(cfg, emailBackupModeManual)
}

// SendTestEmail 发送一封测试邮件（不包含备份数据）。
func (bs *BackupService) SendTestEmail() error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	cfg := bs.cfgMgr.Get()
	eb := cfg.Backup.EmailBackup
	if eb.SMTPHost == "" || eb.From == "" || eb.To == "" || eb.SMTPPort < 1 {
		return fmt.Errorf("SMTP 配置不完整")
	}
	if errors := emailTemplateErrors(eb); len(errors) > 0 {
		return fmt.Errorf("邮件模板配置不完整: %s", strings.Join(errors, "、"))
	}
	subject, body, htmlBody := renderEmailTemplate(eb, "测试邮件（无附件）", "测试", time.Now())
	msg := buildTemplateMIME(eb.From, eb.To, subject, body, htmlBody)

	return sendSMTPMessage(eb, msg)
}

func (bs *BackupService) dirBackupTick() {
	cfg := bs.cfgMgr.Get()
	if !cfg.Backup.DirBackup.Enabled {
		return
	}
	if err := bs.doDirBackup(cfg); err != nil {
		bs.logger.Error("dir_backup_failed", err, nil)
	}
	bs.mu.Lock()
	if cfg.Backup.DirBackup.Interval > 0 {
		interval := time.Duration(cfg.Backup.DirBackup.Interval) * time.Hour
		bs.dirTimer = time.AfterFunc(interval, bs.dirBackupTick)
	}
	bs.mu.Unlock()
}

func (bs *BackupService) emailBackupTick() {
	cfg := bs.cfgMgr.Get()
	if !cfg.Backup.EmailBackup.Enabled {
		return
	}
	if err := bs.doEmailBackup(cfg, emailBackupModeAutomatic); err != nil {
		bs.logger.Error("email_backup_failed", err, nil)
	}
	bs.mu.Lock()
	if cfg.Backup.EmailBackup.Interval > 0 {
		interval := time.Duration(cfg.Backup.EmailBackup.Interval) * time.Hour
		bs.emailTimer = time.AfterFunc(interval, bs.emailBackupTick)
	}
	bs.mu.Unlock()
}

func (bs *BackupService) doDirBackup(cfg Config) error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	db := cfg.Backup.DirBackup
	data, ext, err := bs.exportBackupData(db.Format, db.Password)
	if err != nil {
		bs.setDirStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}

	if err := os.MkdirAll(db.Dir, 0755); err != nil {
		err = fmt.Errorf("创建目录失败: %w", err)
		bs.setDirStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}

	filename := fmt.Sprintf("clipvault-backup-%s.%s", time.Now().Format("20060102-150405"), ext)
	fullPath := filepath.Join(db.Dir, filename)

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		err = fmt.Errorf("写入文件失败: %w", err)
		bs.setDirStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}

	bs.setDirStatus(BackupStatus{LastTime: time.Now(), LastOK: true, BackupFile: fullPath})
	bs.logger.Info("dir_backup_ok", map[string]any{"file": fullPath, "size": len(data)})
	return nil
}

func (bs *BackupService) doEmailBackup(cfg Config, mode string) error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	eb := cfg.Backup.EmailBackup
	if errors := emailTemplateErrors(eb); len(errors) > 0 {
		err := fmt.Errorf("邮件模板配置不完整: %s", strings.Join(errors, "、"))
		bs.setEmailStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}
	data, ext, err := bs.exportBackupData(eb.Format, eb.Password)
	if err != nil {
		bs.setEmailStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}

	now := time.Now()
	filename := fmt.Sprintf("clipvault-backup-%s.%s", now.Format("20060102-150405"), ext)
	subject, body, htmlBody := renderEmailTemplate(eb, filename, ext, now)

	contentType := "application/json"
	switch ext {
	case "cvexport":
		contentType = "application/octet-stream"
	case "xlsx":
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}

	msg := buildMIMEWithAttachment(eb.From, eb.To, subject, body, htmlBody, filename, contentType, data)

	if err := sendSMTPMessage(eb, msg); err != nil {
		err = fmt.Errorf("发送邮件失败: %w", err)
		bs.setEmailStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}
	if err := bs.store.AddEmailBackupHistory(context.Background(), EmailBackupHistory{
		SentAt: now,
		Mode:   mode,
		From:   eb.From,
		To:     eb.To,
		Format: ext,
	}); err != nil {
		bs.logger.Error("email_backup_history_record_failed", err, nil)
	}

	bs.setEmailStatus(BackupStatus{LastTime: time.Now(), LastOK: true, BackupFile: filename})
	bs.logger.Info("email_backup_ok", map[string]any{"to": eb.To, "size": len(data)})
	return nil
}

func sendSMTPMessage(cfg EmailBackupConfig, msg []byte) error {
	host := strings.TrimSpace(cfg.SMTPHost)
	if host == "" || cfg.SMTPPort < 1 || cfg.SMTPPort > 65535 {
		return fmt.Errorf("SMTP 服务器或端口无效")
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil || from.Address == "" {
		return fmt.Errorf("发件人邮箱无效")
	}
	to, err := mail.ParseAddress(cfg.To)
	if err != nil || to.Address == "" {
		return fmt.Errorf("收件人邮箱无效")
	}

	addr := net.JoinHostPort(host, strconv.Itoa(cfg.SMTPPort))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}

	var client *smtp.Client
	if cfg.SMTPPort == 465 {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("SMTP TLS 连接失败: %w", err)
		}
		client, err = smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("SMTP 握手失败: %w", err)
		}
	} else {
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return fmt.Errorf("SMTP 连接失败: %w", err)
		}
		client, err = smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("SMTP 握手失败: %w", err)
		}
		if ok, _ := client.Extension("STARTTLS"); !ok {
			client.Close()
			return fmt.Errorf("SMTP 服务器不支持 STARTTLS，已拒绝明文发送")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			client.Close()
			return fmt.Errorf("SMTP STARTTLS 失败: %w", err)
		}
	}
	defer client.Close()

	if cfg.SMTPUser != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("SMTP 服务器不支持身份认证")
		}
		if err := client.Auth(smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, host)); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("SMTP 发件人被拒绝: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("SMTP 收件人被拒绝: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA 失败: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		w.Close()
		return fmt.Errorf("SMTP 邮件写入失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("SMTP 邮件提交失败: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("SMTP 结束会话失败: %w", err)
	}
	return nil
}

// exportBackupData 导出数据并根据格式返回字节和扩展名。
func (bs *BackupService) exportBackupData(format, password string) ([]byte, string, error) {
	switch format {
	case "xlsx":
		data, err := bs.store.ExportXLSX(context.Background())
		if err != nil {
			return nil, "", fmt.Errorf("Excel 导出失败: %w", err)
		}
		return data, "xlsx", nil
	case "cvexport":
		if password == "" {
			return nil, "", fmt.Errorf("cvexport 格式需要加密密码")
		}
		export, err := bs.store.ExportFullForJSON(context.Background())
		if err != nil {
			return nil, "", fmt.Errorf("导出数据失败: %w", err)
		}
		encData, err := EncryptExportJSON(export, password)
		if err != nil {
			return nil, "", fmt.Errorf("加密失败: %w", err)
		}
		return encData, "cvexport", nil
	case "json":
		export, err := bs.store.ExportFullForJSON(context.Background())
		if err != nil {
			return nil, "", fmt.Errorf("导出数据失败: %w", err)
		}
		jsonData, err := json.MarshalIndent(export, "", "  ")
		if err != nil {
			return nil, "", fmt.Errorf("JSON 编码失败: %w", err)
		}
		return jsonData, "json", nil
	default:
		return nil, "", fmt.Errorf("不支持的备份格式: %s", format)
	}
}

func renderEmailTemplate(cfg EmailBackupConfig, filename, format string, now time.Time) (string, string, string) {
	replacer := strings.NewReplacer(
		"{time}", now.Format("2006-01-02 15:04:05"),
		"{filename}", filename,
		"{format}", format,
	)
	subject := replacer.Replace(cfg.Subject)
	body := replacer.Replace(cfg.Body)
	return subject, body, renderEmailHTML(subject, body, cfg.Style, cfg.AccentColor)
}

func renderEmailHTML(subject, body, style, accentColor string) string {
	escapedSubject := html.EscapeString(subject)
	escapedBody := strings.ReplaceAll(html.EscapeString(normalizeEmailNewlines(body)), "\n", "<br>\n")
	switch style {
	case "plain":
		return fmt.Sprintf("<!doctype html><html><body style=\"margin:0;padding:24px;color:#1f2937;font:15px/1.7 Arial,sans-serif\"><strong style=\"font-size:18px\">%s</strong><div style=\"margin-top:16px\">%s</div></body></html>", escapedSubject, escapedBody)
	case "accent":
		return fmt.Sprintf("<!doctype html><html><body style=\"margin:0;padding:24px;background:#f3f4f6;font:15px/1.7 Arial,sans-serif\"><div style=\"max-width:640px;margin:auto;background:#ffffff;border-top:5px solid %s;padding:28px;color:#1f2937\"><strong style=\"font-size:18px;color:%s\">%s</strong><div style=\"margin-top:16px\">%s</div></div></body></html>", accentColor, accentColor, escapedSubject, escapedBody)
	case "card":
		return fmt.Sprintf("<!doctype html><html><body style=\"margin:0;padding:24px;background:#f3f4f6;font:15px/1.7 Arial,sans-serif\"><div style=\"max-width:640px;margin:auto;background:#ffffff;border:1px solid #e5e7eb;padding:28px;color:#1f2937\"><strong style=\"font-size:18px\">%s</strong><div style=\"width:36px;height:3px;margin:14px 0;background:%s\"></div><div>%s</div></div></body></html>", escapedSubject, accentColor, escapedBody)
	default:
		return ""
	}
}

func normalizeEmailNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

type emailAttachment struct {
	filename    string
	contentType string
	data        []byte
}

func buildTemplateMIME(from, to, subject, body, htmlBody string) []byte {
	return buildMIME(from, to, subject, body, htmlBody, nil)
}

func buildMIMEWithAttachment(from, to, subject, body, htmlBody, filename, contentType string, attachment []byte) []byte {
	return buildMIME(from, to, subject, body, htmlBody, &emailAttachment{
		filename: filename, contentType: contentType, data: attachment,
	})
}

func buildMIME(from, to, subject, body, htmlBody string, attachment *emailAttachment) []byte {
	var buf bytes.Buffer
	if attachment == nil {
		alternativeBody, boundary := buildAlternativeBody(body, htmlBody)
		writeMIMEHeaders(&buf, from, to, subject, mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": boundary}))
		buf.Write(alternativeBody)
		return buf.Bytes()
	}

	var mixedBody bytes.Buffer
	mixed := multipart.NewWriter(&mixedBody)
	writeAlternativePart(mixed, body, htmlBody)
	writeAttachmentPart(mixed, *attachment)
	_ = mixed.Close()
	writeMIMEHeaders(&buf, from, to, subject, mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixed.Boundary()}))
	buf.Write(mixedBody.Bytes())
	return buf.Bytes()
}

func writeMIMEHeaders(buf *bytes.Buffer, from, to, subject, contentType string) {
	fmt.Fprintf(buf, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: %s\r\n\r\n",
		from, to, mime.QEncoding.Encode("UTF-8", subject), contentType)
}

func buildAlternativeBody(body, htmlBody string) ([]byte, string) {
	var buf bytes.Buffer
	alternative := multipart.NewWriter(&buf)
	writeTextPart(alternative, "text/plain; charset=UTF-8", body)
	writeTextPart(alternative, "text/html; charset=UTF-8", htmlBody)
	_ = alternative.Close()
	return buf.Bytes(), alternative.Boundary()
}

func writeAlternativePart(parent *multipart.Writer, body, htmlBody string) {
	alternativeBody, boundary := buildAlternativeBody(body, htmlBody)
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": boundary}))
	part, _ := parent.CreatePart(header)
	_, _ = part.Write(alternativeBody)
}

func writeTextPart(parent *multipart.Writer, contentType, content string) {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	part, _ := parent.CreatePart(header)
	encoded := quotedprintable.NewWriter(part)
	_, _ = encoded.Write([]byte(normalizeEmailNewlines(content)))
	_ = encoded.Close()
}

func writeAttachmentPart(parent *multipart.Writer, attachment emailAttachment) {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", attachment.contentType)
	header.Set("Content-Transfer-Encoding", "base64")
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.filename}))
	part, _ := parent.CreatePart(header)
	encoded := base64.StdEncoding.EncodeToString(attachment.data)
	for i := 0; i < len(encoded); i += 76 {
		end := min(i+76, len(encoded))
		_, _ = fmt.Fprint(part, encoded[i:end]+"\r\n")
	}
}
