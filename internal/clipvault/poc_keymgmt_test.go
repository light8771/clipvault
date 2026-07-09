//go:build windows

package clipvault

// POC: DPAPI + 恢复密码 密钥管理验证
// 本文件仅在 POC 阶段使用，验证通过后会被删除或合并到正式代码中。
//
// 验证项（对应 REVIEWED 文档 POC 清单第 7-9 项）：
//  7. DPAPI CurrentUser 保存/读取 db_key
//  8. 恢复密码文件保存/读取 db_key（scrypt + AES-GCM）
//  9. 删除 DPAPI key 后可用 recovery 密码恢复

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/crypto/scrypt"
)

// ─── DPAPI 封装 ───

type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	dpapiKernel32      = syscall.NewLazyDLL("kernel32.dll")
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtect   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	procLocalFree      = dpapiKernel32.NewProc("LocalFree")
)

const cryptProtectUIForbidden = 0x1

// dpapiProtect 使用 Windows DPAPI (CurrentUser scope) 加密数据。
func dpapiProtect(data []byte) ([]byte, error) {
	var in dataBlob
	in.cbData = uint32(len(data))
	if len(data) > 0 {
		in.pbData = &data[0]
	}
	var out dataBlob
	r, _, err := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // description
		0, // optional entropy
		0, // reserved
		0, // prompt
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData failed: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))

	result := make([]byte, out.cbData)
	copy(result, (*[1 << 30]byte)(unsafe.Pointer(out.pbData))[:out.cbData])
	return result, nil
}

// dpapiUnprotect 使用 Windows DPAPI 解密数据。
func dpapiUnprotect(data []byte) ([]byte, error) {
	var in dataBlob
	in.cbData = uint32(len(data))
	if len(data) > 0 {
		in.pbData = &data[0]
	}
	var out dataBlob
	r, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // description
		0, // optional entropy
		0, // reserved
		0, // prompt
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData failed: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))

	result := make([]byte, out.cbData)
	copy(result, (*[1 << 30]byte)(unsafe.Pointer(out.pbData))[:out.cbData])
	return result, nil
}

// ─── 恢复密码封装 (scrypt + AES-256-GCM) ───

const (
	scryptN     = 32768
	scryptR     = 8
	scryptP     = 1
	wrappingLen = 32
	saltLen     = 32
)

// deriveWrappingKey 用 scrypt 从恢复密码派生 wrapping key。
func deriveWrappingKey(password, salt []byte) ([]byte, error) {
	return scrypt.Key(password, salt, scryptN, scryptR, scryptP, wrappingLen)
}

// encryptAESGCM 用 AES-256-GCM 加密 plaintext，返回 nonce 和 ciphertext。
func encryptAESGCM(key, plaintext []byte) (nonce, ciphertext []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("rand.Read nonce: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return nonce, ciphertext, nil
}

// decryptAESGCM 用 AES-256-GCM 解密 ciphertext。
func decryptAESGCM(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm.Open: %w", err)
	}
	return plaintext, nil
}

// ─── key fingerprint ───

// keyFingerprint 返回 db_key 的 SHA-256 前 8 字节作为短摘要。
func keyFingerprint(dbKey []byte) []byte {
	h := sha256.Sum256(dbKey)
	return h[:8]
}

// ─── DPAPI key 文件读写 (二进制 envelope) ───
//
// 格式: magic[6] + version[1] + fingerprint[8] + created_at[8] + blob_len[4] + blob[]

