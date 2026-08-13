package clipvault

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"clipboard_ext/internal/licensing"
)

var (
	errBackupLicenseRequired = errors.New("需要先激活 ClipVault 才能使用备份功能")
	errBackupStopped         = errors.New("备份服务已停止")
)

const (
	emailBackupModeAutomatic = "automatic"
	emailBackupModeManual    = "manual"
	smtpOperationTimeout     = 5 * time.Minute
	// maxEmailParts 是单次邮件备份分片总数的硬上限，防止数据量异常时
	// 连续发送海量邮件，触发 SMTP 限流、账号封禁或收件箱轰炸。
	maxEmailParts             = 60
	initialEmailPartProbeRows = 5000
)

// BackupStatus 记录最近一次备份的状态。
type BackupStatus struct {
	LastTime   time.Time
	LastOK     bool
	LastError  string
	BackupFile string
	// 透明增量链信息
	ActualMode       string // 实际执行的模式：full / incremental
	AutoFallback     bool   // 是否为自动兜底降级（配置incremental但跑了full）
	IncrementalCount int    // 当前增量链已执行次数
	FullInterval     int    // 配置的兜底阈值（0=不自动兜底）
	LastFullAt       string // 上次全量备份时间
}

// BackupService manages scheduled directory and email backups.
type BackupService struct {
	cfgMgr  *ConfigManager
	store   *Store
	logger  *DailyLogger
	license *licensing.Manager

	mu              sync.Mutex
	execMu          sync.Mutex
	dirTimer        *time.Timer
	dirGeneration   uint64
	dirState        backupTimerState
	emailTimer      *time.Timer
	emailGeneration uint64
	emailState      backupTimerState
	startupOnce     sync.Once
	startupWG       sync.WaitGroup
	stopped         bool

	statusMu    sync.RWMutex
	dirStatus   BackupStatus
	emailStatus BackupStatus
}

type backupTimerState struct {
	active   bool
	interval int
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
	bs.mu.Lock()
	if bs.stopped {
		bs.mu.Unlock()
		return
	}
	startStartup := false
	bs.startupOnce.Do(func() {
		// Add while holding mu so Stop cannot begin Wait concurrently with
		// the first positive Add on this WaitGroup.
		bs.startupWG.Add(1)
		startStartup = true
	})
	bs.mu.Unlock()
	cfg := bs.cfgMgr.Get()
	bs.Reload(cfg)
	if startStartup {
		go func() {
			defer bs.startupWG.Done()
			bs.runStartupBackups(cfg)
		}()
	}
}

// Stop prevents new scheduled work, waits for startup and in-flight backup
// operations, and is safe to call more than once.
func (bs *BackupService) Stop() {
	bs.mu.Lock()
	if bs.stopped {
		bs.mu.Unlock()
		return
	}
	bs.stopped = true
	bs.dirGeneration++
	bs.emailGeneration++
	if bs.dirTimer != nil {
		bs.dirTimer.Stop()
		bs.dirTimer = nil
	}
	if bs.emailTimer != nil {
		bs.emailTimer.Stop()
		bs.emailTimer = nil
	}
	bs.dirState = backupTimerState{}
	bs.emailState = backupTimerState{}
	bs.mu.Unlock()

	bs.startupWG.Wait()
	bs.execMu.Lock()
	bs.execMu.Unlock()
}

