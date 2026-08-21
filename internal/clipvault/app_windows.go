//go:build windows

package clipvault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"clipboard_ext/internal/licensing"
)

func Run(env Environment) error {
	logger, err := NewDailyLoggerWithConsole(env.LogDirectory(), os.Stdout)
	if err != nil {
		return err
	}
	defer logger.Close()

	instanceLock, err := AcquireInstanceLock()
	if err != nil {
		logger.Warn("single_instance_blocked", map[string]any{"error": err.Error()})
		return err
	}
	defer instanceLock.Close()
	preparedEnv, storageWarning, err := prepareEnvironmentForRun(env)
	if err != nil {
		logger.Error("storage_relocation_failed", err, nil)
		return err
	}
	env = preparedEnv
	if storageWarning != "" {
		logger.Warn("storage_relocation_cleanup_pending", map[string]any{"warning": storageWarning})
	}
	if switchedID, switchErr := applyPendingIsolatedDatabaseSwitch(env.DataDir); switchErr != nil {
		logger.Error("isolated_database_switch_failed", switchErr, nil)
		return fmt.Errorf("switch isolated database: %w", switchErr)
	} else if switchedID != "" {
		logger.Info("isolated_database_switched", map[string]any{"isolationId": switchedID})
	}

	// Determine startup mode via state file
	startupMode := determineStartupMode(env.DataDir)
	logger.Info("startup_mode", map[string]any{"mode": startupMode})

	store, err := OpenStore(StoreOptions{DataDir: env.DataDir})
	if err != nil {
		logger.Error("store_open_failed", err, map[string]any{"mode": startupMode})

		// Start recovery server for user to recover or create new database
		rs := NewRecoveryServer(env.DataDir)
		recURL, recErr := rs.Start()
		if recErr != nil {
			logger.Error("recovery_server_start_failed", recErr, nil)
			return &RecoveryError{Err: err, DataDir: env.DataDir}
		}
		safeRecoveryURL := strings.SplitN(recURL, "#", 2)[0]
		logger.Info("recovery_server_started", map[string]any{"url": safeRecoveryURL})
		fmt.Printf("Recovery page: %s\n", safeRecoveryURL)
		openBrowser(recURL)

		// Wait for recovery to complete
		<-rs.Done()
		rs.Close()

		if !rs.Success() {
			return &RecoveryError{Err: err, DataDir: env.DataDir}
		}

		// Retry opening the store after recovery
		store, err = OpenStore(StoreOptions{DataDir: env.DataDir})
		if err != nil {
			logger.Error("store_open_after_recovery_failed", err, nil)
			return &RecoveryError{Err: err, DataDir: env.DataDir}
		}
		logger.Info("store_opened_after_recovery", nil)
	}
	defer store.Close()

	// 恢复密码和二次密码必须在任何剪贴板监听、备份或管理服务启动前
	// 通过同一个门禁页面完成初始化。
	hasRecovery, err := hasRecoveryEnvelopeForKey(env.DataDir, store.DBKey())
	if err != nil {
		return err
	}

	var gate *StartupGate
	// gate 在首次设置或配置修复流程中才会赋值。在这里统一注册清理，
	// 保证赋值后的任何返回路径都会关闭门禁 HTTP 服务。
	defer func() {
		if gate != nil {
			gate.Close()
		}
	}()
	cfgMgr, err := NewConfigManager(store)
	if errors.Is(err, ErrConfigCorrupt) {
		logger.Error("config_corrupt", err, nil)
		if !hasRecovery {
			return fmt.Errorf("configuration is corrupt before security credentials were initialized: %w", err)
		}
		var gateErr error
		gate, gateErr = runStartupGate(store, env, startupGateRepairConfig, logger)
		if gateErr != nil {
			return gateErr
		}
		cfgMgr, err = NewConfigManager(store)
	}
	if err != nil {
		logger.Error("config_load_failed", err, nil)
		return err
	}
	cfg := cfgMgr.Get()
	hasSecondary := cfg.Security.SecondaryPasswordHash != ""
	if hasRecovery != hasSecondary {
		return errors.New("security credential initialization is incomplete")
	}
	if !hasRecovery {
		var gateErr error
		gate, gateErr = runStartupGate(store, env, startupGateSetCredentials, logger)
		if gateErr != nil {
			return gateErr
		}
		if err := cfgMgr.Reload(); err != nil {
			return fmt.Errorf("reload security configuration: %w", err)
		}
	}

	// Write/update state file after successful open and mandatory setup.
	if err := updateStateOnStartup(env.DataDir, store); err != nil {
		logger.Warn("state_update_failed", map[string]any{"error": err.Error()})
	}

	cfg = cfgMgr.Get()
	if err := WriteRecoveryAppearance(env.DataDir, cfg); err != nil {
		logger.Warn("recovery_appearance_sync_failed", map[string]any{"error": err.Error()})
	}
	cfgMgr.OnChange(func(newCfg Config) error {
		if err := WriteRecoveryAppearance(env.DataDir, newCfg); err != nil {
			logger.Warn("recovery_appearance_sync_failed", map[string]any{"error": err.Error()})
		}
		return nil
	})
	receiptPath := filepath.Join(env.DataDir, "license", "clipvault.lic")
	licenseMgr := licensing.NewManager(receiptPath, licensing.BuiltinTrustedKeys())
	licenseCtx := licenseMgr.Load()
	logger.Info("license_loaded", map[string]any{
		"status":      licenseCtx.Status,
		"enforced":    licenseCtx.Enforced,
		"licenseId":   licenseCtx.LicenseID,
		"machineCode": licenseCtx.MachineCode,
	})

	// 同步 autostart 注册表项
	if err := SetAutostart(cfg.Autostart.AppName, cfg.Autostart.Enabled); err != nil {
		logger.Warn("autostart_sync_failed", map[string]any{"error": err.Error()})
	}
	// 配置变更时同步 autostart
	cfgMgr.OnChange(func(newCfg Config) error {
		if err := SetAutostart(newCfg.Autostart.AppName, newCfg.Autostart.Enabled); err != nil {
			logger.Warn("autostart_sync_failed", map[string]any{"error": err.Error()})
		}
		return nil
	})

	state := NewRuntimeState(false)
	recorder := NewRecorder(cfgMgr, store, state, logger, env.ImagesDirectory())
	backup := NewBackupService(cfgMgr, store, logger, licenseMgr)
	defer backup.Stop()
	// 多语言服务：官方词条已由 OpenStore 幂等 Seed；这里构建运行时缓存、
	// 注册模板函数（{{T}}）并让任何配置路径的语言变更都驱动服务与原生 UI。
	i18nSvc, err := NewI18nService(store, cfgMgr, logger)
	if err != nil {
		logger.Error("i18n_service_init_failed", err, nil)
		return fmt.Errorf("init i18n service: %w", err)
	}
	SetTemplateI18nService(i18nSvc)
	defer SetTemplateI18nService(nil)

	// 外部只读访问（HTTP 数据接口 + MCP）共享同一个读取服务：
	// 敏感可见性、限流口径与查询语义在共享服务内统一强制。
	readSvc := NewExternalReadService(store, env)
	// 数据接口：独立监听的低优先级外部 API，随配置变更即时启停。
	dataAPI := NewDataAPIService(cfgMgr, readSvc, store, logger, licenseMgr, env)
	defer dataAPI.Close()
	cfgMgr.OnChange(func(newCfg Config) error {
		return dataAPI.ApplyConfig(newCfg)
	})
	if err := dataAPI.ApplyConfig(cfgMgr.Get()); err != nil {
		logger.Warn("data_api_start_failed", map[string]any{"error": err.Error()})
	}
	// MCP：独立监听的只读 MCP 服务（Streamable HTTP /mcp），随配置变更即时启停；
	// 绑定失败不影响主流程，错误暴露在 MCP 状态页。
	mcpSvc := NewMCPService(cfgMgr, readSvc, store, logger, licenseMgr, env)
	defer mcpSvc.Close()
	cfgMgr.OnChange(func(newCfg Config) error {
		return mcpSvc.ApplyConfig(newCfg)
	})
	if err := mcpSvc.ApplyConfig(cfgMgr.Get()); err != nil {
		logger.Warn("mcp_start_failed", map[string]any{"error": err.Error()})
	}
	searchServer := NewSearchServer(cfgMgr, store, state, logger, env, backup, licenseMgr)
	searchServer.SetDataAPIService(dataAPI)
	searchServer.SetMCPService(mcpSvc)
	searchServer.SetI18nService(i18nSvc)
	searchURL, err := searchServer.Start()
	if err != nil {
		logger.Error("search_server_start_failed", err, nil)
		return fmt.Errorf("start management server: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = searchServer.Close(ctx)
	}()
	fmt.Printf("Web UI (search+settings): %s\n", searchURL)

	// 首次启动门禁存在时，发布记录页地址供当前门禁页自动跳转。
	// 不再额外打开浏览器，避免成功后出现两个相同的记录页。
	if gate != nil {
		gate.SetRecordURL(searchURL)
	}

	backup.Start()
	runtime := NewWindowsRuntime(env, cfgMgr, logger, state, recorder, store, searchURL, licenseMgr)
	runtime.SetI18nService(i18nSvc)

	// 配置变更时重新注册全局快捷键
	cfgMgr.OnChange(func(newCfg Config) error {
		return runtime.ReregisterHotkey(newCfg)
	})

	logger.Info("app_started", map[string]any{
		"pid":       os.Getpid(),
		"dataDir":   env.DataDir,
		"searchURL": searchURL,
	})

	// 启动消息循环（在 goroutine 中运行，主线程等待重启或退出）
	errCh := make(chan error, 1)
	go func() {
		if err := runtime.Run(); err != nil && !errors.Is(err, errAppQuit) {
			errCh <- err
			return
		}
		errCh <- errAppQuit
	}()

	for {
		select {
		case err := <-errCh:
			logger.Info("app_stopped", nil)
			if errors.Is(err, errAppQuit) {
				return nil
			}
			logger.Error("app_run_failed", err, nil)
			return err
		case <-searchServer.RestartCh():
			logger.Info("restart_begin", nil)
			// 关闭旧 search server
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = searchServer.Close(ctx)
			cancel()
			// 创建新的 search server
			searchServer = NewSearchServer(cfgMgr, store, state, logger, env, backup, licenseMgr)
			searchServer.SetDataAPIService(dataAPI)
			searchServer.SetMCPService(mcpSvc)
			searchServer.SetI18nService(i18nSvc)
			searchURL, err = searchServer.Start()
			if err != nil {
				logger.Error("restart_search_failed", err, nil)
				return fmt.Errorf("restart management server: %w", err)
			}
			runtime.SetSearchURL(searchURL)
			logger.Info("restart_done", map[string]any{"searchURL": searchURL})
			fmt.Printf("Web UI restarted: %s\n", searchURL)
			shellExecute("open", searchURL, "")
		}
	}
}

func runStartupGate(store *Store, env Environment, mode startupGateMode, logger *DailyLogger) (*StartupGate, error) {
	gate := NewStartupGate(store, env.DataDir, mode)
	url, err := gate.Start()
	if err != nil {
		return nil, err
	}
	safeURL := strings.SplitN(url, "#", 2)[0]
	logger.Info("startup_gate_started", map[string]any{"mode": mode, "url": safeURL})
	fmt.Printf("Security setup page: %s\n", url)
	openBrowser(url)
	<-gate.Done()
	if !gate.Success() {
		gate.Close()
		return nil, fmt.Errorf("startup security setup was not completed")
	}
	logger.Info("startup_gate_completed", map[string]any{"mode": mode})
	// 不在此处关闭 gate：主流程在主服务就绪后调用 gate.SetRecordURL
	// 把记录页地址写入门禁，前端轮询拿到后自动跳转。SetRecordURL 内部
	// 会在 30 秒后自动关闭 gate HTTP 服务。
	return gate, nil
}

var errAppQuit = fmt.Errorf("app quit")

// RecoveryError indicates the store could not be opened (e.g. DPAPI failure)
// and the user may need to enter a recovery password.
type RecoveryError struct {
	Err     error
	DataDir string
}

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("store open failed (recovery needed): %v", e.Err)
}

