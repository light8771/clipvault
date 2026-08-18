package clipvault

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// exportEmailBackupTo 把一份备份分片写入 dst，永远使用 .cvexport 加密容器：
//   - format=cvexport：JSON 导出直接作为加密载荷（与目录备份的 cvexport 一致）；
//   - format=json/xlsx：明文导出先打进 zip（clipvault-export.json/.xlsx），
//     zip 字节再作为加密载荷——邮件附件绝不出现明文数据。
//
// 与目录备份共用 exportBackupTo 实现，加密密码规则一致。
// 返回最后一条记录的 id（无数据时为 0），供写水印使用。
func (bs *BackupService) exportEmailBackupTo(dst io.Writer, format, password string, scope ExportScope) (int64, error) {
	return bs.exportBackupTo(dst, format, password, scope)
}

func (bs *BackupService) doEmailBackup(cfg Config, mode string, forceFull bool) error {
	bs.execMu.Lock()
	defer bs.execMu.Unlock()
	if bs.isStopped() {
		return errBackupStopped
	}

	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	eb := cfg.Backup.EmailBackup
	if msg := validateEmailBackupEnabled(eb); msg != "" {
		return fmt.Errorf("邮件备份配置不完整: %s", msg)
	}
	normalizedScope, err := normalizeExportRange(eb.Scope, cfg.Search.TimeZone)
	if err != nil {
		return err
	}
	eb.Scope = normalizedScope
	scope, autoFallback, err := resolveBackupScope(bs.store, "email", eb.Scope, eb.IncrementalFullInterval, forceFull)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
		bs.setEmailStatus(makeBackupStatus(time.Now(), false, eb.Scope, false, wm, eb.IncrementalFullInterval, err.Error(), ""))
		return err
	}
	parts, ext, exportedMaxID, err := bs.exportEmailPartFilesSized(eb, scope)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
		bs.setEmailStatus(makeBackupStatus(time.Now(), false, scope, autoFallback, wm, eb.IncrementalFullInterval, err.Error(), ""))
		return err
	}
	defer cleanupEmailPartFiles(parts)

	now := time.Now()
	total := len(parts)

	for i, part := range parts {
		size, err := emailBackupFileMessageSize(eb, ext, now, i, total, part.path)
		if err != nil {
			wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
			bs.setEmailStatus(makeBackupStatus(now, false, scope, autoFallback, wm, eb.IncrementalFullInterval, err.Error(), ""))
			return err
		}
		if size > int64(eb.MaxPartSize) {
			err := fmt.Errorf("第 %d/%d 封邮件为 %d 字节，超过阈值 %d，请缩小备份范围或减少单条记录大小", i+1, total, size, eb.MaxPartSize)
			wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
			bs.setEmailStatus(makeBackupStatus(now, false, scope, autoFallback, wm, eb.IncrementalFullInterval, err.Error(), ""))
			return err
		}
	}

	lastFilename := ""
	for i, part := range parts {
		filename := emailBackupFilename(ext, now, i, total)
		if err := sendSMTPMessageStream(eb, func(w io.Writer) error {
			return writeEmailBackupMessage(w, eb, ext, now, i, total, part.path)
		}); err != nil {

			bs.sendBackupFailureNotice(eb, now, i, total, ext, err)
			err = fmt.Errorf("发送邮件失败(第%d封/%d): %w", i+1, total, err)
			wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
			bs.setEmailStatus(makeBackupStatus(now, false, scope, autoFallback, wm, eb.IncrementalFullInterval, err.Error(), ""))
			return err
		}
		lastFilename = filename
	}

	if err := bs.store.AddEmailBackupHistory(context.Background(), EmailBackupHistory{
		SentAt: now, Mode: mode, From: eb.From, To: eb.To, Format: ext,
	}); err != nil {
		bs.logger.Error("email_backup_history_record_failed", err, nil)
	}
	wm, wmErr := updateBackupWatermark(bs.store, "email", scope, exportedMaxID)
	statusFile := lastFilename
	if total > 1 {
		statusFile = fmt.Sprintf("%d part(s)", total)
	}
	if wmErr != nil {
		bs.logger.Error("email_backup_watermark_failed", wmErr, map[string]any{"mode": scope.Mode})
		bs.setEmailStatus(makeBackupStatus(now, false, scope, autoFallback, wm, eb.IncrementalFullInterval, wmErr.Error(), statusFile))
		return wmErr
	}
	bs.setEmailStatus(makeBackupStatus(now, true, scope, autoFallback, wm, eb.IncrementalFullInterval, "", statusFile))
	bs.logger.Info("email_backup_ok", map[string]any{"to": eb.To, "parts": total, "mode": scope.Mode, "autoFallback": autoFallback})
	return nil
}

func (bs *BackupService) isStopped() bool {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return bs.stopped
}

func sendSMTPMessage(cfg EmailBackupConfig, msg []byte) error {
	return sendSMTPMessageStream(cfg, func(w io.Writer) error {
		_, err := w.Write(msg)
		return err
	})
}

func sendSMTPMessageStream(cfg EmailBackupConfig, writeMessage func(io.Writer) error) error {
	if problems := smtpDeliveryErrors(cfg); len(problems) > 0 {
		return fmt.Errorf("SMTP 配置不完整: %s", strings.Join(problems, "、"))
	}
	host := strings.TrimSpace(cfg.SMTPHost)
	from, _ := mail.ParseAddress(strings.TrimSpace(cfg.From))
	to, _ := mail.ParseAddress(strings.TrimSpace(cfg.To))
	smtpUser := strings.TrimSpace(cfg.SMTPUser)

	addr := net.JoinHostPort(host, strconv.Itoa(cfg.SMTPPort))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}

	var client *smtp.Client
	if cfg.SMTPPort == 465 {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("SMTP TLS 连接失败: %w", err)
		}
		if err := conn.SetDeadline(time.Now().Add(smtpOperationTimeout)); err != nil {
			conn.Close()
			return fmt.Errorf("设置 SMTP 超时失败: %w", err)
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
		if err := conn.SetDeadline(time.Now().Add(smtpOperationTimeout)); err != nil {
			conn.Close()
			return fmt.Errorf("设置 SMTP 超时失败: %w", err)
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

	if smtpUser != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("SMTP 服务器不支持身份认证")
		}
		if err := client.Auth(smtp.PlainAuth("", smtpUser, cfg.SMTPPass, host)); err != nil {
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
	if err := writeMessage(w); err != nil {
		w.Close()
		return fmt.Errorf("SMTP 邮件写入失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("SMTP 邮件提交失败: %w", err)
	}
	_ = client.Quit()
	return nil
}
