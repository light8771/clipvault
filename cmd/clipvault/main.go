package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"clipboard_ext/internal/clipvault"
	"clipboard_ext/internal/clipvault/bootstrap"
)

//go:embed icon.png
var iconPNG []byte

func main() {
	// 注入图标数据（托盘图标与 exe 图标共用 winres/icon.png）
	clipvault.IconPNG = iconPNG

	if err := bootstrap.Execute(os.Args[1:], os.Stdout); err != nil {
		// -H windowsgui 编译时没有控制台，Stderr 输出会丢失。
		// 启动错误写入当前用户的 LocalAppData，避免正式安装目录不可写。
		logPath := clipvault.StartupErrorLogFile()
		if mkErr := os.MkdirAll(filepath.Dir(logPath), 0o700); mkErr == nil {
			f, fErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if fErr == nil {
				fmt.Fprintf(f, "[%s] clipvault: %v\n", time.Now().Format(time.RFC3339), err)
				f.Close()
			}
		}
		_, _ = fmt.Fprintf(os.Stderr, "clipvault: %v\n", err)
		os.Exit(1)
	}
}
