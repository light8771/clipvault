//go:build windows

package windows

import "unsafe"

// PointerFromWin32 converts an address whose lifetime is owned by Win32.
//
//go:nocheckptr
func PointerFromWin32(address uintptr) unsafe.Pointer { return unsafe.Pointer(address) }
