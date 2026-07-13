//go:build windows

package licensing

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetVolumeInformationW = kernel32.NewProc("GetVolumeInformationW")
)

func MachineID() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", fmt.Errorf("%w: open MachineGuid: %v", ErrMachineReadFailed, err)
	}
	machineGUID, _, err := key.GetStringValue("MachineGuid")
	key.Close()
	if err != nil || strings.TrimSpace(machineGUID) == "" {
		return "", fmt.Errorf("%w: read MachineGuid", ErrMachineReadFailed)
	}

	systemDrive := strings.TrimSpace(os.Getenv("SystemDrive"))
	if systemDrive == "" {
		systemDrive = "C:"
	}
	root := strings.TrimRight(systemDrive, `\/`) + `\`
	rootPtr, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return "", fmt.Errorf("%w: invalid system drive", ErrMachineReadFailed)
	}
	var serial uint32
	ok, _, callErr := procGetVolumeInformationW.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		0, 0,
		uintptr(unsafe.Pointer(&serial)),
		0, 0, 0, 0,
	)
	if ok == 0 {
		return "", fmt.Errorf("%w: volume serial: %v", ErrMachineReadFailed, callErr)
	}

	canonical := "clipvault-machine-v1\x00\n" +
		"machine_guid=" + strings.ToLower(strings.TrimSpace(machineGUID)) + "\n" +
		fmt.Sprintf("system_volume_serial=%08X\n", serial)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

func DisplayMachineCode(machineID string) string {
	code := strings.ToUpper(strings.TrimSpace(machineID))
	if len(code) > 20 {
		code = code[:20]
	}
	groups := make([]string, 0, 5)
	for len(code) > 0 {
		n := 4
		if len(code) < n {
			n = len(code)
		}
		groups = append(groups, code[:n])
		code = code[n:]
	}
	return "CV-" + strings.Join(groups, "-")
}
