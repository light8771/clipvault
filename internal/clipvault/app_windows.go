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

	if err := acquireSingleInstance(singleInstanceName()); err != nil {
		logger.Warn("single_instance_blocked", map[string]any{"error": err.Error()})
		return err
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

	// Write/update state file after successful open
	if err := updateStateOnStartup(env.DataDir, store); err != nil {
		logger.Warn("state_update_failed", map[string]any{"error": err.Error()})
	}

	cfgMgr, err := NewConfigManager(store)
	if err != nil {
		logger.Error("config_load_failed", err, nil)
		return err
	}

	cfg := cfgMgr.Get()
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
	recorder := NewRecorder(cfgMgr, store, state, logger, licenseMgr)
	backup := NewBackupService(cfgMgr, store, logger, licenseMgr)
	backup.Start()
	searchServer := NewSearchServer(cfgMgr, store, state, logger, env, backup, licenseMgr)
	searchURL, err := searchServer.Start()
	if err != nil {
		logger.Warn("search_server_start_failed", map[string]any{"error": err.Error()})
	}
	if searchURL != "" {
		fmt.Printf("Web 界面（搜索+设置）: %s\n", searchURL)
	}

	runtime := NewWindowsRuntime(env, cfgMgr, logger, state, recorder, store, searchURL, licenseMgr)

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
			searchURL, err = searchServer.Start()
			if err != nil {
				logger.Error("restart_search_failed", err, nil)
				continue
			}
			runtime.SearchURL = searchURL
			logger.Info("restart_done", map[string]any{"searchURL": searchURL})
			fmt.Printf("Web 界面已重启: %s\n", searchURL)
			shellExecute("open", searchURL, "")
		}
	}
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
	keyPath := filepath.Join(dataDir, "clipvault.key")
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
		// Update last_opened_at only
		state, err := ReadStateFile(dataDir)
		if err != nil {
			// State corrupt — rewrite
			state = AppState{}
		}
		state.LastOpenedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if state.KeyFingerprint == "" {
			state.KeyFingerprint = fmt.Sprintf("%x", fp)
		}
		if state.Engine == "" {
			state.Engine = "duckdb"
		}
		return WriteStateFile(dataDir, state)
	}

	// First time — create state file
	state := AppState{
		Engine:         "duckdb",
		KeyFingerprint: fmt.Sprintf("%x", fp),
		HasRecovery:    false,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		LastOpenedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	return WriteStateFile(dataDir, state)
}
