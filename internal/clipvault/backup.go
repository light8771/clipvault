package clipvault

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
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
	cfg := bs.cfgMgr.Get()
	bs.Reload(cfg)
	bs.startupOnce.Do(func() {
		go bs.runStartupBackups(cfg)
	})
}

func (bs *BackupService) runStartupBackups(cfg Config) {
	runStartupBackups(cfg,
		func(c Config) error {
			err := bs.doDirBackup(c, false)
			if err != nil {
				bs.logger.Error("dir_backup_startup_failed", err, nil)
			}
			return err
		},
		func(c Config) error {
			err := bs.doEmailBackup(c, emailBackupModeAutomatic, false)
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

// Reload updates backup timers when the schedule state actually changes.
func (bs *BackupService) Reload(cfg Config) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

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
	if generation != bs.dirGeneration || !dirBackupScheduleReady(cfg.Backup.DirBackup) {
		return
	}
	bs.scheduleDirTimerLocked(cfg.Backup.DirBackup.Interval, generation)
}

func (bs *BackupService) rearmEmailTimer(cfg Config, generation uint64) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if generation != bs.emailGeneration || !emailBackupScheduleReady(cfg.Backup.EmailBackup) {
		return
	}
	bs.scheduleEmailTimerLocked(cfg.Backup.EmailBackup.Interval, generation)
}

func (bs *BackupService) isCurrentDirTimer(generation uint64) bool {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return generation == bs.dirGeneration
}

func (bs *BackupService) isCurrentEmailTimer(generation uint64) bool {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return generation == bs.emailGeneration
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

	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	db := cfg.Backup.DirBackup
	scope, autoFallback, err := resolveBackupScope(bs.store, "dir", db.Scope, db.IncrementalFullInterval, forceFull)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		bs.setDirStatus(makeBackupStatus(time.Now(), false, db.Scope, false, wm, db.IncrementalFullInterval, err.Error(), ""))
		return err
	}
	if db.Format == "xlsx" {
		scope.Mode = "full" // XLSX 不支持范围过滤，始终全量，水印按全量记录
	}
	data, ext, err := bs.exportBackupData(db.Format, db.Password, scope)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		bs.setDirStatus(makeBackupStatus(time.Now(), false, scope, autoFallback, wm, db.IncrementalFullInterval, err.Error(), ""))
		return err
	}

	now := time.Now()
	fullPath, err := writeBackupFile(db.Dir, now, ext, data)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "dir")
		bs.setDirStatus(makeBackupStatus(now, false, scope, autoFallback, wm, db.IncrementalFullInterval, err.Error(), ""))
		return err
	}

	wm := updateBackupWatermark(bs.store, "dir", scope)
	bs.setDirStatus(makeBackupStatus(now, true, scope, autoFallback, wm, db.IncrementalFullInterval, "", fullPath))
	bs.logger.Info("dir_backup_ok", map[string]any{"file": fullPath, "size": len(data), "mode": scope.Mode, "autoFallback": autoFallback})
	return nil
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

	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	eb := cfg.Backup.EmailBackup
	if problems := emailBackupSendErrors(eb); len(problems) > 0 {
		err := fmt.Errorf("邮件备份配置不完整: %s", strings.Join(problems, "、"))
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
		bs.setEmailStatus(makeBackupStatus(time.Now(), false, eb.Scope, false, wm, eb.IncrementalFullInterval, err.Error(), ""))
		return err
	}
	scope, autoFallback, err := resolveBackupScope(bs.store, "email", eb.Scope, eb.IncrementalFullInterval, forceFull)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
		bs.setEmailStatus(makeBackupStatus(time.Now(), false, eb.Scope, false, wm, eb.IncrementalFullInterval, err.Error(), ""))
		return err
	}
	if eb.Format == "xlsx" {
		scope.Mode = "full" // XLSX 不支持范围过滤，始终全量，水印按全量记录
	}
	parts, ext, err := bs.exportEmailParts(eb.Format, eb.Password, scope)
	if err != nil {
		wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
		bs.setEmailStatus(makeBackupStatus(time.Now(), false, scope, autoFallback, wm, eb.IncrementalFullInterval, err.Error(), ""))
		return err
	}

	now := time.Now()
	total := len(parts)

	contentType := "application/json"
	switch ext {
	case "cvexport":
		contentType = "application/octet-stream"
	case "xlsx":
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}

	lastFilename := ""
	for i, part := range parts {
		if len(part) > eb.MaxPartSize {
			err := fmt.Errorf("第 %d/%d 封附件 %d 字节超过阈值 %d，请缩小备份范围或减少单条记录大小", i+1, total, len(part), eb.MaxPartSize)
			wm, _ := bs.store.GetBackupWatermark(context.Background(), "email")
			bs.setEmailStatus(makeBackupStatus(now, false, scope, autoFallback, wm, eb.IncrementalFullInterval, err.Error(), ""))
			return err
		}
		filename := fmt.Sprintf("clipvault-backup-%s.%s", now.Format("20060102-150405"), ext)
		subject, body, htmlBody := renderEmailTemplate(eb, filename, ext, now)
		if total > 1 {
			filename = fmt.Sprintf("clipvault-backup-%s-%dof%d.%s", now.Format("20060102-150405"), i+1, total, ext)
			subject = fmt.Sprintf("[%d/%d] %s", i+1, total, subject)
		}
		msg := buildMIMEWithAttachment(eb.From, eb.To, subject, body, htmlBody, filename, contentType, part)
		if err := sendSMTPMessage(eb, msg); err != nil {
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
	wm := updateBackupWatermark(bs.store, "email", scope)
	statusFile := lastFilename
	if total > 1 {
		statusFile = fmt.Sprintf("%d part(s)", total)
	}
	bs.setEmailStatus(makeBackupStatus(now, true, scope, autoFallback, wm, eb.IncrementalFullInterval, "", statusFile))
	bs.logger.Info("email_backup_ok", map[string]any{"to": eb.To, "parts": total, "mode": scope.Mode, "autoFallback": autoFallback})
	return nil
}

func sendSMTPMessage(cfg EmailBackupConfig, msg []byte) error {
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
	if _, err := w.Write(msg); err != nil {
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
func (bs *BackupService) exportBackupData(format, password string, scope ExportScope) ([]byte, string, error) {
	switch format {
	case "xlsx":
		// XLSX 暂不支持 scope，全量导出
		data, err := bs.store.ExportXLSX(context.Background())
		if err != nil {
			return nil, "", fmt.Errorf("Excel 导出失败: %w", err)
		}
		return data, "xlsx", nil
	case "cvexport":
		if password == "" {
			return nil, "", fmt.Errorf("cvexport 格式需要加密密码")
		}
		var buf bytes.Buffer
		if err := bs.store.StreamExport(context.Background(), scope, &buf); err != nil {
			return nil, "", fmt.Errorf("导出数据失败: %w", err)
		}
		encData, err := EncryptExport(buf.Bytes(), password)
		if err != nil {
			return nil, "", fmt.Errorf("加密失败: %w", err)
		}
		return encData, "cvexport", nil
	case "json":
		var buf bytes.Buffer
		if err := bs.store.StreamExport(context.Background(), scope, &buf); err != nil {
			return nil, "", fmt.Errorf("导出数据失败: %w", err)
		}
		return buf.Bytes(), "json", nil
	default:
		return nil, "", fmt.Errorf("不支持的备份格式: %s", format)
	}
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
		return configured, false, nil
	}
	if configured.Mode == "range" {
		// range 按字符串比较 created_at（RFC3339 / YYYY-MM-DD 前缀字典序一致）
		if configured.From == "" || configured.To == "" {
			return configured, false, fmt.Errorf("range 备份需要配置起止时间（from/to）")
		}
		if configured.From >= configured.To {
			return configured, false, fmt.Errorf("range 备份起始时间 %q 必须早于结束时间 %q", configured.From, configured.To)
		}
		return configured, false, nil
	}
	if configured.Mode != "incremental" {
		return configured, false, nil
	}
	wm, err := store.GetBackupWatermark(context.Background(), kind)
	if err != nil || wm.LastItemID <= 0 {
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

// updateBackupWatermark records the current max id after a successful backup
// and returns the new watermark. full resets the incremental counter and
// updates LastFullAt; incremental increments the counter and preserves LastFullAt.
func updateBackupWatermark(store *Store, kind string, scope ExportScope) BackupWatermark {
	_, maxID, err := store.IDRange(context.Background(), scope)
	if err != nil || maxID <= 0 {
		return BackupWatermark{}
	}
	wm, _ := store.GetBackupWatermark(context.Background(), kind)
	now := time.Now().UTC().Format(time.RFC3339)
	newWM := BackupWatermark{LastItemID: maxID, LastSentAt: now}
	if scope.Mode == "incremental" {
		newWM.IncrementalCount = wm.IncrementalCount + 1
		newWM.LastFullAt = wm.LastFullAt
	} else {
		newWM.IncrementalCount = 0
		newWM.LastFullAt = now
	}
	store.SetBackupWatermark(context.Background(), kind, newWM)
	return newWM
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
// email). Each part is an independent ExportData over an id window, so it can be
// imported on its own. cvexport parts are individually encrypted.
func (bs *BackupService) exportEmailParts(format, password string, scope ExportScope) ([][]byte, string, error) {
	if format == "xlsx" {
		data, err := bs.store.ExportXLSX(context.Background())
		if err != nil {
			return nil, "", fmt.Errorf("Excel 导出失败: %w", err)
		}
		return [][]byte{data}, "xlsx", nil
	}
	minID, maxID, err := bs.store.IDRange(context.Background(), scope)
	if err != nil {
		return nil, "", fmt.Errorf("查询 id 范围失败: %w", err)
	}
	ext := "json"
	if format == "cvexport" {
		ext = "cvexport"
	}
	// No new data: still produce one self-contained empty export (same behavior
	// as directory backups, which always write a file), so the scheduled email
	// confirms the backup chain is alive. updateBackupWatermark leaves the
	// watermark untouched when the scope matches no rows.
	if minID <= 0 {
		var buf bytes.Buffer
		if err := bs.store.StreamExport(context.Background(), scope, &buf); err != nil {
			return nil, "", fmt.Errorf("导出空备份失败: %w", err)
		}
		raw := buf.Bytes()
		if format == "cvexport" {
			if password == "" {
				return nil, "", fmt.Errorf("cvexport 格式需要加密密码")
			}
			enc, err := EncryptExport(raw, password)
			if err != nil {
				return nil, "", fmt.Errorf("加密分片失败: %w", err)
			}
			raw = enc
		}
		return [][]byte{raw}, ext, nil
	}
	// Split by id windows of defaultItemsPerPart rows each.
	var parts [][]byte
	for start := minID; start <= maxID; start += defaultItemsPerPart {
		end := start + defaultItemsPerPart - 1
		if end > maxID {
			end = maxID
		}
		partScope := scope
		partScope.Mode = "incremental"
		partScope.SinceID = start - 1
		partScope.MaxID = end
		var buf bytes.Buffer
		if err := bs.store.StreamExport(context.Background(), partScope, &buf); err != nil {
			return nil, "", fmt.Errorf("导出分片失败: %w", err)
		}
		raw := buf.Bytes()
		if format == "cvexport" {
			if password == "" {
				return nil, "", fmt.Errorf("cvexport 格式需要加密密码")
			}
			enc, err := EncryptExport(raw, password)
			if err != nil {
				return nil, "", fmt.Errorf("加密分片失败: %w", err)
			}
			raw = enc
		}
		parts = append(parts, raw)
	}
	return parts, ext, nil
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
