package clipvault

import (
	"clipboard_ext/internal/licensing"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
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
