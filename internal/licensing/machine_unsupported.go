//go:build !windows

package licensing

func MachineID() (string, error) { return "", ErrMachineReadFailed }

func DisplayMachineCode(machineID string) string { return machineID }
