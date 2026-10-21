//go:build windows

package clipvault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// 由文件系统返回真实路径，覆盖 8.3 别名、symlink 和 junction。
// 仅申请元数据权限，不读取文件正文，并允许其他进程正常读写和删除。
func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	systemPath := abs
	if !strings.HasPrefix(abs, `\\?\`) && !strings.HasPrefix(abs, `\\.\`) {
		if strings.HasPrefix(abs, `\\`) {
			systemPath = `\\?\UNC\` + abs[2:]
		} else {
			systemPath = `\\?\` + abs
		}
	}
	wide, err := windows.UTF16PtrFromString(systemPath)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(wide, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "解析真实路径", Path: path, Err: err}
	}
	defer windows.CloseHandle(handle)
	buffer, flags := make([]uint16, 512), uint32(0)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), flags)
		if errors.Is(err, windows.ERROR_PATH_NOT_FOUND) && flags == 0 {
			flags = 1 // 无盘符卷使用 VOLUME_NAME_GUID；不改变实际目录身份。
			continue
		}
		if err != nil {
			return "", &os.PathError{Op: "解析真实路径", Path: path, Err: err}
		}
		if n >= uint32(len(buffer)) {
			if n > 65535 {
				return "", errors.New("Windows 真实路径长度无效")
			}
			buffer = make([]uint16, n+1)
			continue
		}
		resolved := windows.UTF16ToString(buffer[:n])
		if strings.HasPrefix(resolved, `\\?\UNC\`) {
			resolved = `\\` + resolved[8:]
		} else if len(resolved) >= 7 && strings.HasPrefix(resolved, `\\?\`) && resolved[5] == ':' {
			resolved = resolved[4:]
		}
		return filepath.Clean(resolved), nil
	}
}
