package clipvault

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
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
	if len(encData) < 8+1+3+32+12+12+4 {
		return nil, fmt.Errorf("cvexport file too short")
	}
	if string(encData[:8]) != cvexportMagic {
		return nil, fmt.Errorf("bad magic: not a .cvexport file")
	}

	offset := 8 + 1 + 3 // magic + version + reserved
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

// EncryptExportJSON marshals the given value to JSON, then encrypts it.
func EncryptExportJSON(v any, password string) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("json marshal: %w", err)
	}
	return EncryptExport(data, password)
}

// DecryptExportJSON decrypts the .cvexport data and unmarshals JSON into v.
func DecryptExportJSON(encData []byte, password string, v any) error {
	data, err := DecryptExport(encData, password)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// IsCVExport checks if the given data starts with the .cvexport magic.
func IsCVExport(data []byte) bool {
	return len(data) >= 8 && string(data[:8]) == cvexportMagic
}
