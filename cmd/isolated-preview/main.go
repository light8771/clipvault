package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"clipboard_ext/internal/clipvault"
	clipsecurity "clipboard_ext/internal/clipvault/security"
	"clipboard_ext/internal/licensing"
)

// previewDirPrefix 与下方 MkdirTemp 的前缀保持一致。
const previewDirPrefix = "clipvault-ui-preview-"

// sweepStalePreviewDirs 清理上次强杀残留的临时数据目录：taskkill /F 走
// TerminateProcess，跳过所有 defer（含目录 cleanup），%TEMP% 会留下孤儿目录。
// 只清理修改时间超过 1 小时的目录；仍活跃实例的核心文件（DuckDB 库与日志）
// 被进程持有句柄，RemoveAll 会失败并跳过，因此不会误删正在运行的实例数据。
func sweepStalePreviewDirs(now time.Time) {
	const staleAge = time.Hour
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), previewDirPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < staleAge {
			continue
		}
		_ = os.RemoveAll(filepath.Join(os.TempDir(), e.Name()))
	}
}

func main() {
	dataDir := flag.String("data-dir", "", "isolated data directory; defaults to a new temporary directory")
	listen := flag.String("listen", "127.0.0.1:18795", "preview listen address")
	lifetime := flag.Duration("lifetime", 0, "optional automatic graceful shutdown duration")
	flag.Parse()

	sweepStalePreviewDirs(time.Now())

	cleanup := func() {}
	if *dataDir == "" {
		var err error
		*dataDir, err = os.MkdirTemp("", previewDirPrefix)
		if err != nil {
			panic(err)
		}
		cleanup = func() { _ = os.RemoveAll(*dataDir) }
	}
	defer cleanup()

	store, err := clipvault.OpenStore(clipvault.StoreOptions{DataDir: *dataDir})
	if err != nil {
		panic(err)
	}
	defer store.Close()
	manager, err := clipvault.NewConfigManager(store)
	if err != nil {
		panic(err)
	}
	cfg := manager.Get()
	cfg.Search.ListenAddr = *listen
	if password := os.Getenv("CLIPVAULT_PREVIEW_SECONDARY_PASSWORD"); password != "" {
		if err := clipsecurity.ValidatePassword(password); err != nil {
			panic(fmt.Errorf("invalid preview secondary password: %w", err))
		}
		hash, err := clipsecurity.HashPassword(password)
		if err != nil {
			panic(fmt.Errorf("hash preview secondary password: %w", err))
		}
		cfg.Security.SecondaryPasswordHash = hash
		cfg.Security.SecondaryAuthTimeout = 0
	}
	if err := manager.Update(cfg); err != nil {
		panic(err)
	}
	logger, err := clipvault.NewDailyLogger(filepath.Join(*dataDir, "logs"))
	if err != nil {
		panic(err)
	}
	defer logger.Close()
	license := licensing.NewManager(filepath.Join(*dataDir, "license", "clipvault.lic"), licensing.BuiltinTrustedKeys())
	license.Load()
	// 外部只读服务（HTTP 数据接口 + MCP）共享读取服务；随配置变更即时启停。
	env := clipvault.Environment{DataDir: *dataDir, LogDir: filepath.Join(*dataDir, "logs"), Source: "isolated-preview"}
	readSvc := clipvault.NewExternalReadService(store, env)
	dataAPI := clipvault.NewDataAPIService(manager, readSvc, store, logger, license, env)
	defer dataAPI.Close()
	manager.OnChange(func(newCfg clipvault.Config) error { return dataAPI.ApplyConfig(newCfg) })
	if err := dataAPI.ApplyConfig(manager.Get()); err != nil {
		logger.Warn("data_api_start_failed", map[string]any{"error": err.Error()})
	}
	mcpSvc := clipvault.NewMCPService(manager, readSvc, store, logger, license, env)
	defer mcpSvc.Close()
	manager.OnChange(func(newCfg clipvault.Config) error { return mcpSvc.ApplyConfig(newCfg) })
	if err := mcpSvc.ApplyConfig(manager.Get()); err != nil {
		logger.Warn("mcp_start_failed", map[string]any{"error": err.Error()})
	}
	// 定时备份服务（目录备份 + 邮件备份），随配置变更即时启停。
	backup := clipvault.NewBackupService(manager, store, logger, license)
	defer backup.Stop()
	server := clipvault.NewSearchServer(manager, store, clipvault.NewRuntimeState(false), logger, env, backup, license)
	server.SetDataAPIService(dataAPI)
	server.SetMCPService(mcpSvc)
	url, err := server.Start()
	if err != nil {
		panic(err)
	}
	fmt.Printf("PREVIEW_URL=%s\nPREVIEW_DATA_DIR=%s\n", url, *dataDir)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	if *lifetime > 0 {
		timer := time.NewTimer(*lifetime)
		select {
		case <-stop:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	} else {
		<-stop
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Close(ctx)
}