func (bs *BackupService) runStartupBackups(cfg Config) {
	runStartupBackups(cfg,
		func(c Config) error {
			err := bs.doDirBackup(c, false)
			if err != nil && !errors.Is(err, errBackupStopped) {
				bs.logger.Error("dir_backup_startup_failed", err, nil)
			}
			return err
		},
		func(c Config) error {
			err := bs.doEmailBackup(c, emailBackupModeAutomatic, false)
			if err != nil && !errors.Is(err, errBackupStopped) {
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

// Reload updates backup timers when the schedule state actually changes.
func (bs *BackupService) Reload(cfg Config) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.stopped {
		return
	}

	licensed := bs.license == nil || bs.license.CanUsePaidFeatures()

	if state := dirBackupTimerState(cfg, licensed); state != bs.dirState {
		bs.dirGeneration++
		if bs.dirTimer != nil {
			bs.dirTimer.Stop()
			bs.dirTimer = nil
		}
		bs.dirState = state
		if state.active {
			bs.scheduleDirTimerLocked(state.interval, bs.dirGeneration)
		}
	}

	if state := emailBackupTimerState(cfg, licensed); state != bs.emailState {
		bs.emailGeneration++
		if bs.emailTimer != nil {
			bs.emailTimer.Stop()
			bs.emailTimer = nil
		}
		bs.emailState = state
		if state.active {
			bs.scheduleEmailTimerLocked(state.interval, bs.emailGeneration)
		}
	}
}

func dirBackupTimerState(cfg Config, licensed bool) backupTimerState {
	if !licensed || !dirBackupScheduleReady(cfg.Backup.DirBackup) {
		return backupTimerState{}
	}
	return backupTimerState{active: true, interval: cfg.Backup.DirBackup.Interval}
}

func emailBackupTimerState(cfg Config, licensed bool) backupTimerState {
	if !licensed || !emailBackupScheduleReady(cfg.Backup.EmailBackup) {
		return backupTimerState{}
	}
	return backupTimerState{active: true, interval: cfg.Backup.EmailBackup.Interval}
}

func dirBackupScheduleReady(backup DirBackupConfig) bool {
	return backup.Enabled && validateDirBackupEnabled(backup) == ""
}

func emailBackupScheduleReady(backup EmailBackupConfig) bool {
	return backup.Enabled && validateEmailBackupEnabled(backup) == ""
}

func (bs *BackupService) scheduleDirTimerLocked(intervalHours int, generation uint64) {
	interval := time.Duration(intervalHours) * time.Hour
	bs.dirTimer = time.AfterFunc(interval, func() {
		bs.dirBackupTick(generation)
	})
}

func (bs *BackupService) scheduleEmailTimerLocked(intervalHours int, generation uint64) {
	interval := time.Duration(intervalHours) * time.Hour
	bs.emailTimer = time.AfterFunc(interval, func() {
		bs.emailBackupTick(generation)
	})
}

func (bs *BackupService) rearmDirTimer(cfg Config, generation uint64) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.stopped || generation != bs.dirGeneration || !dirBackupScheduleReady(cfg.Backup.DirBackup) {
		return
	}
	bs.scheduleDirTimerLocked(cfg.Backup.DirBackup.Interval, generation)
}

func (bs *BackupService) rearmEmailTimer(cfg Config, generation uint64) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.stopped || generation != bs.emailGeneration || !emailBackupScheduleReady(cfg.Backup.EmailBackup) {
		return
	}
	bs.scheduleEmailTimerLocked(cfg.Backup.EmailBackup.Interval, generation)
}

func (bs *BackupService) isCurrentDirTimer(generation uint64) bool {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return !bs.stopped && generation == bs.dirGeneration
}

func (bs *BackupService) isCurrentEmailTimer(generation uint64) bool {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return !bs.stopped && generation == bs.emailGeneration
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

// RunDirBackup 立即执行一次目录备份（由 API 端点调用）。forceFull=true 强制全量并重置增量链。
func (bs *BackupService) RunDirBackup(forceFull bool) error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	cfg := bs.cfgMgr.Get()
	return bs.doDirBackup(cfg, forceFull)
}

// RunEmailBackup 立即执行一次邮件备份（由 API 端点调用）。forceFull=true 强制全量并重置增量链。
func (bs *BackupService) RunEmailBackup(forceFull bool) error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	cfg := bs.cfgMgr.Get()
	return bs.doEmailBackup(cfg, emailBackupModeManual, forceFull)
}

// SendTestEmail 发送一封测试邮件（不包含备份数据）。
func (bs *BackupService) SendTestEmail() error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	cfg := bs.cfgMgr.Get()
	eb := cfg.Backup.EmailBackup
	if problems := smtpDeliveryErrors(eb); len(problems) > 0 {
		return fmt.Errorf("SMTP 配置不完整: %s", strings.Join(problems, "、"))
	}
	if errors := emailTemplateErrors(eb); len(errors) > 0 {
		return fmt.Errorf("邮件模板配置不完整: %s", strings.Join(errors, "、"))
	}
	subject, body, htmlBody := renderEmailTemplate(eb, "测试邮件（无附件）", "测试", time.Now())
	msg := buildTemplateMIME(eb.From, eb.To, subject, body, htmlBody)

	return sendSMTPMessage(eb, msg)
}

