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

func (bs *BackupService) doEmailBackup(cfg Config, trigger string, forceFull bool) error {
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
	bs.markEmailRunning(trigger)
	fail := func(now time.Time, scope ExportScope, autoFallback bool, partsSent, totalParts, items int, runErr error) error {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
		bs.setEmailStatus(makeBackupStatus(now, false, scope, autoFallback, wm, runErr.Error(), "", trigger))
		bs.recordBackupHistory("email", trigger, scope.Mode, false, runErr, eb.To, eb.Format, partsSent, items)
		return runErr
	}
	normalizedScope, err := normalizeExportRange(eb.Scope, cfg.Search.TimeZone)
	if err != nil {
		return fail(time.Now(), eb.Scope, false, 0, 0, 0, err)
	}
	eb.Scope = normalizedScope
	scope, autoFallback, err := resolveBackupScope(bs.store, "email", eb.Scope, eb.FullFallbackDays, forceFull)
	if err != nil {
		return fail(time.Now(), eb.Scope, false, 0, 0, 0, err)
	}
	now := time.Now()
	// 无数据可备份时跳过：增量没有新记录、或全量时库为空，
	// 不发送空备份邮件，水位线保持不动。
	total, err := bs.store.CountInScope(context.Background(), scope)
	if err != nil {
		return fail(now, scope, autoFallback, 0, 0, 0, fmt.Errorf("统计备份数据失败: %w", err))
	}
	if total == 0 {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
		status := makeBackupStatus(now, true, scope, autoFallback, wm, "", "", trigger)
		status.Skipped = true
		bs.setEmailStatus(status)
		bs.recordBackupHistory("email", trigger, "skipped", true, nil, eb.To, eb.Format, 0, 0)
		bs.logger.Info("email_backup_skipped_empty", map[string]any{"mode": scope.Mode, "trigger": trigger})
		return nil
	}
	parts, ext, exportedMaxID, err := bs.exportEmailPartFilesSized(eb, scope)
	if err != nil {
		return fail(now, scope, autoFallback, 0, 0, total, err)
	}
	defer cleanupEmailPartFiles(parts)
	totalParts := len(parts)

	for i, part := range parts {
		size, err := emailBackupFileMessageSize(eb, ext, now, i, totalParts, part.path)
		if err != nil {
			return fail(now, scope, autoFallback, 0, totalParts, total, err)
		}
		if size > int64(eb.MaxPartSize) {
			return fail(now, scope, autoFallback, 0, totalParts, total,
				fmt.Errorf("第 %d/%d 封邮件为 %d 字节，超过阈值 %d，请缩小备份范围或减少单条记录大小", i+1, totalParts, size, eb.MaxPartSize))
		}
	}

	// 只有跟踪水位线的范围模式才逐片推进；range 是一次性导出，不动水位线。
	advancePerPart := scope.Mode == "incremental" || scope.Mode == "full"
	lastFilename := ""
	for i, part := range parts {
		filename := emailBackupFilename(ext, now, i, totalParts)
		if err := sendSMTPMessageStream(eb, func(w io.Writer) error {
			return writeEmailBackupMessage(w, eb, ext, now, i, totalParts, part.path)
		}); err != nil {
			bs.sendBackupFailureNotice(eb, now, i, totalParts, ext, err)
			return fail(now, scope, autoFallback, i, totalParts, total,
				fmt.Errorf("发送邮件失败(第%d封/%d): %w", i+1, totalParts, err))
		}
		lastFilename = filename
		if advancePerPart && part.endID > 0 {
			if _, wmErr := updateBackupWatermark(bs.store, "email", ExportScope{Mode: "incremental"}, part.endID); wmErr != nil {
				return fail(now, scope, autoFallback, i+1, totalParts, total,
					fmt.Errorf("第 %d 封已发送，但记录备份进度失败: %w", i+1, wmErr))
			}
		}
	}

	// 全量备份：所有片段发送完成，刷新全量时间（增量备份的 LastFullAt
	// 由逐片推进保留不变）。
	wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
	if scope.Mode == "full" {
		var wmErr error
		wm, wmErr = updateBackupWatermark(bs.store, "email", scope, exportedMaxID)
		if wmErr != nil {
			bs.logger.Error("email_backup_watermark_failed", wmErr, map[string]any{"mode": scope.Mode})
			return fail(now, scope, autoFallback, totalParts, totalParts, total,
				fmt.Errorf("邮件已全部发送，但更新备份进度失败: %w", wmErr))
		}
	}
	statusFile := lastFilename
	if totalParts > 1 {
		statusFile = fmt.Sprintf("%d part(s)", totalParts)
	}
	bs.setEmailStatus(makeBackupStatus(now, true, scope, autoFallback, wm, "", statusFile, trigger))
	bs.recordBackupHistory("email", trigger, scope.Mode, true, nil, eb.To, ext, totalParts, total)
	bs.logger.Info("email_backup_ok", map[string]any{"to": eb.To, "parts": totalParts, "mode": scope.Mode, "autoFallback": autoFallback, "trigger": trigger})
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
