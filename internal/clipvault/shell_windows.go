//go:build windows

package clipvault

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modShell32 = syscall.NewLazyDLL("shell32.dll")
	modOle32   = syscall.NewLazyDLL("ole32.dll")

	procSHOpenFolderAndSelectItems = modShell32.NewProc("SHOpenFolderAndSelectItems")
	procILCreateFromPathW          = modShell32.NewProc("ILCreateFromPathW")
	procILFree                     = modShell32.NewProc("ILFree")
	procILClone                    = modShell32.NewProc("ILClone")
	procILRemoveLastID             = modShell32.NewProc("ILRemoveLastID")
	procILIsEqual                  = modShell32.NewProc("ILIsEqual")
	procILGetNext                  = modShell32.NewProc("ILGetNext")
	procILGetSize                  = modShell32.NewProc("ILGetSize")
	procCoInitialize               = modOle32.NewProc("CoInitialize")
	procCoUninitialize             = modOle32.NewProc("CoUninitialize")
)

// openFolderAndSelectItem uses the Windows Shell API SHOpenFolderAndSelectItems
// to open the containing folder of a file and select the file within it.
// This is more reliable than `explorer /select,path` which fails for paths
// containing certain characters (Chinese, spaces, etc.) and falls back to
// opening the Documents folder.
func openFolderAndSelectItem(path string) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("Shell API 异常: %v (路径: %s)", r, path)
		}
	}()

	// Normalize path separators to backslashes for Windows Shell API
	path = strings.ReplaceAll(path, "/", "\\")

	// Initialize COM (required by SHOpenFolderAndSelectItems)
	// S_FALSE (1) means COM was already initialized on this thread — that's fine
	procCoInitialize.Call(0)
	defer procCoUninitialize.Call()

	pPath, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("无法编码路径: %w", err)
	}

	// Create a PIDL from the full file path
	fullPidl, _, _ := procILCreateFromPathW.Call(uintptr(unsafe.Pointer(pPath)))
	if fullPidl == 0 {
		return fmt.Errorf("无法定位文件 (ILCreateFromPathW 返回空): %s", path)
	}
	defer procILFree.Call(fullPidl)

	// Clone the full PIDL, then remove the last item ID to get the parent folder PIDL
	parentPidl, _, _ := procILClone.Call(fullPidl)
	if parentPidl == 0 {
		return fmt.Errorf("无法克隆 PIDL (ILClone 返回空): %s", path)
	}
	defer procILFree.Call(parentPidl)

	r, _, _ := procILRemoveLastID.Call(parentPidl)
	if r == 0 {
		return fmt.Errorf("无法移除末尾 ID (ILRemoveLastID 失败): %s", path)
	}

	// The child PIDL is the portion of fullPidl after parentPidl's bytes.
	// We compute it by using ILGetNext to skip past the parent's items in the full PIDL.
	// ILGetNext returns a pointer into fullPidl's memory pointing to the child item.
	childPidl := computeChildPidl(parentPidl, fullPidl)
	if childPidl == 0 {
		return fmt.Errorf("无法计算子项 PIDL: %s", path)
	}

	// Open the parent folder and select the child item
	items := [1]uintptr{childPidl}
	hr, _, _ := procSHOpenFolderAndSelectItems.Call(
		parentPidl,
		uintptr(len(items)),
		uintptr(unsafe.Pointer(&items[0])),
		0,
	)
	if hr != 0 {
		return fmt.Errorf("打开文件夹失败 (SHOpenFolderAndSelectItems 错误码 0x%x): %s", hr, path)
	}
	return nil
}

// computeChildPidl calculates the child PIDL by finding where the parent PIDL
// ends within the full PIDL. Both PIDLs are contiguous ITEMIDLIST structures.
// The child PIDL is the remaining bytes after the parent's items in the full PIDL.
func computeChildPidl(parentPidl, fullPidl uintptr) uintptr {
	parentSize, _, _ := procILGetSize.Call(parentPidl)
	fullSize, _, _ := procILGetSize.Call(fullPidl)
	if parentSize == 0 || fullSize == 0 || parentSize >= fullSize {
		return 0
	}
	// The child PIDL starts at offset (parentSize - 2) in the full PIDL,
	// because ILGetSize includes the 2-byte terminating NULL of the parent,
	// but the child's first item ID begins right after the parent's last item ID
	// (before the parent's terminating NULL).
	offset := parentSize - 2 // subtract the parent's terminating NULL
	return fullPidl + uintptr(offset)
}