func (bs *BackupService) dirBackupTick(generation uint64) {
	if !bs.isCurrentDirTimer(generation) {
		return
	}
	cfg := bs.cfgMgr.Get()
	if !dirBackupScheduleReady(cfg.Backup.DirBackup) {
		return
	}
	if err := bs.doDirBackup(cfg, false); err != nil {
		bs.logger.Error("dir_backup_failed", err, nil)
	}
	bs.rearmDirTimer(cfg, generation)
}

func (bs *BackupService) emailBackupTick(generation uint64) {
	if !bs.isCurrentEmailTimer(generation) {
		return
	}
	cfg := bs.cfgMgr.Get()
	if !emailBackupScheduleReady(cfg.Backup.EmailBackup) {
		return
	}
	if err := bs.doEmailBackup(cfg, emailBackupModeAutomatic, false); err != nil {
		bs.logger.Error("email_backup_failed", err, nil)
	}
	bs.rearmEmailTimer(cfg, generation)
}

func (bs *BackupService) doDirBackup(cfg Config, forceFull bool) error {
	bs.execMu.Lock()
	defer bs.execMu.Unlock()
	if bs.isStopped() {
		return errBackupStopped
	}

	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	db := cfg.Backup.DirBackup
	if msg := validateDirBackupEnabled(db); msg != "" {
		return fmt.Errorf("目录备份配置不完整: %s", msg)
	}
	normalizedScope, err := normalizeExportRange(db.Scope, cfg.Search.TimeZone)
	if err != nil {
		return err
	}
	db.Scope = normalizedScope
	scope, autoFallback, err := resolveBackupScope(bs.store, "dir", db.Scope, db.IncrementalFullInterval, forceFull)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		bs.setDirStatus(makeBackupStatus(time.Now(), false, db.Scope, false, wm, db.IncrementalFullInterval, err.Error(), ""))
		return err
	}
	now := time.Now()
	ext := db.Format
	if ext != "json" && ext != "xlsx" && ext != "cvexport" {
		err := fmt.Errorf("不支持的备份格式: %s", ext)
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		bs.setDirStatus(makeBackupStatus(now, false, scope, autoFallback, wm, db.IncrementalFullInterval, err.Error(), ""))
		return err
	}
	fullPath, size, exportedMaxID, err := bs.writeBackupFileStream(db.Dir, now, ext, db.Password, scope)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		bs.setDirStatus(makeBackupStatus(now, false, scope, autoFallback, wm, db.IncrementalFullInterval, err.Error(), ""))
		return err
	}

	wm, wmErr := updateBackupWatermark(bs.store, "dir", scope, exportedMaxID)
	if wmErr != nil {
		bs.logger.Error("dir_backup_watermark_failed", wmErr, map[string]any{"mode": scope.Mode})
		bs.setDirStatus(makeBackupStatus(now, false, scope, autoFallback, wm, db.IncrementalFullInterval, wmErr.Error(), fullPath))
		return wmErr
	}
	bs.setDirStatus(makeBackupStatus(now, true, scope, autoFallback, wm, db.IncrementalFullInterval, "", fullPath))
	bs.logger.Info("dir_backup_ok", map[string]any{"file": fullPath, "size": size, "mode": scope.Mode, "autoFallback": autoFallback})
	return nil
}

