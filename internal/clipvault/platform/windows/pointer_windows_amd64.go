//go:build windows && amd64

package windows

import "unsafe"

// PointerFromWin32 preserves an address supplied by the Win32 ABI. The tiny
// assembly bridge avoids asking Go's pointer checker to reinterpret ownership.
func PointerFromWin32(address uintptr) unsafe.Pointer