func writeDPAPIKeyFile(path string, dbKey []byte) error {
	protected, err := dpapiProtect(dbKey)
	if err != nil {
		return fmt.Errorf("dpapiProtect: %w", err)
	}
	fp := keyFingerprint(dbKey)
	now := time.Now().Unix()

	var buf []byte
	buf = append(buf, []byte("CVKEY1")...)
	buf = append(buf, 1) // version
	buf = append(buf, fp...)
	var ts [8]byte
	binary.LittleEndian.PutUint64(ts[:], uint64(now))
	buf = append(buf, ts[:]...)
	var bl [4]byte
	binary.LittleEndian.PutUint32(bl[:], uint32(len(protected)))
	buf = append(buf, bl[:]...)
	buf = append(buf, protected...)

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func readDPAPIKeyFile(path string) (dbKey, fingerprint []byte, err error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read key file: %w", err)
	}
	if len(buf) < 6+1+8+8+4 {
		return nil, nil, fmt.Errorf("key file too short")
	}
	if string(buf[:6]) != "CVKEY1" {
		return nil, nil, fmt.Errorf("bad magic: %q", buf[:6])
	}
	// version := buf[6]
	fingerprint = buf[7 : 7+8]
	// created_at := binary.LittleEndian.Uint64(buf[15:23])
	blobLen := binary.LittleEndian.Uint32(buf[23:27])
	if len(buf) < 27+int(blobLen) {
		return nil, nil, fmt.Errorf("key file truncated")
	}
	blob := buf[27 : 27+blobLen]
	dbKey, err = dpapiUnprotect(blob)
	if err != nil {
		return nil, nil, fmt.Errorf("dpapiUnprotect: %w", err)
	}
	return dbKey, fingerprint, nil
}

// ─── Recovery 文件读写 (二进制 envelope) ───
//
// 格式: magic[6] + version[1] + fingerprint[8] + salt[32] + nonce[12]
//
//	+ kdf_N[4] + kdf_r[4] + kdf_p[4] + data_len[4] + data[]

func writeRecoveryFile(path string, dbKey, password []byte) error {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("rand salt: %w", err)
	}
	wrappingKey, err := deriveWrappingKey(password, salt)
	if err != nil {
		return fmt.Errorf("deriveWrappingKey: %w", err)
	}
	nonce, ciphertext, err := encryptAESGCM(wrappingKey, dbKey)
	if err != nil {
		return fmt.Errorf("encryptAESGCM: %w", err)
	}
	fp := keyFingerprint(dbKey)

	var buf []byte
	buf = append(buf, []byte("CVREC1")...)
	buf = append(buf, 1) // version
	buf = append(buf, fp...)
	buf = append(buf, salt...)
	buf = append(buf, nonce...)
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], scryptN)
	buf = append(buf, n[:]...)
	binary.LittleEndian.PutUint32(n[:], scryptR)
	buf = append(buf, n[:]...)
	binary.LittleEndian.PutUint32(n[:], scryptP)
	buf = append(buf, n[:]...)
	binary.LittleEndian.PutUint32(n[:], uint32(len(ciphertext)))
	buf = append(buf, n[:]...)
	buf = append(buf, ciphertext...)

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func readRecoveryFile(path string, password []byte) (dbKey, fingerprint []byte, err error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read recovery file: %w", err)
	}
	if len(buf) < 6+1+8+32+12+12+4 {
		return nil, nil, fmt.Errorf("recovery file too short")
	}
	if string(buf[:6]) != "CVREC1" {
		return nil, nil, fmt.Errorf("bad magic: %q", buf[:6])
	}
	// version := buf[6]
	fingerprint = buf[7 : 7+8]
	salt := buf[15 : 15+32]
	nonce := buf[47 : 47+12]
	// kdf params: N, r, p at buf[59:71]
	nVal := binary.LittleEndian.Uint32(buf[59:63])
	rVal := binary.LittleEndian.Uint32(buf[63:67])
	pVal := binary.LittleEndian.Uint32(buf[67:71])
	dataLen := binary.LittleEndian.Uint32(buf[71:75])
	if len(buf) < 75+int(dataLen) {
		return nil, nil, fmt.Errorf("recovery file truncated")
	}
	ciphertext := buf[75 : 75+dataLen]

	wrappingKey, err := scrypt.Key(password, salt, int(nVal), int(rVal), int(pVal), wrappingLen)
	if err != nil {
		return nil, nil, fmt.Errorf("scrypt.Key: %w", err)
	}
	dbKey, err = decryptAESGCM(wrappingKey, nonce, ciphertext)
	if err != nil {
		return nil, nil, fmt.Errorf("decryptAESGCM: %w", err)
	}
	return dbKey, fingerprint, nil
}

// ─── 辅助函数 ───

func fileStatSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Size()
}

// ─── POC 测试 ───

