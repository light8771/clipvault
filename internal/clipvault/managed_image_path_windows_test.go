//go:build windows

package clipvault

import (
	"errors"
	"os"
	"os/exec"
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
	t.Run("long path", func(t *testing.T) {
		longDir := filepath.Join(longRoot, strings.Repeat("x", 180), strings.Repeat("y", 180))
		if err := os.MkdirAll(longDir, 0o700); err != nil {
			t.Fatal(err)
		}
		resolved, err := canonicalExistingPath(longDir)
		if err != nil {
			t.Fatalf("长路径解析失败：%v", err)
		}
		actual, err := os.Stat(resolved)
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.Stat(longDir)
		if err != nil || !os.SameFile(actual, original) {
			t.Fatalf("长路径解析改变了目录身份：%v", err)
		}
	})
	// 混用长、短路径时仍拒绝父子目录搬移，且不能把日志放进数据目录。
	t.Setenv("LOCALAPPDATA", t.TempDir())
	child := filepath.Join(longRoot, "nested")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dirs := range [][2]string{
		{longRoot, filepath.Join(shortRoot, "nested")},
		{filepath.Join(longRoot, "nested"), shortRoot},
	} {
		err := ScheduleDataDirectoryMove(Environment{DataDir: dirs[0], Source: "default"}, dirs[1])
		if err == nil || !strings.Contains(err.Error(), "parent or child") {
			t.Fatalf("长短路径别名绕过了搬移目录关系检查：%v", err)
		}
	}
	if err := ConfigureLogDirectory(Environment{DataDir: longRoot, LogDir: t.TempDir()}, filepath.Join(shortRoot, "nested")); err == nil {
		t.Fatal("日志目录不能通过短路径别名进入数据目录")
	}
	full, err := resolveManagedImagePath(Environment{DataDir: shortRoot}, "images/missing/image.png")
	if err != nil {
		t.Fatalf("短路径目录内的缺失图片被误判为路径越界：%v", err)
	}
	if _, err := os.Stat(full); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("缺失图片错误应保留 os.ErrNotExist：%v", err)
	}
}

func TestManagedDirectoriesRejectJunctionAncestors(t *testing.T) {
	shell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("系统未提供用于创建目录联接回归夹具的 PowerShell")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "images"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "images", "junction")
	command := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command",
		"$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path $env:CLIPVAULT_TEST_JUNCTION_LINK -Target $env:CLIPVAULT_TEST_JUNCTION_TARGET | Out-Null")
	command.Env = append(os.Environ(), "CLIPVAULT_TEST_JUNCTION_LINK="+link, "CLIPVAULT_TEST_JUNCTION_TARGET="+outside)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("创建本地目录联接夹具失败：%v (%s)", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	if err := os.Mkdir(filepath.Join(outside, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, resolve := range []func(string) (string, error){resolveExistingDirectory, resolveExistingLogDirectory} {
		if _, err := resolve(filepath.Join(link, "child")); err == nil {
			t.Fatal("数据或日志目录不能通过目录联接祖先校验")
		}
	}
	if _, err := resolveManagedImagePath(Environment{DataDir: root}, "images/junction/missing/image.png"); err == nil {
		t.Fatal("缺失图片不能通过目录联接逃出受管目录")
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
