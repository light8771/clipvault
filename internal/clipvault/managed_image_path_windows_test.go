//go:build windows

package clipvault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestManagedImageMissingFileUnderWindowsShortPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "long image directory for short path regression")
	if err := os.MkdirAll(filepath.Join(root, "images"), 0o700); err != nil {
		t.Fatal(err)
	}
	longRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	wideRoot, err := windows.UTF16PtrFromString(longRoot)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	n, err := windows.GetShortPathName(wideRoot, &buffer[0], uint32(len(buffer)))
	if err != nil || n >= uint32(len(buffer)) {
		t.Fatalf("GetShortPathName: length=%d err=%v", n, err)
	}
	shortRoot := windows.UTF16ToString(buffer[:n])
	if strings.EqualFold(filepath.Clean(shortRoot), filepath.Clean(longRoot)) {
		t.Skip("此卷未提供可用于回归的 Windows 短路径别名")
	}
	for _, resolve := range []struct {
		name string
		fn   func(string) (string, error)
	}{{"data", resolveExistingDirectory}, {"logs", resolveExistingLogDirectory}} {
		t.Run(resolve.name, func(t *testing.T) {
			resolved, err := resolve.fn(shortRoot)
			if err != nil {
				t.Fatalf("普通目录的短路径别名被误判为链接：%v", err)
			}
			actual, err := os.Stat(resolved)
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.Stat(longRoot)
			if err != nil || !os.SameFile(actual, original) {
				t.Fatalf("解析目录身份发生变化：%v", err)
			}
		})
	}
	full, err := resolveManagedImagePath(Environment{DataDir: shortRoot}, "images/missing/image.png")
	if err != nil {
		t.Fatalf("短路径目录内的缺失图片被误判为路径越界：%v", err)
	}
	if _, err := os.Stat(full); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("缺失图片错误应保留 os.ErrNotExist：%v", err)
	}
}

func TestManagedImageMissingFileThroughOutsideSymlinkIsRejected(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "images"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "images", "linked")); err != nil {
		t.Skipf("创建符号链接需要系统权限：%v", err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "existing"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, resolve := range []func(string) (string, error){resolveExistingDirectory, resolveExistingLogDirectory} {
		if _, err := resolve(filepath.Join(root, "images", "linked", "existing")); err == nil {
			t.Fatal("数据或日志目录不能通过祖先符号链接校验")
		}
	}
	if _, err := resolveManagedImagePath(Environment{DataDir: root}, "images/linked/missing/image.png"); err == nil {
		t.Fatal("指向受管目录外的符号链接不能因子文件不存在而通过校验")
	}
}