// TestPOC7_DPAPI 验证 DPAPI CurrentUser 保存/读取 db_key。
func TestPOC7_DPAPI(t *testing.T) {
	// 生成 32 字节随机 db_key
	dbKey := make([]byte, 32)
	if _, err := rand.Read(dbKey); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	keyHex := hex.EncodeToString(dbKey)
	fp := keyFingerprint(dbKey)
	t.Logf("db_key (hex): %s", keyHex)
	t.Logf("fingerprint: %s", hex.EncodeToString(fp))

	// 1. DPAPI 加密
	protected, err := dpapiProtect(dbKey)
	if err != nil {
		t.Fatalf("dpapiProtect: %v", err)
	}
	t.Logf("[OK] DPAPI protect: %d bytes -> %d bytes", len(dbKey), len(protected))

	// 2. DPAPI 解密
	decrypted, err := dpapiUnprotect(protected)
	if err != nil {
		t.Fatalf("dpapiUnprotect: %v", err)
	}
	if !bytes.Equal(dbKey, decrypted) {
		t.Fatalf("DPAPI roundtrip mismatch")
	}
	t.Logf("[OK] DPAPI unprotect: roundtrip match")

	// 3. 写入 key 文件
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "clipvault.key")
	if err := writeDPAPIKeyFile(keyPath, dbKey); err != nil {
		t.Fatalf("writeDPAPIKeyFile: %v", err)
	}
	t.Logf("[OK] write key file: %s (%d bytes)", keyPath, fileStatSize(t, keyPath))

	// 4. 读取 key 文件
	readKey, readFP, err := readDPAPIKeyFile(keyPath)
	if err != nil {
		t.Fatalf("readDPAPIKeyFile: %v", err)
	}
	if !bytes.Equal(dbKey, readKey) {
		t.Fatalf("key file roundtrip mismatch")
	}
	if !bytes.Equal(fp, readFP) {
		t.Fatalf("fingerprint mismatch")
	}
	t.Logf("[OK] read key file: roundtrip match, fingerprint=%s", hex.EncodeToString(readFP))

	// 5. key 文件中不含 db_key 明文
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key file raw: %v", err)
	}
	if bytes.Contains(raw, dbKey) {
		t.Fatalf("key file contains plaintext db_key!")
	}
	t.Logf("[OK] key file does not contain plaintext db_key")
}

// TestPOC8_RecoveryPassword 验证恢复密码文件保存/读取 db_key。
func TestPOC8_RecoveryPassword(t *testing.T) {
	dbKey := make([]byte, 32)
	if _, err := rand.Read(dbKey); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	password := "MyRecoveryP@ssw0rd!"

	// 1. 写入 recovery 文件
	tmpDir := t.TempDir()
	recPath := filepath.Join(tmpDir, "clipvault.recovery")
	if err := writeRecoveryFile(recPath, dbKey, []byte(password)); err != nil {
		t.Fatalf("writeRecoveryFile: %v", err)
	}
	t.Logf("[OK] write recovery file: %s (%d bytes)", recPath, fileStatSize(t, recPath))

	// 2. 正确密码读取
	readKey, readFP, err := readRecoveryFile(recPath, []byte(password))
	if err != nil {
		t.Fatalf("readRecoveryFile: %v", err)
	}
	if !bytes.Equal(dbKey, readKey) {
		t.Fatalf("recovery roundtrip mismatch")
	}
	if !bytes.Equal(keyFingerprint(dbKey), readFP) {
		t.Fatalf("fingerprint mismatch")
	}
	t.Logf("[OK] correct password: roundtrip match, fingerprint=%s", hex.EncodeToString(readFP))

	// 3. 错误密码不能解密
	_, _, err = readRecoveryFile(recPath, []byte("WrongPassword123"))
	if err == nil {
		t.Fatalf("wrong password should fail!")
	}
	t.Logf("[OK] wrong password rejected: %v", err)

	// 4. recovery 文件中不含 db_key 明文
	raw, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("read recovery file raw: %v", err)
	}
	if bytes.Contains(raw, dbKey) {
		t.Fatalf("recovery file contains plaintext db_key!")
	}
	t.Logf("[OK] recovery file does not contain plaintext db_key")

	// 5. recovery 文件中不含密码明文
	if bytes.Contains(raw, []byte(password)) {
		t.Fatalf("recovery file contains plaintext password!")
	}
	t.Logf("[OK] recovery file does not contain plaintext password")
}

