// Command testdata-inject 向指定数据目录注入测试剪贴板记录，
// 用于浏览器 E2E 测试（搜索/筛选/敏感/清理等）。与 cmd/isolated-preview 配套：
// 先注入数据，再以 -data-dir 启动隔离预览，即可得到带真实数据的测试环境。
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"clipboard_ext/internal/clipvault"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: testdata-inject <data-dir> <image-png-path>")
		os.Exit(2)
	}
	dataDir := os.Args[1]
	store, err := clipvault.OpenStore(clipvault.StoreOptions{DataDir: dataDir})
	if err != nil {
		panic(err)
	}
	defer store.Close()

	imgPath, err := installManagedImage(dataDir, os.Args[2])
	if err != nil {
		panic(err)
	}

	now := time.Now().UTC().UnixMilli()
	day := int64(24 * 3600 * 1000)
	items := []clipvault.ClipboardItem{
		// 今天：普通文本
		{CapturedAtMS: now - 60_000, Kind: "text", Format: "CF_UNICODETEXT", Text: "ClipVault 事件优先测试记录 001",
			SourceExeName: "chrome.exe", SourceExePath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, SourceWindowTitle: "Google Chrome", SourceMethod: "foreground"},
		{CapturedAtMS: now - 120_000, Kind: "text", Format: "CF_UNICODETEXT", Text: "会议纪要：明天上午 10 点评审 DuckDB 加密方案",
			SourceExeName: "WeChat.exe", SourceExePath: `C:\Program Files\Tencent\WeChat\WeChat.exe`, SourceWindowTitle: "微信", SourceMethod: "foreground"},
		{CapturedAtMS: now - 180_000, Kind: "text", Format: "CF_UNICODETEXT", Text: "https://example.com/clipvault/docs",
			SourceExeName: "chrome.exe", SourceExePath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, SourceWindowTitle: "Chrome", SourceMethod: "foreground"},
		{CapturedAtMS: now - 240_000, Kind: "text", Format: "CF_UNICODETEXT", Text: "curl https://127.0.0.1:18795/api/v1/items",
			SourceExeName: "WindowsTerminal.exe", SourceExePath: `C:\Program Files\WindowsApps\WindowsTerminal.exe`, SourceWindowTitle: "Windows Terminal", SourceMethod: "foreground"},
		// 重复事件（同文本，is_duplicate=true）
		{CapturedAtMS: now - 300_000, Kind: "text", Format: "CF_UNICODETEXT", Text: "ClipVault 事件优先测试记录 001", IsDuplicate: true,
			SourceExeName: "chrome.exe", SourceExePath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, SourceWindowTitle: "Chrome", SourceMethod: "foreground"},
		// 无内容事件（原因：被覆盖 / 超长）
		{CapturedAtMS: now - 360_000, Kind: "other", Format: "CLIPVAULT_EVENT_ONLY", UnavailableReason: "superseded_before_read",
			SourceExeName: "explorer.exe", SourceExePath: `C:\Windows\explorer.exe`, SourceWindowTitle: "文件资源管理器", SourceMethod: "foreground"},
		{CapturedAtMS: now - 420_000, Kind: "other", Format: "CLIPVAULT_EVENT_ONLY", UnavailableReason: "text_limit_exceeded",
			SourceExeName: "notepad.exe", SourceExePath: `C:\Windows\System32\notepad.exe`, SourceWindowTitle: "记事本", SourceMethod: "foreground"},
		// 图片（托管 PNG，路径格式与真实 recorder 产物保持一致）
		{CapturedAtMS: now - 480_000, Kind: "image", Format: "CLIPVAULT_IMAGE_FILE", Text: imgPath, MetaJSON: fmt.Sprintf(`{"imagePath":%q}`, imgPath),
			SourceExeName: "Snipaste.exe", SourceExePath: `C:\Program Files\Snipaste\Snipaste.exe`, SourceWindowTitle: "Snipaste", SourceMethod: "foreground"},
		// 文件拖放：普通文件 / Office / 压缩包
		{CapturedAtMS: now - 540_000, Kind: "files", Format: "CF_HDROP", MetaJSON: `{"paths":["C:\\Users\\test\\Downloads\\report.pdf"]}`,
			SourceExeName: "explorer.exe", SourceExePath: `C:\Windows\explorer.exe`, SourceWindowTitle: "文件资源管理器", SourceMethod: "foreground"},
		{CapturedAtMS: now - 600_000, Kind: "office", Format: "CF_HDROP", MetaJSON: `{"paths":["C:\\Users\\test\\Documents\\方案.xlsx"]}`,
			SourceExeName: "explorer.exe", SourceExePath: `C:\Windows\explorer.exe`, SourceWindowTitle: "文件资源管理器", SourceMethod: "foreground"},
		{CapturedAtMS: now - 660_000, Kind: "archive", Format: "CF_HDROP", MetaJSON: `{"paths":["C:\\Users\\test\\Downloads\\backup.zip"]}`,
			SourceExeName: "explorer.exe", SourceExePath: `C:\Windows\explorer.exe`, SourceWindowTitle: "文件资源管理器", SourceMethod: "foreground"},
		// 敏感记录（查看需二次认证）
		{CapturedAtMS: now - 720_000, Kind: "text", Format: "CF_UNICODETEXT", Text: "内部口令：ClipVault-Master-Key-2026", Sensitive: true,
			SourceExeName: "chrome.exe", SourceExePath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, SourceWindowTitle: "Chrome", SourceMethod: "foreground"},
		// 昨天 / 上周（验证日期筛选与相对时间）
		{CapturedAtMS: now - day - 1_000, Kind: "text", Format: "CF_UNICODETEXT", Text: "昨天的剪贴板内容",
			SourceExeName: "code.exe", SourceExePath: `C:\Program Files\Microsoft VS Code\Code.exe`, SourceWindowTitle: "Visual Studio Code", SourceMethod: "foreground"},
		{CapturedAtMS: now - 7*day - 1_000, Kind: "text", Format: "CF_UNICODETEXT", Text: "上周的备份策略讨论",
			SourceExeName: "WeChat.exe", SourceExePath: `C:\Program Files\Tencent\WeChat\WeChat.exe`, SourceWindowTitle: "微信", SourceMethod: "foreground"},
	}

	for i, it := range items {
		if err := store.Save(context.Background(), it); err != nil {
			fmt.Fprintf(os.Stderr, "item %d failed: %v\n", i, err)
			os.Exit(1)
		}
	}
	fmt.Printf("injected %d items into %s\n", len(items), dataDir)
}

func installManagedImage(dataDir, sourcePath string) (string, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", fmt.Errorf("open source image: %w", err)
	}
	defer source.Close()

	info, err := source.Stat()
	if err != nil {
		return "", fmt.Errorf("stat source image: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("source image is a directory: %s", sourcePath)
	}

	relativePath := filepath.Join("images", "e2e", "managed.png")
	destinationPath := filepath.Join(dataDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		return "", fmt.Errorf("create managed image directory: %w", err)
	}
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("create managed image: %w", err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		destination.Close()
		return "", fmt.Errorf("copy managed image: %w", err)
	}
	if err := destination.Close(); err != nil {
		return "", fmt.Errorf("close managed image: %w", err)
	}
	return filepath.ToSlash(relativePath), nil
}
