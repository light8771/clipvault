package clipvault

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"
)

// .cvexport format:
// magic[8] + version[1] + reserved[3]
// + salt[32] + nonce[12]
// + kdf_N[4] + kdf_r[4] + kdf_p[4]
// + data_len[4] + encrypted_data[]
//
// The encrypted_data is gzip-compressed JSON, encrypted with AES-256-GCM
// using a key derived from the user's password via scrypt.

const cvexportMagic = "CVEXP001"

// cvexportMagicLen 是文件头 magic 的字节长度，直接由 magic 常量推导，
// 避免头部长度计算与偏移量中散落字面量 8。
const cvexportMagicLen = len(cvexportMagic)

// 解密端对文件头里的 KDF 参数设上限，防止恶意 .cvexport 通过超大 N/r/p
// 让 scrypt 尝试分配海量内存导致 OOM（这些值必须 >= 写入端常量 scryptN/R/P）。
const (
	maxDecryptScryptN = 1 << 20 // 1,048,576（写入端 32768 = 2^15）
	maxDecryptScryptR = 16      // 写入端 8
	maxDecryptScryptP = 8       // 写入端 1
)

// EncryptExport encrypts the given data (JSON bytes) with the given password
// and returns a .cvexport byte slice.
func EncryptExport(data []byte, password string) ([]byte, error) {
	// Gzip compress the data first
	var compressed bytes.Buffer
	gw := gzip.NewWriter(&compressed)
	if _, err := gw.Write(data); err != nil {
		return nil, fmt.Errorf("gzip write: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}

	// Derive key via scrypt
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("rand salt: %w", err)
	}
	wrappingKey, err := deriveWrappingKey([]byte(password), salt)
	if err != nil {
		return nil, fmt.Errorf("deriveWrappingKey: %w", err)
	}

	// Encrypt with AES-256-GCM
	nonce, ciphertext, err := encryptAESGCM(wrappingKey, compressed.Bytes())
	if err != nil {
		return nil, fmt.Errorf("encryptAESGCM: %w", err)
	}

	// Build the envelope
	var buf bytes.Buffer
	buf.WriteString(cvexportMagic)
	buf.WriteByte(1)           // version
	buf.Write([]byte{0, 0, 0}) // reserved
	buf.Write(salt)
	buf.Write(nonce)
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], scryptN)
	buf.Write(n[:])
	binary.LittleEndian.PutUint32(n[:], scryptR)
	buf.Write(n[:])
	binary.LittleEndian.PutUint32(n[:], scryptP)
	buf.Write(n[:])
	binary.LittleEndian.PutUint32(n[:], uint32(len(ciphertext)))
	buf.Write(n[:])
	buf.Write(ciphertext)

	return buf.Bytes(), nil
}

// DecryptExport decrypts a .cvexport byte slice with the given password
// and returns the original data (JSON bytes).
func DecryptExport(encData []byte, password string) ([]byte, error) {
	if len(encData) < cvexportMagicLen+1+3+saltLen+nonceLen+12+4 {
		return nil, fmt.Errorf("cvexport file too short")
	}
	if string(encData[:cvexportMagicLen]) != cvexportMagic {
		return nil, fmt.Errorf("bad magic: not a .cvexport file")
	}

	offset := cvexportMagicLen + 1 + 3 // magic + version + reserved
	salt := encData[offset : offset+saltLen]
	offset += saltLen
	nonce := encData[offset : offset+nonceLen]
	offset += nonceLen
	nVal := binary.LittleEndian.Uint32(encData[offset : offset+4])
	offset += 4
	rVal := binary.LittleEndian.Uint32(encData[offset : offset+4])
	offset += 4
	pVal := binary.LittleEndian.Uint32(encData[offset : offset+4])
	offset += 4
	dataLen := binary.LittleEndian.Uint32(encData[offset : offset+4])
	offset += 4
	if len(encData) < offset+int(dataLen) {
		return nil, fmt.Errorf("cvexport file truncated")
	}
	ciphertext := encData[offset : offset+int(dataLen)]

	// 校验不可信的 KDF 参数，超限直接拒绝，避免 scrypt 分配过量内存。
	if nVal == 0 || nVal > maxDecryptScryptN || (nVal&(nVal-1)) != 0 {
		return nil, fmt.Errorf("unsupported scrypt N: %d", nVal)
	}
	if rVal == 0 || rVal > maxDecryptScryptR {
		return nil, fmt.Errorf("unsupported scrypt r: %d", rVal)
	}
	if pVal == 0 || pVal > maxDecryptScryptP {
		return nil, fmt.Errorf("unsupported scrypt p: %d", pVal)
	}

	// Derive key
	wrappingKey, err := scrypt.Key([]byte(password), salt, int(nVal), int(rVal), int(pVal), wrappingLen)
	if err != nil {
		return nil, fmt.Errorf("scrypt: %w", err)
	}

	// Decrypt
	plaintext, err := decryptAESGCM(wrappingKey, nonce, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	// Gzip decompress
	gr, err := gzip.NewReader(bytes.NewReader(plaintext))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	decompressed, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("gzip read: %w", err)
	}

	return decompressed, nil
}

// IsCVExport checks if the given data starts with the .cvexport magic.
func IsCVExport(data []byte) bool {
	return len(data) >= cvexportMagicLen && string(data[:cvexportMagicLen]) == cvexportMagic
}