func (e *RecoveryError) Unwrap() error { return e.Err }

// determineStartupMode checks the state file and key file to determine startup flow.
func determineStartupMode(dataDir string) string {
	stateExists := StateExists(dataDir)
	keyPath := activeDatabaseFile(dataDir, databaseKeyFileName)
	_, keyErr := os.Stat(keyPath)

	if !stateExists {
		if os.IsNotExist(keyErr) {
			return "first_install"
		}
		return "state_corrupt"
	}
	if os.IsNotExist(keyErr) {
		return "recovery_needed"
	}
	return "normal"
}

// updateStateOnStartup writes or updates the state file after a successful store open.
func updateStateOnStartup(dataDir string, store *Store) error {
	fp := KeyFingerprint(store.dbKey)

	if StateExists(dataDir) {
		state, err := ReadStateFile(dataDir)
		if err != nil {
			// State corrupt — rewrite
			state = AppState{}
		}
		// Engine and fingerprint describe the store that was actually opened;
		// never preserve stale or tampered metadata from the sidecar file.
		state.Engine = "duckdb"
		state.KeyFingerprint = fmt.Sprintf("%x", fp)
		if state.CreatedAt == "" {
			state.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		return WriteStateFile(dataDir, state)
	}

	// First time — create state file
	state := AppState{
		Engine:         "duckdb",
		KeyFingerprint: fmt.Sprintf("%x", fp),
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		LastOpenedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	return WriteStateFile(dataDir, state)
}