// TestPOC9_RecoveryAfterDPAPILoss 验证删除 DPAPI key 后可用 recovery 密码恢复。
// 这是整个密钥管理方案的核心验收场景。
func TestPOC9_RecoveryAfterDPAPILoss(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "clipvault.key")
	recPath := filepath.Join(tmpDir, "clipvault.recovery")
	dbPath := filepath.Join(tmpDir, "clipvault.duckdb")
	password := "RecoveryTestP@ss2026"

	// 1. 生成 db_key
	dbKey := make([]byte, 32)
	if _, err := rand.Read(dbKey); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	keyHex := hex.EncodeToString(dbKey)
	fp := keyFingerprint(dbKey)
	t.Logf("db_key (hex): %s, fingerprint: %s", keyHex, hex.EncodeToString(fp))

	// 2. 用 DPAPI 保护 db_key，写 key 文件
	if err := writeDPAPIKeyFile(keyPath, dbKey); err != nil {
		t.Fatalf("writeDPAPIKeyFile: %v", err)
	}
	t.Logf("[OK] DPAPI key file written")

	// 3. 设置恢复密码，写 recovery 文件
	if err := writeRecoveryFile(recPath, dbKey, []byte(password)); err != nil {
		t.Fatalf("writeRecoveryFile: %v", err)
	}
	t.Logf("[OK] recovery file written")

	// 4. 用 DPAPI key 正常打开 DuckDB 并写入数据
	db := openDuckDBEncrypted(t, dbPath, keyHex)
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS test (id INTEGER, text TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec("INSERT INTO test VALUES (1, 'secret clipboard content')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()
	t.Logf("[OK] DuckDB created with DPAPI key, data written")

	// 5. 模拟 DPAPI 失效：删除 key 文件
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove key file: %v", err)
	}
	t.Logf("[OK] DPAPI key file deleted (simulating DPAPI loss)")

	// 6. 用恢复密码恢复 db_key
	recoveredKey, recoveredFP, err := readRecoveryFile(recPath, []byte(password))
	if err != nil {
		t.Fatalf("readRecoveryFile: %v", err)
	}
	if !bytes.Equal(dbKey, recoveredKey) {
		t.Fatalf("recovered key mismatch")
	}
	if !bytes.Equal(fp, recoveredFP) {
		t.Fatalf("fingerprint mismatch after recovery")
	}
	t.Logf("[OK] recovered db_key via recovery password, fingerprint matches")

	// 7. 用恢复的 key 重新打开 DuckDB，验证数据完整
	recoveredKeyHex := hex.EncodeToString(recoveredKey)
	db2 := openDuckDBEncrypted(t, dbPath, recoveredKeyHex)
	defer db2.Close()
	var text string
	if err := db2.QueryRow("SELECT text FROM test WHERE id = 1").Scan(&text); err != nil {
		t.Fatalf("select after recovery: %v", err)
	}
	if text != "secret clipboard content" {
		t.Fatalf("data mismatch: got %q, want %q", text, "secret clipboard content")
	}
	t.Logf("[OK] DuckDB reopened with recovered key, data intact: %q", text)

	// 8. 重新生成 DPAPI key 文件（重新绑定当前 Windows 用户）
	if err := writeDPAPIKeyFile(keyPath, recoveredKey); err != nil {
		t.Fatalf("rewrite DPAPI key: %v", err)
	}
	t.Logf("[OK] DPAPI key file regenerated (rebound to current user)")

	// 9. 验证新的 DPAPI key 文件可以正常读取
	finalKey, finalFP, err := readDPAPIKeyFile(keyPath)
	if err != nil {
		t.Fatalf("readDPAPIKeyFile after recovery: %v", err)
	}
	if !bytes.Equal(dbKey, finalKey) {
		t.Fatalf("final key mismatch")
	}
	if !bytes.Equal(fp, finalFP) {
		t.Fatalf("final fingerprint mismatch")
	}
	t.Logf("[OK] DPAPI key file roundtrip after recovery, fingerprint=%s", hex.EncodeToString(finalFP))

	// 10. 错误恢复密码不能恢复
	_, _, err = readRecoveryFile(recPath, []byte("WrongPassword"))
	if err == nil {
		t.Fatalf("wrong recovery password should fail!")
	}
	t.Logf("[OK] wrong recovery password rejected")
}
