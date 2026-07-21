package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"clipboard_ext/internal/licensing"
)

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
	passphrase, err := licensing.ReadPassphraseFile(*passphraseFile)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(*passphraseFile); os.IsNotExist(statErr) {
		fmt.Println("generated new passphrase file:", *passphraseFile)
	}
	publicKey, privateKey, err := licensing.GenerateKeyPair()
	if err != nil {
		return err
	}
	privateText, err := licensing.EncryptPrivateKey(privateKey, passphrase)
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
	passphrase, err := licensing.ReadPassphraseFile(*passphraseFile)
	if err != nil {
		return err
	}
	privateKey, err := licensing.DecryptPrivateKey(raw, passphrase)
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
