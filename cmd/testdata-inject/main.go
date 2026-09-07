// Command testdata-inject 向指定数据目录注入测试剪贴板记录，
// 用于浏览器 E2E 测试（搜索/筛选/敏感/清理等）。与 cmd/isolated-preview 配套：
// 先注入数据，再以 -data-dir 启动隔离预览，即可得到带真实数据的测试环境。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	imgPath, imgBytes, imgHash, err := installManagedImage(dataDir, os.Args[2])
	if err != nil {
		panic(err)
	}

	now := time.Now().UTC().UnixMilli()
	day := int64(24 * 3600 * 1000)
	items := []clipvault.ClipboardItem{
		// 今天：普通文本
		{CapturedAtMS: now - 60_000, RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "ClipVault 事件优先测试记录 001",
			SourceExeName: "chrome.exe", SourceExePath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, SourceWindowTitle: "Google Chrome", SourceAttribution: "foreground"},
		{CapturedAtMS: now - 120_000, RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "会议纪要：明天上午 10 点评审 DuckDB 加密方案",
			SourceExeName: "WeChat.exe", SourceExePath: `C:\Program Files\Tencent\WeChat\WeChat.exe`, SourceWindowTitle: "微信", SourceAttribution: "foreground"},
		{CapturedAtMS: now - 180_000, RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "https://example.com/clipvault/docs",
			SourceExeName: "chrome.exe", SourceExePath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, SourceWindowTitle: "Chrome", SourceAttribution: "foreground"},
		{CapturedAtMS: now - 240_000, RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "curl https://127.0.0.1:18795/api/v1/items",
			SourceExeName: "WindowsTerminal.exe", SourceExePath: `C:\Program Files\WindowsApps\WindowsTerminal.exe`, SourceWindowTitle: "Windows Terminal", SourceAttribution: "foreground"},
		{CapturedAtMS: now - 300_000, RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "ClipVault 事件优先测试记录 001",
			SourceExeName: "chrome.exe", SourceExePath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, SourceWindowTitle: "Chrome", SourceAttribution: "foreground"},
		{CapturedAtMS: now - 360_000, RecordType: "event", EventCode: "virtual_file", CaptureStage: "classify", CaptureAttempts: 1,
			SourceExeName: "explorer.exe", SourceExePath: `C:\Windows\explorer.exe`, SourceWindowTitle: "文件资源管理器", SourceAttribution: "foreground"},
		{CapturedAtMS: now - 420_000, RecordType: "event", EventCode: "text_limit_exceeded", CaptureStage: "validate", CaptureAttempts: 1,
			SourceExeName: "notepad.exe", SourceExePath: `C:\Windows\System32\notepad.exe`, SourceWindowTitle: "记事本", SourceAttribution: "foreground"},
		// 图片（托管 PNG，路径格式与真实 recorder 产物保持一致）
		{CapturedAtMS: now - 480_000, RecordType: "content", Kind: "image", Format: "CLIPVAULT_IMAGE_FILE", Text: imgPath, ByteLen: imgBytes, ContentSHA256: imgHash, MetaJSON: fmt.Sprintf(`{"imagePath":%q,"imageBytes":%d}`, imgPath, imgBytes),
			SourceExeName: "Snipaste.exe", SourceExePath: `C:\Program Files\Snipaste\Snipaste.exe`, SourceWindowTitle: "Snipaste", SourceAttribution: "foreground"},
		// 文件拖放：普通文件 / Office / 压缩包
		{CapturedAtMS: now - 540_000, RecordType: "content", Kind: "files", Format: "CF_HDROP", Text: `C:\Users\test\Downloads\report.pdf`, MetaJSON: `{"paths":["C:\\Users\\test\\Downloads\\report.pdf"]}`,
			SourceExeName: "explorer.exe", SourceExePath: `C:\Windows\explorer.exe`, SourceWindowTitle: "文件资源管理器", SourceAttribution: "foreground"},
		{CapturedAtMS: now - 600_000, RecordType: "content", Kind: "office", Format: "CF_HDROP", Text: `C:\Users\test\Documents\方案.xlsx`, MetaJSON: `{"paths":["C:\\Users\\test\\Documents\\方案.xlsx"]}`,
			SourceExeName: "explorer.exe", SourceExePath: `C:\Windows\explorer.exe`, SourceWindowTitle: "文件资源管理器", SourceAttribution: "foreground"},
		{CapturedAtMS: now - 660_000, RecordType: "content", Kind: "archive", Format: "CF_HDROP", Text: `C:\Users\test\Downloads\backup.zip`, MetaJSON: `{"paths":["C:\\Users\\test\\Downloads\\backup.zip"]}`,
			SourceExeName: "explorer.exe", SourceExePath: `C:\Windows\explorer.exe`, SourceWindowTitle: "文件资源管理器", SourceAttribution: "foreground"},
		// 敏感记录（查看需二次认证）
		{CapturedAtMS: now - 720_000, RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "内部口令：ClipVault-Master-Key-2026", Sensitive: true,
			SourceExeName: "chrome.exe", SourceExePath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, SourceWindowTitle: "Chrome", SourceAttribution: "foreground"},
		// 昨天 / 上周（验证日期筛选与相对时间）
		{CapturedAtMS: now - day - 1_000, RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "昨天的剪贴板内容",
			SourceExeName: "code.exe", SourceExePath: `C:\Program Files\Microsoft VS Code\Code.exe`, SourceWindowTitle: "Visual Studio Code", SourceAttribution: "foreground"},
		{CapturedAtMS: now - 7*day - 1_000, RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "上周的备份策略讨论",
			SourceExeName: "WeChat.exe", SourceExePath: `C:\Program Files\Tencent\WeChat\WeChat.exe`, SourceWindowTitle: "微信", SourceAttribution: "foreground"},
	}

	for i, it := range items {
		if err := store.Save(context.Background(), it); err != nil {
			fmt.Fprintf(os.Stderr, "item %d failed: %v\n", i, err)
			os.Exit(1)
		}
	}
	fmt.Printf("injected %d items into %s\n", len(items), dataDir)
}

func installManagedImage(dataDir, sourcePath string) (string, int, string, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", 0, "", fmt.Errorf("open source image: %w", err)
	}
	defer source.Close()

	info, err := source.Stat()
	if err != nil {
		return "", 0, "", fmt.Errorf("stat source image: %w", err)
	}
	if info.IsDir() {
		return "", 0, "", fmt.Errorf("source image is a directory: %s", sourcePath)
	}

	relativePath := filepath.Join("images", "e2e", "managed.png")
	destinationPath := filepath.Join(dataDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		return "", 0, "", fmt.Errorf("create managed image directory: %w", err)
	}
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, "", fmt.Errorf("create managed image: %w", err)
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hasher), source)
	if err != nil {
		destination.Close()
		return "", 0, "", fmt.Errorf("copy managed image: %w", err)
	}
	if err := destination.Close(); err != nil {
		return "", 0, "", fmt.Errorf("close managed image: %w", err)
	}
	if written <= 0 || written > int64(^uint(0)>>1) {
		return "", 0, "", fmt.Errorf("managed image size %d is unsupported", written)
	}
	return filepath.ToSlash(relativePath), int(written), hex.EncodeToString(hasher.Sum(nil)), nil
}
