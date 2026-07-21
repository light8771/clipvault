//go:build windows

package licensing

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const receiptMagic = "CVLIC1"

type receipt struct {
	Version int    `json:"v"`
	Token   string `json:"token"`
}

type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	receiptKernel32    = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtect   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	procLocalFree      = receiptKernel32.NewProc("LocalFree")
)

func protectCurrentUser(data []byte) ([]byte, error) {
	var in dataBlob
	in.cbData = uint32(len(data))
	if len(data) > 0 {
		in.pbData = &data[0]
	}
	var out dataBlob
	r, _, err := procCryptProtect.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	result := make([]byte, out.cbData)
	copy(result, unsafe.Slice(out.pbData, out.cbData))
	return result, nil
}

func unprotectCurrentUser(data []byte) ([]byte, error) {
	var in dataBlob
	in.cbData = uint32(len(data))
	if len(data) > 0 {
		in.pbData = &data[0]
	}
	var out dataBlob
	r, _, err := procCryptUnprotect.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	result := make([]byte, out.cbData)
	copy(result, unsafe.Slice(out.pbData, out.cbData))
	return result, nil
}

func writeReceipt(path, token string) error {
	return WriteReceipt(path, token)
}

// WriteReceipt writes a license token to the receipt file at path, encrypted
// with the current user's DPAPI. Exported so tooling (e.g. dev-activate) can
// pre-populate the receipt without going through the activation UI.
func WriteReceipt(path, token string) error {
	plain, err := json.Marshal(receipt{Version: 1, Token: token})
	if err != nil {
		return err
	}
	protected, err := protectCurrentUser(plain)
	if err != nil {
		return err
	}
	buf := make([]byte, 0, len(receiptMagic)+1+4+len(protected))
	buf = append(buf, receiptMagic...)
	buf = append(buf, 1)
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(protected)))
	buf = append(buf, size[:]...)
	buf = append(buf, protected...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readReceipt(path string) (string, error) {
	buf, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", ErrLicenseMissing
	}
	if err != nil {
		return "", err
	}
	if len(buf) < len(receiptMagic)+1+4 || string(buf[:len(receiptMagic)]) != receiptMagic || buf[len(receiptMagic)] != 1 {
		return "", ErrLicenseCorrupt
	}
	offset := len(receiptMagic) + 1
	size := int(binary.LittleEndian.Uint32(buf[offset : offset+4]))
	offset += 4
	if size <= 0 || size > 64<<10 || len(buf) != offset+size {
		return "", ErrLicenseCorrupt
	}
	plain, err := unprotectCurrentUser(buf[offset:])
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrLicenseCorrupt, err)
	}
	var value receipt
	if err := json.Unmarshal(plain, &value); err != nil || value.Version != 1 || value.Token == "" {
		return "", ErrLicenseCorrupt
	}
	return value.Token, nil
}
