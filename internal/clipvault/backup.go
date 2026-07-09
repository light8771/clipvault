package clipvault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/smtp"
	"os"
	"path/filepath"
	"sync"
	"time"
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
	cfgMgr *ConfigManager
	store  *Store
	logger *DailyLogger

	mu         sync.Mutex
	dirTimer   *time.Timer
	emailTimer *time.Timer

	statusMu sync.RWMutex
	dirStatus   BackupStatus
	emailStatus BackupStatus
}

// NewBackupService 创建备份服务，并注册配置变更监听。
func NewBackupService(cfgMgr *ConfigManager, store *Store, logger *DailyLogger) *BackupService {
	bs := &BackupService{
		cfgMgr: cfgMgr,
		store:  store,
		logger: logger,
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

	if cfg.Backup.DirBackup.Enabled && cfg.Backup.DirBackup.Interval > 0 && cfg.Backup.DirBackup.Dir != "" {
		interval := time.Duration(cfg.Backup.DirBackup.Interval) * time.Hour
		bs.dirTimer = time.AfterFunc(interval, bs.dirBackupTick)
	}

	if cfg.Backup.EmailBackup.Enabled && cfg.Backup.EmailBackup.Interval > 0 &&
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
	cfg := bs.cfgMgr.Get()
	return bs.doDirBackup(cfg)
}

// RunEmailBackup 立即执行一次邮件备份（由 API 端点调用）。
func (bs *BackupService) RunEmailBackup() error {
	cfg := bs.cfgMgr.Get()
	return bs.doEmailBackup(cfg)
}

// SendTestEmail 发送一封测试邮件（不包含备份数据）。
func (bs *BackupService) SendTestEmail() error {
	cfg := bs.cfgMgr.Get()
	eb := cfg.Backup.EmailBackup
	if eb.SMTPHost == "" || eb.To == "" {
		return fmt.Errorf("SMTP 配置不完整")
	}

	subject := "ClipVault 测试邮件"
	body := "这是一封来自 ClipVault 的测试邮件，用于验证 SMTP 配置是否正确。"

	msg := buildSimpleMIME(eb.From, eb.To, subject, body)

	addr := fmt.Sprintf("%s:%d", eb.SMTPHost, eb.SMTPPort)
	var auth smtp.Auth
	if eb.SMTPUser != "" {
		auth = smtp.PlainAuth("", eb.SMTPUser, eb.SMTPPass, eb.SMTPHost)
	}
	return smtp.SendMail(addr, auth, eb.From, []string{eb.To}, msg)
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
	if err := bs.doEmailBackup(cfg); err != nil {
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
	data, ext, err := bs.exportBackupData(cfg.Backup.DirBackup.Format, cfg.Backup.DirBackup.Password)
	if err != nil {
		bs.setDirStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}

	if err := os.MkdirAll(cfg.Backup.DirBackup.Dir, 0755); err != nil {
		err = fmt.Errorf("创建目录失败: %w", err)
		bs.setDirStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}

	filename := fmt.Sprintf("clipvault-backup-%s.%s", time.Now().Format("20060102-150405"), ext)
	fullPath := filepath.Join(cfg.Backup.DirBackup.Dir, filename)

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		err = fmt.Errorf("写入文件失败: %w", err)
		bs.setDirStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}

	bs.setDirStatus(BackupStatus{LastTime: time.Now(), LastOK: true, BackupFile: fullPath})
	bs.logger.Info("dir_backup_ok", map[string]any{"file": fullPath, "size": len(data)})
	return nil
}

func (bs *BackupService) doEmailBackup(cfg Config) error {
	data, ext, err := bs.exportBackupData(cfg.Backup.EmailBackup.Format, cfg.Backup.EmailBackup.Password)
	if err != nil {
		bs.setEmailStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}

	eb := cfg.Backup.EmailBackup
	filename := fmt.Sprintf("clipvault-backup-%s.%s", time.Now().Format("20060102-150405"), ext)
	subject := fmt.Sprintf("ClipVault 备份 %s", time.Now().Format("2006-01-02 15:04:05"))
	body := "ClipVault 自动备份文件，请查收附件。"

	contentType := "application/json"
	if ext == "cvexport" {
		contentType = "application/octet-stream"
	}

	msg := buildMIMEWithAttachment(eb.From, eb.To, subject, body, filename, contentType, data)

	addr := fmt.Sprintf("%s:%d", eb.SMTPHost, eb.SMTPPort)
	var auth smtp.Auth
	if eb.SMTPUser != "" {
		auth = smtp.PlainAuth("", eb.SMTPUser, eb.SMTPPass, eb.SMTPHost)
	}

	if err := smtp.SendMail(addr, auth, eb.From, []string{eb.To}, msg); err != nil {
		err = fmt.Errorf("发送邮件失败: %w", err)
		bs.setEmailStatus(BackupStatus{LastTime: time.Now(), LastOK: false, LastError: err.Error()})
		return err
	}

	bs.setEmailStatus(BackupStatus{LastTime: time.Now(), LastOK: true, BackupFile: filename})
	bs.logger.Info("email_backup_ok", map[string]any{"to": eb.To, "size": len(data)})
	return nil
}

// exportBackupData 导出数据并根据格式返回字节和扩展名。
func (bs *BackupService) exportBackupData(format, password string) ([]byte, string, error) {
	export, err := bs.store.ExportFullForJSON(context.Background())
	if err != nil {
		return nil, "", fmt.Errorf("导出数据失败: %w", err)
	}

	if format == "cvexport" {
		if password == "" {
			return nil, "", fmt.Errorf("cvexport 格式需要加密密码")
		}
		encData, err := EncryptExportJSON(export, password)
		if err != nil {
			return nil, "", fmt.Errorf("加密失败: %w", err)
		}
		return encData, "cvexport", nil
	}

	jsonData, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("JSON 编码失败: %w", err)
	}
	return jsonData, "json", nil
}

// buildSimpleMIME 构建一个纯文本 MIME 邮件。
func buildSimpleMIME(from, to, subject, body string) []byte {
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n",
		from, to, subject)
	return []byte(headers + body)
}

// buildMIMEWithAttachment 构建一个带附件的 MIME 邮件。
func buildMIMEWithAttachment(from, to, subject, body, filename, contentType string, attachment []byte) []byte {
	boundary := "----ClipVaultBackupBoundary"
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundary)

	// Text part
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=UTF-8\r\n")
	fmt.Fprintf(&buf, "Content-Transfer-Encoding: 8bit\r\n\r\n")
	fmt.Fprintf(&buf, "%s\r\n\r\n", body)

	// Attachment part
	encoded := base64.StdEncoding.EncodeToString(attachment)
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: %s\r\n", contentType)
	fmt.Fprintf(&buf, "Content-Transfer-Encoding: base64\r\n")
	fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=\"%s\"\r\n\r\n", filename)

	// Base64 line wrapping (76 chars per line)
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		buf.WriteString(encoded[i:end])
		buf.WriteString("\r\n")
	}

	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return buf.Bytes()
}
