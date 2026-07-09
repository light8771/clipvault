//go:build windows

package clipvault

// POC: DPAPI + 恢复密码 密钥管理验证
// 验证项（对应 REVIEWED 文档 POC 清单第 7-9 项）：
//  7. DPAPI CurrentUser 保存/读取 db_key
//  8. 恢复密码文件保存/读取 db_key（scrypt + AES-GCM）
//  9. 删除 DPAPI key 后可用 recovery 密码恢复
//
// 注意：正式实现已提取到 dpapi_windows.go / keyfile.go / statefile.go，
// 本文件仅保留测试用例，调用正式 API。

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func fileStatSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Size()
}

// TestPOC7_DPAPI 验证 DPAPI CurrentUser 保存/读取 db_key。
func TestPOC7_DPAPI(t *testing.T) {
	dbKey := make([]byte, 32)
	if _, err := rand.Read(dbKey); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	keyHex := hex.EncodeToString(dbKey)
	fp := KeyFingerprint(dbKey)
	t.Logf("db_key (hex): %s", keyHex)
	t.Logf("fingerprint: %s", hex.EncodeToString(fp))

	// 1. DPAPI 加密
	protected, err := DPAPIProtect(dbKey)
	if err != nil {
		t.Fatalf("DPAPIProtect: %v", err)
	}
	t.Logf("[OK] DPAPI protect: %d bytes -> %d bytes", len(dbKey), len(protected))

	// 2. DPAPI 解密
	decrypted, err := DPAPIUnprotect(protected)
	if err != nil {
		t.Fatalf("DPAPIUnprotect: %v", err)
	}
	if !bytes.Equal(dbKey, decrypted) {
		t.Fatalf("DPAPI roundtrip mismatch")
	}
	t.Logf("[OK] DPAPI unprotect: roundtrip match")

	// 3. 写入 key 文件
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "clipvault.key")
	if err := WriteDPAPIKeyFile(keyPath, dbKey); err != nil {
		t.Fatalf("WriteDPAPIKeyFile: %v", err)
	}
	t.Logf("[OK] write key file: %s (%d bytes)", keyPath, fileStatSize(t, keyPath))

	// 4. 读取 key 文件
	readKey, readFP, err := ReadDPAPIKeyFile(keyPath)
	if err != nil {
		t.Fatalf("ReadDPAPIKeyFile: %v", err)
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
	if err := WriteRecoveryFile(recPath, dbKey, []byte(password)); err != nil {
		t.Fatalf("WriteRecoveryFile: %v", err)
	}
	t.Logf("[OK] write recovery file: %s (%d bytes)", recPath, fileStatSize(t, recPath))

	// 2. 正确密码读取
	readKey, readFP, err := ReadRecoveryFile(recPath, []byte(password))
	if err != nil {
		t.Fatalf("ReadRecoveryFile: %v", err)
	}
	if !bytes.Equal(dbKey, readKey) {
		t.Fatalf("recovery roundtrip mismatch")
	}
	if !bytes.Equal(KeyFingerprint(dbKey), readFP) {
		t.Fatalf("fingerprint mismatch")
	}
	t.Logf("[OK] correct password: roundtrip match, fingerprint=%s", hex.EncodeToString(readFP))

	// 3. 错误密码不能解密
	_, _, err = ReadRecoveryFile(recPath, []byte("WrongPassword123"))
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
	fp := KeyFingerprint(dbKey)
	t.Logf("db_key (hex): %s, fingerprint: %s", keyHex, hex.EncodeToString(fp))

	// 2. 用 DPAPI 保护 db_key，写 key 文件
	if err := WriteDPAPIKeyFile(keyPath, dbKey); err != nil {
		t.Fatalf("WriteDPAPIKeyFile: %v", err)
	}
	t.Logf("[OK] DPAPI key file written")

	// 3. 设置恢复密码，写 recovery 文件
	if err := WriteRecoveryFile(recPath, dbKey, []byte(password)); err != nil {
		t.Fatalf("WriteRecoveryFile: %v", err)
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
	recoveredKey, recoveredFP, err := ReadRecoveryFile(recPath, []byte(password))
	if err != nil {
		t.Fatalf("ReadRecoveryFile: %v", err)
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
	if err := WriteDPAPIKeyFile(keyPath, recoveredKey); err != nil {
		t.Fatalf("rewrite DPAPI key: %v", err)
	}
	t.Logf("[OK] DPAPI key file regenerated (rebound to current user)")

	// 9. 验证新的 DPAPI key 文件可以正常读取
	finalKey, finalFP, err := ReadDPAPIKeyFile(keyPath)
	if err != nil {
		t.Fatalf("ReadDPAPIKeyFile after recovery: %v", err)
	}
	if !bytes.Equal(dbKey, finalKey) {
		t.Fatalf("final key mismatch")
	}
	if !bytes.Equal(fp, finalFP) {
		t.Fatalf("final fingerprint mismatch")
	}
	t.Logf("[OK] DPAPI key file roundtrip after recovery, fingerprint=%s", hex.EncodeToString(finalFP))

	// 10. 错误恢复密码不能恢复
	_, _, err = ReadRecoveryFile(recPath, []byte("WrongPassword"))
	if err == nil {
		t.Fatalf("wrong recovery password should fail!")
	}
	t.Logf("[OK] wrong recovery password rejected")
}
