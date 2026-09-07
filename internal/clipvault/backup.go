package clipvault

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"clipboard_ext/internal/licensing"
)

var (
	errBackupLicenseRequired = errors.New("api.error.backup_needs_license")
	errBackupStopped         = errors.New("api.error.backup_unavailable")
	// errBackupBusy 表示已有备份正在执行（手动触发与定时触发共用全局执行锁）。
	errBackupBusy = errors.New("backup.err_busy")
)

const (
	// 备份触发方式：定时 / 启动 / 手动。用于状态展示与历史记录。
	backupTriggerScheduled = "scheduled"
	backupTriggerStartup   = "startup"
	backupTriggerManual    = "manual"

	smtpOperationTimeout = 5 * time.Minute
	// maxEmailParts 是单次邮件备份分片总数的硬上限，防止数据量异常时
	// 连续发送海量邮件，触发 SMTP 限流、账号封禁或收件箱轰炸。
	maxEmailParts             = 60
	initialEmailPartProbeRows = 5000
)

// BackupStatus 记录最近一次备份执行的状态（含进行中标记）。
type BackupStatus struct {
	LastTime     time.Time // 最近一次执行结束时间
	LastOK       bool
	LastError    string
	BackupFile   string
	ActualMode   string // 实际执行的范围模式：full / incremental / range
	AutoFallback bool   // 配置增量但因兜底规则自动转了全量
	LastFullAt   string // 上次全量备份时间（RFC3339）
	Trigger      string // scheduled / startup / manual
	Skipped      bool   // 本次因无新数据而跳过（未产生文件/邮件）
	Running      bool   // 当前有备份正在执行
}

