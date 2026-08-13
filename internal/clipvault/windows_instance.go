package clipvault

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

// InstanceLock prevents another ClipVault process from opening the store while
// the holder is running or relocating data.
type InstanceLock struct {
	handle uintptr
	once   sync.Once
}

// AcquireInstanceLock obtains ClipVault's process-wide store lock.
func AcquireInstanceLock() (*InstanceLock, error) {
	return acquireInstanceLock(singleInstanceName(environmentControlDir()))
}

func acquireInstanceLock(name string) (*InstanceLock, error) {
	mutexName, err := syscall.UTF16PtrFromString(`Global\` + name)
	if err != nil {
		return nil, fmt.Errorf("encode mutex name: %w", err)
	}
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(mutexName)))
	if handle == 0 {
		return nil, fmt.Errorf("CreateMutexW failed: %w", callErr)
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == syscall.Errno(183) {
		procCloseHandle.Call(handle)
		return nil, fmt.Errorf("another ClipVault instance is already running")
	}
	return &InstanceLock{handle: handle}, nil
}

// Close releases the process-wide store lock.
func (lock *InstanceLock) Close() {
	if lock == nil {
		return
	}
	lock.once.Do(func() {
		if lock.handle != 0 {
			procCloseHandle.Call(lock.handle)
			lock.handle = 0
		}
	})
}
