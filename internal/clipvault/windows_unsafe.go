//go:build windows

package clipvault

import (
	"unsafe"

	platformwindows "clipboard_ext/internal/clipvault/platform/windows"
)

// pointerFromWin32 converts an address returned by a Win32 API or delivered in
// a window message. The memory is owned and kept alive by Windows for the
// duration documented at each call site.
//
//go:nocheckptr
func pointerFromWin32(address uintptr) unsafe.Pointer {
	return platformwindows.PointerFromWin32(address)
}
