package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"clipboard_ext/internal/clipvault"
	"clipboard_ext/internal/licensing"
)

func main() {
	dataDir := flag.String("data-dir", "", "isolated data directory; defaults to a new temporary directory")
	listen := flag.String("listen", "127.0.0.1:18795", "preview listen address")
	lifetime := flag.Duration("lifetime", 0, "optional automatic graceful shutdown duration")
	flag.Parse()

	cleanup := func() {}
	if *dataDir == "" {
		var err error
		*dataDir, err = os.MkdirTemp("", "clipvault-ui-preview-")
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
	server := clipvault.NewSearchServer(manager, store, clipvault.NewRuntimeState(false), logger, clipvault.Environment{DataDir: *dataDir, LogDir: filepath.Join(*dataDir, "logs"), Source: "isolated-preview"}, nil, license)
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
