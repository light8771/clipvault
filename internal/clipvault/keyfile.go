package clipvault

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/scrypt"
)

const (
	scryptN     = 32768
	scryptR     = 8
	scryptP     = 1
	wrappingLen = 32
	saltLen     = 32
	nonceLen    = 12
)

// KeyFingerprint 返回 db_key 的 SHA-256 前 8 字节作为短摘要。
func KeyFingerprint(dbKey []byte) []byte {
	h := sha256.Sum256(dbKey)
	return h[:8]
}

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

// atomicWriteFile atomically publishes a fully flushed, owner-only temporary
// file. A unique temporary name also makes concurrent writers independent.
func atomicWriteFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// ─── DPAPI key 文件 (二进制 envelope) ───
//
// 格式: magic[6] + version[1] + fingerprint[8] + created_at[8] + blob_len[4] + blob[]

const dpapiKeyMagic = "CVKEY1"
const keyEnvelopeVersion = 1

// WriteDPAPIKeyFile 用 DPAPI 保护 dbKey 后原子写入 key 文件。
func WriteDPAPIKeyFile(path string, dbKey []byte) error {
	protected, err := DPAPIProtect(dbKey)
	if err != nil {
		return fmt.Errorf("DPAPIProtect: %w", err)
	}
	fp := KeyFingerprint(dbKey)
	now := time.Now().Unix()

	var buf []byte
	buf = append(buf, []byte(dpapiKeyMagic)...)
	buf = append(buf, keyEnvelopeVersion)
	buf = append(buf, fp...)
	var ts [8]byte
	binary.LittleEndian.PutUint64(ts[:], uint64(now))
	buf = append(buf, ts[:]...)
	var bl [4]byte
	binary.LittleEndian.PutUint32(bl[:], uint32(len(protected)))
	buf = append(buf, bl[:]...)
	buf = append(buf, protected...)

	return atomicWriteFile(path, buf)
}

// ReadDPAPIKeyFile 读取 DPAPI key 文件并解密 dbKey。
func ReadDPAPIKeyFile(path string) (dbKey, fingerprint []byte, err error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read key file: %w", err)
	}
	if len(buf) < 6+1+8+8+4 {
		return nil, nil, fmt.Errorf("key file too short")
	}
	if string(buf[:6]) != dpapiKeyMagic {
		return nil, nil, fmt.Errorf("bad magic: %q", buf[:6])
	}
	if buf[6] != keyEnvelopeVersion {
		return nil, nil, fmt.Errorf("unsupported key file version %d", buf[6])
	}
	storedFingerprint := buf[7 : 7+8]
	blobLen := binary.LittleEndian.Uint32(buf[23:27])
	if blobLen == 0 || len(buf) != 27+int(blobLen) {
		return nil, nil, fmt.Errorf("invalid key file length")
	}
	blob := buf[27 : 27+blobLen]
	dbKey, err = DPAPIUnprotect(blob)
	if err != nil {
		return nil, nil, fmt.Errorf("DPAPIUnprotect: %w", err)
	}
	if len(dbKey) != wrappingLen {
		return nil, nil, fmt.Errorf("invalid database key length %d", len(dbKey))
	}
	fingerprint = KeyFingerprint(dbKey)
	if !bytes.Equal(storedFingerprint, fingerprint) {
		return nil, nil, fmt.Errorf("key fingerprint mismatch")
	}
	return dbKey, fingerprint, nil
}

// ─── Recovery 文件 (二进制 envelope) ───
//
// 格式: magic[6] + version[1] + fingerprint[8] + salt[32] + nonce[12]
//
//	+ kdf_N[4] + kdf_r[4] + kdf_p[4] + data_len[4] + data[]

const recoveryMagic = "CVREC1"

type recoveryEnvelope struct {
	fingerprint []byte
	salt        []byte
	nonce       []byte
	n           uint32
	r           uint32
	p           uint32
	ciphertext  []byte
}

// WriteRecoveryFile 用 scrypt + AES-256-GCM 加密 dbKey 后原子写入 recovery 文件。
func WriteRecoveryFile(path string, dbKey, password []byte) error {
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
	fp := KeyFingerprint(dbKey)

	var buf []byte
	buf = append(buf, []byte(recoveryMagic)...)
	buf = append(buf, keyEnvelopeVersion)
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

	return atomicWriteFile(path, buf)
}

// ReadRecoveryFile 用 password 解密 recovery 文件，恢复 dbKey。
func ReadRecoveryFile(path string, password []byte) (dbKey, fingerprint []byte, err error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read recovery file: %w", err)
	}
	envelope, err := parseRecoveryEnvelope(buf)
	if err != nil {
		return nil, nil, err
	}
	wrappingKey, err := scrypt.Key(password, envelope.salt, int(envelope.n), int(envelope.r), int(envelope.p), wrappingLen)
	if err != nil {
		return nil, nil, fmt.Errorf("scrypt.Key: %w", err)
	}
	dbKey, err = decryptAESGCM(wrappingKey, envelope.nonce, envelope.ciphertext)
	if err != nil {
		return nil, nil, fmt.Errorf("decryptAESGCM: %w", err)
	}
	if len(dbKey) != wrappingLen {
		return nil, nil, fmt.Errorf("invalid recovered database key length %d", len(dbKey))
	}
	fingerprint = KeyFingerprint(dbKey)
	if !bytes.Equal(envelope.fingerprint, fingerprint) {
		return nil, nil, fmt.Errorf("recovery fingerprint mismatch")
	}
	return dbKey, fingerprint, nil
}

func readRecoveryFileFingerprint(path string) ([]byte, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read recovery file: %w", err)
	}
	envelope, err := parseRecoveryEnvelope(buf)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), envelope.fingerprint...), nil
}

func parseRecoveryEnvelope(buf []byte) (recoveryEnvelope, error) {
	if len(buf) < 6+1+8+32+12+12+4 {
		return recoveryEnvelope{}, fmt.Errorf("recovery file too short")
	}
	if string(buf[:6]) != recoveryMagic {
		return recoveryEnvelope{}, fmt.Errorf("bad magic: %q", buf[:6])
	}
	if buf[6] != keyEnvelopeVersion {
		return recoveryEnvelope{}, fmt.Errorf("unsupported recovery file version %d", buf[6])
	}
	nVal := binary.LittleEndian.Uint32(buf[59:63])
	rVal := binary.LittleEndian.Uint32(buf[63:67])
	pVal := binary.LittleEndian.Uint32(buf[67:71])
	// v1 参数由文件提供，但不能在验证前原样交给 scrypt：损坏或恶意文件
	// 可用极端参数造成内存/CPU 拒绝服务。v1 只接受写入端的固定参数。
	if nVal != uint32(scryptN) || rVal != uint32(scryptR) || pVal != uint32(scryptP) {
		return recoveryEnvelope{}, fmt.Errorf("unsupported recovery KDF parameters")
	}
	dataLen := binary.LittleEndian.Uint32(buf[71:75])
	if dataLen != 48 || len(buf) != 75+int(dataLen) {
		return recoveryEnvelope{}, fmt.Errorf("recovery file truncated")
	}
	return recoveryEnvelope{
		fingerprint: buf[7:15],
		salt:        buf[15:47],
		nonce:       buf[47:59],
		n:           nVal,
		r:           rVal,
		p:           pVal,
		ciphertext:  buf[75 : 75+dataLen],
	}, nil
}
