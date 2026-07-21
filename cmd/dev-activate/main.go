// Command dev-activate signs a perpetual license token for the current
// machine using a dev private key and writes it directly to the license
// receipt file. This lets dev-run.ps1 start clipvault in a fully activated
// state without manually pasting an activation code through the Web UI.
//
// Usage:
//
//	go run ./cmd/dev-activate \
//	  -key signing/cv-dev.ed25519 \
//	  -passphrase-file signing/key-passphrase-dev.txt \
//	  -receipt-path "$env:LOCALAPPDATA\ClipVault\license\clipvault.lic"
package main

import (
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"clipboard_ext/internal/licensing"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dev-activate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("dev-activate", flag.ContinueOnError)
	keyPath := fs.String("key", "", "encrypted private key path")
	passphraseFile := fs.String("passphrase-file", "", "passphrase file path")
	receiptPath := fs.String("receipt-path", "", "license receipt output path")
	keyID := fs.String("kid", licensing.BuiltinKeyID(), "signing key id")
	licenseID := fs.String("license-id", "CV-DEV-000001", "license id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*keyPath) == "" || strings.TrimSpace(*passphraseFile) == "" || strings.TrimSpace(*receiptPath) == "" {
		return errors.New("-key, -passphrase-file and -receipt-path are required")
	}

	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		return fmt.Errorf("read private key: %w", err)
	}
	passphrase, err := licensing.ReadPassphraseFile(*passphraseFile)
	if err != nil {
		return err
	}
	privateKey, err := licensing.DecryptPrivateKey(raw, passphrase)
	if err != nil {
		return err
	}

	machineID, err := licensing.MachineID()
	if err != nil {
		return fmt.Errorf("read machine id: %w", err)
	}

	claims := licensing.NewPerpetualClaims(*keyID, *licenseID, machineID)
	token, err := licensing.SignToken(claims, ed25519.PrivateKey(privateKey))
	if err != nil {
		return err
	}
	// Self-verify to catch key/keyid mismatches early.
	if _, err := licensing.ParseAndVerify(token, map[string]ed25519.PublicKey{
		claims.KeyID: ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey),
	}); err != nil {
		return fmt.Errorf("self-verify failed: %w", err)
	}

	if err := licensing.WriteReceipt(*receiptPath, token); err != nil {
		return fmt.Errorf("write receipt: %w", err)
	}

	fmt.Println("machine:", machineID)
	fmt.Println("license:", token)
	fmt.Println("receipt:", *receiptPath)
	return nil
}
