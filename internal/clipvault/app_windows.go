//go:build windows

package clipvault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

func Run(env Environment) error {
	logger, err := NewDailyLoggerWithConsole(env.LogDirectory(), os.Stdout)
	if err != nil {
		return err
	}
	defer logger.Close()

	if err := acquireSingleInstance("ClipVaultClipboardRecorder"); err != nil {
		logger.Warn("single_instance_blocked", map[string]any{"error": err.Error()})
		return err
	}

	store, err := OpenStore(StoreOptions{DataDir: env.DataDir})
	if err != nil {
		logger.Error("store_open_failed", err, nil)
		return err
	}
	defer store.Close()

	cfgMgr, err := NewConfigManager(store)
	if err != nil {
		logger.Error("config_load_failed", err, nil)
		return err
	}

	cfg := cfgMgr.Get()

	// 同步 autostart 注册表项
	if err := SetAutostart(cfg.Autostart.AppName, cfg.Autostart.Enabled); err != nil {
		logger.Warn("autostart_sync_failed", map[string]any{"error": err.Error()})
	}
	// 配置变更时同步 autostart
	cfgMgr.OnChange(func(newCfg Config) {
		if err := SetAutostart(newCfg.Autostart.AppName, newCfg.Autostart.Enabled); err != nil {
			logger.Warn("autostart_sync_failed", map[string]any{"error": err.Error()})
		}
	})

	state := NewRuntimeState(false)
	recorder := NewRecorder(cfgMgr, store, state, logger)
	searchServer := NewSearchServer(cfgMgr, store, state, logger)
	searchURL, err := searchServer.Start()
	if err != nil {
		logger.Warn("search_server_start_failed", map[string]any{"error": err.Error()})
	}
	if searchURL != "" {
		fmt.Printf("Web 界面（搜索+设置）: %s\n", searchURL)
	}

	runtime := NewWindowsRuntime(env, cfgMgr, logger, state, recorder, searchURL)
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
			searchServer = NewSearchServer(cfgMgr, store, state, logger)
			searchURL, err = searchServer.Start()
			if err != nil {
				logger.Error("restart_search_failed", err, nil)
				continue
			}
			runtime.SearchURL = searchURL
			logger.Info("restart_done", map[string]any{"searchURL": searchURL})
			fmt.Printf("Web 界面已重启: %s\n", searchURL)
		}
	}
}

var errAppQuit = fmt.Errorf("app quit")
