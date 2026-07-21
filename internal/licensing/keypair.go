package licensing

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/scrypt"
)

// Private key file format constants. The private key file is a JSON envelope
// encrypted with scrypt-derived AES-256-GCM. Moved here from cmd/license-keygen
// so that dev-activate and other tooling can decrypt private keys without
// duplicating the logic.

const privateKeyFileVersion = 1

var privateKeyAAD = []byte("clipvault-license-private-key-v1")

type encryptedPrivateKeyFile struct {
	Version    int    `json:"v"`
	KDF        string `json:"kdf"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// GenerateKeyPair generates a fresh Ed25519 key pair.
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// EncryptPrivateKey encrypts an Ed25519 private key with the given passphrase
// using scrypt + AES-256-GCM. The output is a JSON envelope ready to write to
// a file.
func EncryptPrivateKey(privateKey, passphrase []byte) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 private key")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key(passphrase, salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	envelope := encryptedPrivateKeyFile{
		Version:    privateKeyFileVersion,
		KDF:        "scrypt-n32768-r8-p1",
		Salt:       base64.RawStdEncoding.EncodeToString(salt),
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(gcm.Seal(nil, nonce, privateKey, privateKeyAAD)),
	}
	encoded, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// DecryptPrivateKey decrypts an Ed25519 private key from the JSON envelope
// produced by EncryptPrivateKey.
func DecryptPrivateKey(raw, passphrase []byte) (ed25519.PrivateKey, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope encryptedPrivateKeyFile
	if err := decoder.Decode(&envelope); err != nil {
		return nil, errors.New("invalid encrypted private key file")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, errors.New("invalid encrypted private key file")
	}
	if envelope.Version != privateKeyFileVersion || envelope.KDF != "scrypt-n32768-r8-p1" {
		return nil, errors.New("unsupported encrypted private key format")
	}
	salt, err := base64.RawStdEncoding.DecodeString(envelope.Salt)
	if err != nil || len(salt) != 16 {
		return nil, errors.New("invalid encrypted private key file")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, errors.New("invalid encrypted private key file")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid encrypted private key file")
	}
	key, err := scrypt.Key(passphrase, salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid encrypted private key file")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, privateKeyAAD)
	if err != nil || len(plain) != ed25519.PrivateKeySize {
		return nil, errors.New("private key passphrase is incorrect or the key file is damaged")
	}
	return ed25519.PrivateKey(plain), nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("trailing JSON value")
	}
	return err
}

// ReadPassphraseFile reads a passphrase from the given path. If the file does
// not exist, a fresh 32-byte random passphrase is generated and written with
// 0600 permissions so callers can run generate-key without pre-creating the
// passphrase file.
func ReadPassphraseFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read passphrase file: %w", err)
		}
		generated := make([]byte, 32)
		if _, gErr := rand.Read(generated); gErr != nil {
			return nil, fmt.Errorf("generate passphrase: %w", gErr)
		}
		encoded := make([]byte, base64.RawStdEncoding.EncodedLen(len(generated)))
		base64.RawStdEncoding.Encode(encoded, generated)
		encoded = append(encoded, '\n')
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create passphrase dir: %w", err)
		}
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			return nil, fmt.Errorf("write passphrase file: %w", err)
		}
		return bytes.TrimSpace(encoded), nil
	}
	passphrase := bytes.TrimSpace(raw)
	if len(passphrase) < 16 {
		return nil, errors.New("private-key passphrase must be at least 16 bytes")
	}
	return passphrase, nil
}