func (bs *BackupService) writeBackupFileStream(dir string, now time.Time, format, password string, scope ExportScope) (string, int64, int64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, 0, fmt.Errorf("创建目录失败: %w", err)
	}
	file, err := os.CreateTemp(dir, fmt.Sprintf("clipvault-backup-%s-*.%s", now.Format("20060102-150405"), format))
	if err != nil {
		return "", 0, 0, fmt.Errorf("创建备份文件失败: %w", err)
	}
	path := file.Name()
	success := false
	defer func() {
		if !success {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	maxID, err := bs.exportBackupTo(file, format, password, scope)
	if err != nil {
		return "", 0, 0, err
	}
	if err := file.Sync(); err != nil {
		return "", 0, 0, fmt.Errorf("同步文件失败: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return "", 0, 0, fmt.Errorf("读取备份文件信息失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", 0, 0, fmt.Errorf("关闭文件失败: %w", err)
	}
	success = true
	return path, info.Size(), maxID, nil
}

func (bs *BackupService) exportBackupTo(dst io.Writer, format, password string, scope ExportScope) (int64, error) {
	switch format {
	case "json":
		maxID, err := bs.store.StreamExport(context.Background(), scope, dst)
		if err != nil {
			return 0, fmt.Errorf("导出数据失败: %w", err)
		}
		return maxID, nil
	case "cvexport":
		if password == "" {
			return 0, errors.New("cvexport 格式需要加密密码")
		}
		var maxID int64
		err := WriteCVExport(dst, password, func(plain io.Writer) error {
			var streamErr error
			maxID, streamErr = bs.store.StreamExport(context.Background(), scope, plain)
			return streamErr
		})
		if err != nil {
			return 0, fmt.Errorf("加密失败: %w", err)
		}
		return maxID, nil
	case "xlsx":
		maxID, err := bs.store.WriteXLSXScope(context.Background(), scope, dst)
		if err != nil {
			return 0, fmt.Errorf("Excel 导出失败: %w", err)
		}
		return maxID, nil
	default:
		return 0, fmt.Errorf("不支持的备份格式: %s", format)
	}
}

func writeBackupFile(dir string, now time.Time, ext string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}
	file, err := os.CreateTemp(dir, fmt.Sprintf("clipvault-backup-%s-*.%s", now.Format("20060102-150405"), ext))
	if err != nil {
		return "", fmt.Errorf("创建备份文件失败: %w", err)
	}
	path := file.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("写入文件失败: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("同步文件失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("关闭文件失败: %w", err)
	}
	success = true
	return path, nil
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
			// 部分片已发出无法撤回，发一封通知邮件告知收件人不完整
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

// exportBackupData 导出单份数据（用于目录备份），按 scope 过滤。
// 返回 (data, ext, exportedMaxID)：exportedMaxID 是该次导出快照包含的
// 最后一条记录的 id（无数据时为 0），供写水印使用，避免二次查询竞态。
func (bs *BackupService) exportBackupData(format, password string, scope ExportScope) ([]byte, string, int64, error) {
	if format != "json" && format != "xlsx" && format != "cvexport" {
		return nil, "", 0, fmt.Errorf("不支持的备份格式: %s", format)
	}
	var buf bytes.Buffer
	maxID, err := bs.exportBackupTo(&buf, format, password, scope)
	if err != nil {
		return nil, "", 0, err
	}
	return buf.Bytes(), format, maxID, nil
}

// resolveBackupScope resolves the actual scope to run: for incremental mode it
// reads the watermark to fill SinceID; if no watermark exists or the incremental
// chain reached fullInterval, it falls back to full. forceFull (manual trigger)
// always runs full. Returns (scope, autoFallback) where autoFallback is true only
// for automatic degradation (not manual forceFull).
func resolveBackupScope(store *Store, kind string, configured ExportScope, fullInterval int, forceFull bool) (ExportScope, bool, error) {
	if configured.Mode == "" {
		configured.Mode = "full"
	}
	if forceFull {
		configured.Mode = "full"
		configured.From = ""
		configured.To = ""
		configured.SinceID = 0
		configured.MaxID = 0
		return configured, false, nil
	}
	if configured.Mode == "range" {
		// 复用统一校验（validateExportScope：非空 + From<To + 格式合法），
		// 与导出 API handleExportData 口径一致，避免两条路径校验不一致。
		if err := validateExportScope(configured); err != nil {
			return configured, false, fmt.Errorf("range 备份配置非法: %w", err)
		}
		return configured, false, nil
	}
	if configured.Mode != "incremental" {
		return configured, false, nil
	}
	wm, err := store.GetBackupWatermark(context.Background(), kind)
	if err != nil {
		return configured, false, fmt.Errorf("读取备份水印失败: %w", err)
	}
	if wm.LastItemID <= 0 {
		configured.Mode = "full"
		return configured, true, nil
	}
	if fullInterval > 0 && wm.IncrementalCount >= fullInterval {
		configured.Mode = "full"
		return configured, true, nil
	}
	configured.SinceID = wm.LastItemID
	return configured, false, nil
}

// updateBackupWatermark records the backup position from the id that was ACTUALLY
// exported (exportedMaxID), not from a fresh query — a later query would race
// with concurrent clipboard writes and could advance the watermark past rows
// that were never exported, silently losing them from every future incremental
// backup. exportedMaxID<=0 means "no data was exported" and the watermark is
// left untouched (the existing watermark is returned so the UI status does not
// flash a reset).
func updateBackupWatermark(store *Store, kind string, scope ExportScope, exportedMaxID int64) (BackupWatermark, error) {
	wm, err := store.GetBackupWatermark(context.Background(), kind)
	if err != nil {
		return BackupWatermark{}, fmt.Errorf("读取水印失败: %w", err)
	}
	// A date-range export is an independent snapshot, not a link in the
	// full/incremental chain. Advancing the shared watermark here would make a
	// later incremental backup skip records outside the selected date range.
	if scope.Mode == "range" || exportedMaxID <= 0 {
		return wm, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	newWM := BackupWatermark{LastItemID: exportedMaxID, LastSentAt: now}
	if scope.Mode == "incremental" {
		newWM.IncrementalCount = wm.IncrementalCount + 1
		newWM.LastFullAt = wm.LastFullAt
	} else {
		newWM.IncrementalCount = 0
		newWM.LastFullAt = now
	}
	if err := store.SetBackupWatermark(context.Background(), kind, newWM); err != nil {
		return wm, fmt.Errorf("写回水印失败: %w", err)
	}
	return newWM, nil
}

// makeBackupStatus builds a BackupStatus with transparent incremental-chain info.
// wm is provided by the caller (updateBackupWatermark's return on success, or a
// fresh read on failure) to avoid a redundant settings lookup here.
func makeBackupStatus(now time.Time, ok bool, scope ExportScope, autoFallback bool, wm BackupWatermark, fullInterval int, errMsg, file string) BackupStatus {
	return BackupStatus{
		LastTime:         now,
		LastOK:           ok,
		LastError:        errMsg,
		BackupFile:       file,
		ActualMode:       scope.Mode,
		AutoFallback:     autoFallback,
		IncrementalCount: wm.IncrementalCount,
		FullInterval:     fullInterval,
		LastFullAt:       wm.LastFullAt,
	}
}

// exportEmailParts exports data split into multiple self-contained parts (one per
// email). Each part is an independent ExportData over a row-count-sized id window,
// so it can be imported on its own. cvexport parts are individually encrypted.
//
// The returned maxID is the id of the LAST item actually included in the parts,
// computed from the same snapshot that produced the parts, so the caller can
// persist the backup watermark from the exported data (not from a later query —
// that would race with concurrent clipboard writes and silently skip rows).
// exportEmailPartFilesSized adaptively chooses the largest row count whose final
// attachment (including XLSX packaging or cvexport encryption) fits the
// configured final MIME size. A single oversized record is reported
// explicitly instead of producing a partially sent backup set.
type emailBackupPartFile struct {
	path string
}

func cleanupEmailPartFiles(parts []emailBackupPartFile) {
	for _, part := range parts {
		_ = os.Remove(part.path)
	}
}

// exportEmailPartFilesSized is the production email path. Attachments are
// staged on disk and later streamed one message at a time, so total backup size
// no longer multiplies heap usage.
func (bs *BackupService) exportEmailPartFilesSized(cfg EmailBackupConfig, scope ExportScope) (_ []emailBackupPartFile, ext string, maxID int64, resultErr error) {
	format := cfg.Format
	ext = format
	if format != "json" && format != "xlsx" && format != "cvexport" {
		return nil, "", 0, fmt.Errorf("不支持的备份格式: %s", format)
	}
	if cfg.MaxPartSize <= 0 {
		return nil, "", 0, errors.New("单封邮件大小上限必须大于 0")
	}
	var parts []emailBackupPartFile
	defer func() {
		if resultErr != nil {
			cleanupEmailPartFiles(parts)
		}
	}()
	total, err := bs.store.CountInScope(context.Background(), scope)
	if err != nil {
		return nil, "", 0, fmt.Errorf("统计分片行数失败: %w", err)
	}
	if total <= 0 {
		part, err := bs.exportEmailPartFile(format, cfg.Password, scope)
		if err != nil {
			return nil, "", 0, fmt.Errorf("导出空备份失败: %w", err)
		}
		parts = append(parts, part)
		size, err := emailBackupFileMessageSize(cfg, ext, time.Unix(0, 0), maxEmailParts-1, maxEmailParts, part.path)
		if err != nil {
			return nil, "", 0, err
		}
		if size > int64(cfg.MaxPartSize) {
			return nil, "", 0, fmt.Errorf("空备份邮件仍超过上限 %d 字节", cfg.MaxPartSize)
		}
		return parts, ext, 0, nil
	}

	lastID := scope.SinceID
	if scope.Mode == "range" || scope.Mode == "" || scope.Mode == "full" {
		lastID = 0
	}
	consumed := 0
	for consumed < total {
		if len(parts) >= maxEmailParts {
			return nil, "", 0, fmt.Errorf("备份需要超过 %d 封邮件，请提高单封大小上限、改用目录备份或缩小范围", maxEmailParts)
		}
		// Grow candidate row counts exponentially before binary-searching the
		// final bracket. This can exceed the initial probe without first creating
		// an attachment containing half of a very large database.
		remaining := emailPartCandidateUpperBound(total, consumed)
		bestRows := 0
		bestEndID := int64(0)
		bestPart := emailBackupPartFile{}
		removeBest := func() {
			if bestPart.path != "" {
				_ = os.Remove(bestPart.path)
				bestPart.path = ""
			}
		}
		tryRows := func(rows int) (bool, error) {
			endID, err := bs.store.NthIDInScopeAfter(context.Background(), scope, lastID, rows-1)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return false, nil
				}
				return false, fmt.Errorf("定位分片边界失败: %w", err)
			}
			partScope := scope
			partScope.SinceID = lastID
			partScope.MaxID = endID
			candidate, err := bs.exportEmailPartFile(format, cfg.Password, partScope)
			if err != nil {
				return false, err
			}
			size, err := emailBackupFileMessageSize(cfg, ext, time.Unix(0, 0), maxEmailParts-1, maxEmailParts, candidate.path)
			if err != nil {
				_ = os.Remove(candidate.path)
				return false, err
			}
			if size <= int64(cfg.MaxPartSize) {
				if bestPart.path != "" {
					_ = os.Remove(bestPart.path)
				}
				bestPart, bestEndID, bestRows = candidate, endID, rows
				return true, nil
			}
			_ = os.Remove(candidate.path)
			return false, nil
		}

		probe := min(initialEmailPartProbeRows, remaining)
		failedRows := 0
		for {
			fits, err := tryRows(probe)
			if err != nil {
				removeBest()
				return nil, "", 0, err
			}
			if !fits {
				failedRows = probe
				break
			}
			if probe == remaining {
				break
			}
			probe = nextEmailPartProbe(probe, remaining)
		}
		if failedRows > 0 {
			low, high := bestRows+1, failedRows-1
			for low <= high {
				rows := low + (high-low)/2
				fits, err := tryRows(rows)
				if err != nil {
					removeBest()
					return nil, "", 0, err
				}
				if fits {
					low = rows + 1
				} else {
					high = rows - 1
				}
			}
		}
		if bestRows == 0 {
			removeBest()
			return nil, "", 0, fmt.Errorf("至少一条记录单独导出后邮件仍超过上限 %d 字节；请提高上限或改用目录备份", cfg.MaxPartSize)
		}
		parts = append(parts, bestPart)
		lastID = bestEndID
		consumed += bestRows
	}
	return parts, ext, lastID, nil
}

func emailPartCandidateUpperBound(total, consumed int) int {
	if total <= consumed {
		return 0
	}
	return total - consumed
}

func nextEmailPartProbe(current, remaining int) int {
	if current >= remaining {
		return remaining
	}
	if current > remaining/2 {
		return remaining
	}
	return current * 2
}

func (bs *BackupService) exportEmailPartFile(format, password string, scope ExportScope) (_ emailBackupPartFile, resultErr error) {
	file, err := os.CreateTemp("", "clipvault-email-part-*."+format)
	if err != nil {
		return emailBackupPartFile{}, fmt.Errorf("创建邮件分片文件失败: %w", err)
	}
	path := file.Name()
	success := false
	defer func() {
		if !success {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if _, err := bs.exportBackupTo(file, format, password, scope); err != nil {
		return emailBackupPartFile{}, err
	}
	if err := file.Sync(); err != nil {
		return emailBackupPartFile{}, fmt.Errorf("同步邮件分片失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return emailBackupPartFile{}, fmt.Errorf("关闭邮件分片失败: %w", err)
	}
	success = true
	return emailBackupPartFile{path: path}, nil
}

// sendBackupFailureNotice sends a notification email when a multi-part backup
// failed partway, so the recipient knows the partial parts cannot be restored
// alone and a full retry is needed. Failure to send the notice is only logged
// and does not mask the original backup error.
func (bs *BackupService) sendBackupFailureNotice(eb EmailBackupConfig, startedAt time.Time, sentParts, totalParts int, ext string, cause error) {
	subject := fmt.Sprintf("⚠️ ClipVault 备份未完成（已发 %d/%d 片）", sentParts, totalParts)
	body := fmt.Sprintf(`ClipVault 邮件备份未完成

备份发起时间：%s
备份格式：%s
计划分片总数：%d
已成功发送：%d 片
失败位置：第 %d 片
失败原因：%v

重要说明：
- 已发送的 %d 片已无法撤回，但数据不完整，无法单独还原。
- 请勿仅凭已收到的部分片进行还原，否则会丢失数据。
- 建议在 ClipVault 设置页查看备份状态（LastError），修复问题后重新触发一次完整备份。
- 若频繁失败，可尝试：改用增量备份减少单次数据量、检查 SMTP 配置与网络、或降低单封附件上限。

—— ClipVault`, startedAt.Format("2006-01-02 15:04:05"), ext, totalParts, sentParts, sentParts+1, cause, sentParts)
	htmlBody := renderEmailHTML(subject, strings.ReplaceAll(body, "\n", "<br>"), eb.Style, eb.AccentColor)
	msg := buildMIME(eb.From, eb.To, subject, body, htmlBody, nil)
	if err := sendSMTPMessage(eb, msg); err != nil {
		bs.logger.Error("backup_failure_notice_failed", err, map[string]any{"sentParts": sentParts, "totalParts": totalParts})
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

type emailBackupMessage struct {
	filename string
	data     []byte
}

func emailBackupFilename(ext string, now time.Time, index, total int) string {
	filename := fmt.Sprintf("clipvault-backup-%s.%s", now.Format("20060102-150405"), ext)
	if total > 1 {
		filename = fmt.Sprintf("clipvault-backup-%s-%dof%d.%s", now.Format("20060102-150405"), index+1, total, ext)
	}
	return filename
}

func buildEmailBackupMessage(cfg EmailBackupConfig, ext string, now time.Time, index, total int, attachment []byte) emailBackupMessage {
	filename := emailBackupFilename(ext, now, index, total)
	subject, body, htmlBody := renderEmailTemplate(cfg, filename, ext, now)
	if total > 1 {
		subject = fmt.Sprintf("[%d/%d] %s", index+1, total, subject)
	}
	contentType := "application/json"
	switch ext {
	case "cvexport":
		contentType = "application/octet-stream"
	case "xlsx":
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return emailBackupMessage{
		filename: filename,
		data:     buildMIMEWithAttachment(cfg.From, cfg.To, subject, body, htmlBody, filename, contentType, attachment),
	}
}

func emailBackupFileMessageSize(cfg EmailBackupConfig, ext string, now time.Time, index, total int, attachmentPath string) (int64, error) {
	var counter byteCounter
	if err := writeEmailBackupMessage(&counter, cfg, ext, now, index, total, attachmentPath); err != nil {
		return 0, err
	}
	return counter.n, nil
}

func writeEmailBackupMessage(dst io.Writer, cfg EmailBackupConfig, ext string, now time.Time, index, total int, attachmentPath string) error {
	filename := emailBackupFilename(ext, now, index, total)
	subject, body, htmlBody := renderEmailTemplate(cfg, filename, ext, now)
	if total > 1 {
		subject = fmt.Sprintf("[%d/%d] %s", index+1, total, subject)
	}
	contentType := "application/json"
	switch ext {
	case "cvexport":
		contentType = "application/octet-stream"
	case "xlsx":
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	attachment, err := os.Open(attachmentPath)
	if err != nil {
		return fmt.Errorf("打开邮件分片失败: %w", err)
	}
	defer attachment.Close()
	return writeMIMEAttachmentStream(dst, cfg.From, cfg.To, subject, body, htmlBody, filename, contentType, attachment)
}

func writeMIMEAttachmentStream(dst io.Writer, from, to, subject, body, htmlBody, filename, contentType string, attachment io.Reader) error {
	mixed := multipart.NewWriter(dst)
	if _, err := fmt.Fprintf(dst, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: %s\r\n\r\n",
		from, to, mime.QEncoding.Encode("UTF-8", subject), mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixed.Boundary()})); err != nil {
		return err
	}
	alternativeBody, boundary := buildAlternativeBody(body, htmlBody)
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": boundary}))
	part, err := mixed.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(alternativeBody); err != nil {
		return err
	}
	header = textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "base64")
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	part, err = mixed.CreatePart(header)
	if err != nil {
		return err
	}
	lineWriter := &streamBase64LineWriter{dst: part}
	encoder := base64.NewEncoder(base64.StdEncoding, lineWriter)
	if _, err := io.Copy(encoder, attachment); err != nil {
		_ = encoder.Close()
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	if err := lineWriter.Flush(); err != nil {
		return err
	}
	return mixed.Close()
}

type byteCounter struct{ n int64 }

func (w *byteCounter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

type streamBase64LineWriter struct {
	dst  io.Writer
	line []byte
}

func (w *streamBase64LineWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		space := base64MIMELineWidth - len(w.line)
		if space > len(p) {
			space = len(p)
		}
		w.line = append(w.line, p[:space]...)
		p = p[space:]
		if len(w.line) == base64MIMELineWidth {
			if err := w.flushLine(); err != nil {
				return total - len(p), err
			}
		}
	}
	return total, nil
}

func (w *streamBase64LineWriter) Flush() error {
	if len(w.line) == 0 {
		return nil
	}
	return w.flushLine()
}

func (w *streamBase64LineWriter) flushLine() error {
	if _, err := w.dst.Write(w.line); err != nil {
		return err
	}
	if _, err := io.WriteString(w.dst, "\r\n"); err != nil {
		return err
	}
	w.line = w.line[:0]
	return nil
}

func emailBackupMessageSize(cfg EmailBackupConfig, ext string, attachment []byte) int {
	// Use the longest permitted part label so adaptive splitting remains valid
	// when the final backup contains multiple messages.
	message := buildEmailBackupMessage(cfg, ext, time.Unix(0, 0), maxEmailParts-1, maxEmailParts, attachment)
	return len(message.data)
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

	// 流式 base64 编码 + 定宽折行，替代「先整体 EncodeToString 再逐行字符串拼接」。
	// 输出与旧实现逐字节等价：每满 base64MIMELineWidth 个字符输出一次 CRLF，
	// 末尾不足一行的残余同样以 CRLF 收尾；attachment.data 为空时不产生任何输出。
	var out bytes.Buffer
	lw := &base64LineWriter{buf: &out}
	encoder := base64.NewEncoder(base64.StdEncoding, lw)
	_, _ = encoder.Write(attachment.data)
	_ = encoder.Close()
	lw.flushLine()
	_, _ = part.Write(out.Bytes())
}

// base64MIMELineWidth 是 base64 附件正文的折行宽度（RFC 2045 建议 76 列）。
const base64MIMELineWidth = 76

// base64LineWriter 接收 base64 编码流，按 base64MIMELineWidth 折行写入缓冲区。
// 写入目标是 *bytes.Buffer（其 Write 不会失败），因此内部无需处理错误。
type base64LineWriter struct {
	buf *bytes.Buffer
	col int
}

func (lw *base64LineWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		n := base64MIMELineWidth - lw.col
		if n > len(p) {
			n = len(p)
		}
		lw.buf.Write(p[:n])
		lw.col += n
		p = p[n:]
		if lw.col == base64MIMELineWidth {
			lw.buf.WriteString("\r\n")
			lw.col = 0
		}
	}
	return total, nil
}

// flushLine 为不足整行的残余补上换行，与旧实现末行也带 CRLF 的行为保持一致。
func (lw *base64LineWriter) flushLine() {
	if lw.col > 0 {
		lw.buf.WriteString("\r\n")
		lw.col = 0
	}
}
