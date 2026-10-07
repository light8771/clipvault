//go:build windows

package clipvault

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

func restoreItemFromWeb(item StoredItem, dataDir string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := syscall.UTF16PtrFromString("STATIC")
	// A hidden message-only owner supports immediate data; destroying it after
	// CloseClipboard does not discard the system-owned format payloads.
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, 0, 0)
	if hwnd == 0 {
		return fmt.Errorf("create clipboard owner: %w", err)
	}
	defer procDestroyWindow.Call(hwnd)
	_, err = restoreItemToClipboard(hwnd, item, dataDir)
	return err
}