// BackupService manages scheduled directory and email backups.
type BackupService struct {
	cfgMgr  *ConfigManager
	store   *Store
	logger  *DailyLogger
	license *licensing.Manager

	mu              sync.Mutex
	execMu          sync.Mutex
	timerCallbackMu sync.Mutex
	dirTimer        *time.Timer
	dirGeneration   uint64
	dirState        backupTimerState
	emailTimer      *time.Timer
	emailGeneration uint64
	emailState      backupTimerState
	startupOnce     sync.Once
	startupWG       sync.WaitGroup
	stopped         bool
	stopDone        chan struct{}

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
	if bs.stopDone == nil {
		bs.stopDone = make(chan struct{})
	}
	stopDone := bs.stopDone
	if bs.stopped {
		bs.mu.Unlock()
		<-stopDone
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

	// A timer may already have entered its callback when Timer.Stop reports
	// false. Waiting on this gate prevents a callback that passed its initial
	// generation check from touching runtime dependencies after Stop returns.
	bs.timerCallbackMu.Lock()
	bs.timerCallbackMu.Unlock()
	bs.startupWG.Wait()
	// Acquiring execMu is a shutdown barrier: every backup operation holds it
	// for its full lifetime, so Stop does not return while one is still active.
	bs.execMu.Lock()
	bs.execMu.Unlock()
	close(stopDone)
}

func (bs *BackupService) runStartupBackups(cfg Config) {
	runStartupBackups(cfg,
		func(c Config) error {
			err := bs.runDirBackupLocked(c, backupTriggerStartup, false)
			if err != nil && !errors.Is(err, errBackupStopped) && !errors.Is(err, errBackupBusy) {
				bs.logger.Error("dir_backup_startup_failed", err, nil)
			}
			return err
		},
		func(c Config) error {
			err := bs.runEmailBackupLocked(c, backupTriggerStartup, false)
			if err != nil && !errors.Is(err, errBackupStopped) && !errors.Is(err, errBackupBusy) {
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
		bs.timerCallbackMu.Lock()
		defer bs.timerCallbackMu.Unlock()
		bs.dirBackupTick(generation)
	})
}

func (bs *BackupService) scheduleEmailTimerLocked(intervalHours int, generation uint64) {
	interval := time.Duration(intervalHours) * time.Hour
	bs.emailTimer = time.AfterFunc(interval, func() {
		bs.timerCallbackMu.Lock()
		defer bs.timerCallbackMu.Unlock()
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

// markDirRunning / markEmailRunning 在执行开始时把状态标记为"进行中"，
// 保留上一次的结果信息，供前端轮询展示进度。
func (bs *BackupService) markDirRunning(trigger string) {
	bs.statusMu.Lock()
	bs.dirStatus.Running = true
	bs.dirStatus.Trigger = trigger
	bs.statusMu.Unlock()
}

func (bs *BackupService) markEmailRunning(trigger string) {
	bs.statusMu.Lock()
	bs.emailStatus.Running = true
	bs.emailStatus.Trigger = trigger
	bs.statusMu.Unlock()
}

// recordBackupHistory 追加一条备份历史（成功/失败/跳过都记录）。
// 历史写入失败只记日志，不影响备份结果。
func (bs *BackupService) recordBackupHistory(kind, trigger, mode string, ok bool, runErr error, target, format string, parts, items int) {
	record := BackupHistory{
		Kind:    kind,
		Trigger: trigger,
		Mode:    mode,
		OK:      ok,
		Target:  target,
		Format:  format,
		Parts:   parts,
		Items:   items,
	}
	if runErr != nil {
		record.Error = runErr.Error()
	}
	if err := bs.store.AddBackupHistory(context.Background(), record); err != nil {
		bs.logger.Error("backup_history_record_failed", err, map[string]any{"kind": kind})
	}
}

// runDirBackupLocked 尝试获取执行锁并同步执行目录备份。
// 已有备份在执行时返回 errBackupBusy（调用方决定跳过还是报错）。
func (bs *BackupService) runDirBackupLocked(cfg Config, trigger string, forceFull bool) error {
	if err := bs.beginBackupExecution(); err != nil {
		return err
	}
	defer bs.execMu.Unlock()
	return bs.doDirBackup(cfg, trigger, forceFull)
}

// runEmailBackupLocked 同 runDirBackupLocked，针对邮件备份。
func (bs *BackupService) runEmailBackupLocked(cfg Config, trigger string, forceFull bool) error {
	if err := bs.beginBackupExecution(); err != nil {
		return err
	}
	defer bs.execMu.Unlock()
	return bs.doEmailBackup(cfg, trigger, forceFull)
}

// beginBackupExecution makes the stopped check and execution-lock acquisition
// one operation with respect to Stop. Once it succeeds, Stop must wait for the
// caller to release execMu; once Stop marks the service stopped, no caller can
// report that a new backup was accepted.
func (bs *BackupService) beginBackupExecution() error {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.stopped {
		return errBackupStopped
	}
	if !bs.execMu.TryLock() {
		return errBackupBusy
	}
	return nil
}

// RunDirBackup 异步启动一次目录备份（手动触发）。
// license、配置完整性、执行占用都在同步阶段校验并返回明确错误；
// 校验通过后备份在后台执行，进度与结果通过 DirStatus / 历史记录观察。
// forceFull=true 强制全量，并把本次全量设为增量备份的新起点。
func (bs *BackupService) RunDirBackup(forceFull bool) error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	cfg := bs.cfgMgr.Get()
	if msg := validateDirBackupEnabled(cfg.Backup.DirBackup); msg != "" {
		return fmt.Errorf("%s: %s", templateT("backup.incomplete_dir_prefix", nil), msg)
	}
	if err := bs.beginBackupExecution(); err != nil {
		return err
	}
	go func() {
		defer bs.execMu.Unlock()
		err := bs.doDirBackup(cfg, backupTriggerManual, forceFull)
		switch {
		case err == nil:
			bs.logger.Info("dir_backup_manual_ok", nil)
		case errors.Is(err, errBackupStopped):
		default:
			bs.logger.Error("dir_backup_manual_failed", err, nil)
		}
	}()
	return nil
}

// RunEmailBackup 异步启动一次邮件备份（手动触发）。语义同 RunDirBackup。
func (bs *BackupService) RunEmailBackup(forceFull bool) error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	cfg := bs.cfgMgr.Get()
	if msg := validateEmailBackupEnabled(cfg.Backup.EmailBackup); msg != "" {
		return fmt.Errorf("%s: %s", templateT("backup.incomplete_email_prefix", nil), msg)
	}
	if err := bs.beginBackupExecution(); err != nil {
		return err
	}
	go func() {
		defer bs.execMu.Unlock()
		err := bs.doEmailBackup(cfg, backupTriggerManual, forceFull)
		switch {
		case err == nil:
			bs.logger.Info("email_backup_manual_ok", nil)
		case errors.Is(err, errBackupStopped):
		default:
			bs.logger.Error("email_backup_manual_failed", err, nil)
		}
	}()
	return nil
}

// SendTestEmail 发送一封测试邮件（不包含备份数据）。
func (bs *BackupService) SendTestEmail() error {
	if bs.license != nil && !bs.license.CanUsePaidFeatures() {
		return errBackupLicenseRequired
	}
	cfg := bs.cfgMgr.Get()
	eb := cfg.Backup.EmailBackup
	if problems := smtpDeliveryErrors(eb); len(problems) > 0 {
		return fmt.Errorf("%s: %s", templateT("backup.smtp_incomplete", nil), translateBackupMissingList(problems))
	}
	if errors := emailTemplateErrors(eb); len(errors) > 0 {
		return fmt.Errorf("%s: %s", templateT("backup.template_incomplete", nil), translateBackupMissingList(errors))
	}
	subject, body, htmlBody := renderEmailTemplate(eb, templateT("backup.test_subject", nil), templateT("backup.test_kind", nil), time.Now())
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
	if err := bs.runDirBackupLocked(cfg, backupTriggerScheduled, false); err != nil {
		if errors.Is(err, errBackupBusy) {
			bs.logger.Info("dir_backup_skipped_busy", nil)
		} else {
			bs.logger.Error("dir_backup_failed", err, nil)
		}
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
	if err := bs.runEmailBackupLocked(cfg, backupTriggerScheduled, false); err != nil {
		if errors.Is(err, errBackupBusy) {
			bs.logger.Info("email_backup_skipped_busy", nil)
		} else {
			bs.logger.Error("email_backup_failed", err, nil)
		}
	}
	bs.rearmEmailTimer(cfg, generation)
}
