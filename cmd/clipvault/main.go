package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"clipboard_ext/internal/clipvault"
)

//go:embed icon.png
var iconPNG []byte

func main() {
	// 注入图标数据（托盘图标与 exe 图标共用 winres/icon.png）
	clipvault.IconPNG = iconPNG

	if err := run(os.Args[1:]); err != nil {
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

func run(args []string) error {
	env, err := clipvault.NewEnvironment()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return clipvault.Run(env)
	}

	switch strings.ToLower(args[0]) {
	case "run":
		return clipvault.Run(env)
	case "search":
		return search(env, args[1:])
	case "data":
		fmt.Println(env.DataDir)
		return nil
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func search(env clipvault.Environment, args []string) error {
	store, err := clipvault.OpenStore(clipvault.StoreOptions{DataDir: env.DataDir})
	if err != nil {
		return err
	}
	defer store.Close()

	limit := 20
	queryParts := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n", "--limit":
			if i+1 >= len(args) {
				return errors.New("missing limit value")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid limit %q", args[i+1])
			}
			limit = n
			i++
		default:
			queryParts = append(queryParts, args[i])
		}
	}

	items, _, err := store.Search(context.Background(), clipvault.SearchOptions{Query: strings.Join(queryParts, " "), Limit: limit})
	if err != nil {
		return err
	}
	for _, item := range items {
		text := strings.ReplaceAll(item.Text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\n", "\\n")
		if len(text) > 120 {
			text = text[:120] + "..."
		}
		fmt.Printf("%d\t%s\t%s\t%d chars\t%s\n", item.ID, item.CreatedAt.Local().Format(time.DateTime), item.Kind, item.CharLen, text)
	}
	return nil
}

func printUsage() {
	fmt.Println(`clipvault - Windows clipboard history recorder

Usage:
  clipvault                Run in tray mode
  clipvault run            Run in tray mode
  clipvault search [q]     Search clipboard history in the console
  clipvault data           Print data directory`)
}
