package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"clipboard_ext/internal/licensing"
	"golang.org/x/crypto/scrypt"
)

const privateKeyFileVersion = 1

var privateKeyAAD = []byte("clipvault-license-private-key-v1")

type encryptedPrivateKeyFile struct {
	Version    int    `json:"v"`
	KDF        string `json:"kdf"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "license-keygen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "generate-key":
		return generateKey(args[1:])
	case "issue":
		return issue(args[1:])
	default:
		return usageError()
	}
}

func generateKey(args []string) error {
	fs := flag.NewFlagSet("generate-key", flag.ContinueOnError)
	out := fs.String("out", "", "private key output path")
	passphraseFile := fs.String("passphrase-file", "", "path to a file containing the private-key passphrase")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*out) == "" || strings.TrimSpace(*passphraseFile) == "" {
		return errors.New("-out and -passphrase-file are required")
	}
	if _, err := os.Stat(*out); err == nil {
		return errors.New("refusing to overwrite an existing private key")
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(*out + ".pub"); err == nil {
		return errors.New("refusing to overwrite an existing public key")
	} else if !os.IsNotExist(err) {
		return err
	}
	passphrase, err := readPassphrase(*passphraseFile)
	if err != nil {
		return err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privateText, err := encryptPrivateKey(privateKey, passphrase)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(*out, privateText, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(*out+".pub", []byte(base64.RawStdEncoding.EncodeToString(publicKey)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Println("key pair created")
	fmt.Println("private:", *out)
	fmt.Println("public:", *out+".pub")
	return nil
}

func issue(args []string) error {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	keyPath := fs.String("key", "", "private key path")
	passphraseFile := fs.String("passphrase-file", "", "path to a file containing the private-key passphrase")
	keyID := fs.String("kid", licensing.BuiltinKeyID(), "signing key id")
	licenseID := fs.String("license-id", "", "license id")
	machineID := fs.String("machine", "", "full machine id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || *passphraseFile == "" || *licenseID == "" || *machineID == "" {
		return errors.New("-key, -passphrase-file, -license-id and -machine are required")
	}
	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	passphrase, err := readPassphrase(*passphraseFile)
	if err != nil {
		return err
	}
	privateKey, err := decryptPrivateKey(raw, passphrase)
	if err != nil {
		return err
	}
	claims := licensing.NewPerpetualClaims(*keyID, *licenseID, *machineID)
	token, err := licensing.SignToken(claims, ed25519.PrivateKey(privateKey))
	if err != nil {
		return err
	}
	verified, err := licensing.ParseAndVerify(token, map[string]ed25519.PublicKey{claims.KeyID: ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey)})
	if err != nil || verified != claims {
		return errors.New("generated token failed self-verification")
	}
	fmt.Println(token)
	return nil
}

func usageError() error {
	return errors.New("usage: license-keygen generate-key -out PATH -passphrase-file PATH | license-keygen issue -key PATH -passphrase-file PATH -license-id CV-... -machine HASH")
}

func readPassphrase(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read passphrase file: %w", err)
	}
	passphrase := bytes.TrimSpace(raw)
	if len(passphrase) < 16 {
		return nil, errors.New("private-key passphrase must be at least 16 bytes")
	}
	return passphrase, nil
}

func encryptPrivateKey(privateKey, passphrase []byte) ([]byte, error) {
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

func decryptPrivateKey(raw, passphrase []byte) (ed25519.PrivateKey, error) {
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
