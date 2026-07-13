package licensing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestTokenRoundtrip(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	machineID := strings.Repeat("a", 64)
	claims := NewPerpetualClaims("cv-main-2026", "CV-2026-000001", machineID)
	token, err := SignToken(claims, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseAndVerify(token, map[string]ed25519.PublicKey{"cv-main-2026": publicKey})
	if err != nil {
		t.Fatal(err)
	}
	if got != claims {
		t.Fatalf("claims mismatch: got %+v want %+v", got, claims)
	}
}

func TestTokenRejectsTamperingAndWrongMachineFormat(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	claims := NewPerpetualClaims("cv-main-2026", "CV-2026-000001", strings.Repeat("b", 64))
	token, err := SignToken(claims, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	signature := []byte(parts[2])
	if signature[0] == 'A' {
		signature[0] = 'B'
	} else {
		signature[0] = 'A'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(signature)
	if _, err := ParseAndVerify(tampered, map[string]ed25519.PublicKey{"cv-main-2026": publicKey}); err == nil {
		t.Fatal("tampered token verified")
	}
	claims.MachineID = "ABC"
	if _, err := SignToken(claims, privateKey); !errors.Is(err, ErrInvalidMachineID) {
		t.Fatalf("got %v, want ErrInvalidMachineID", err)
	}
}

func TestTokenRejectsNonCanonicalBase64URLSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	claims := NewPerpetualClaims("cv-main-2026", "CV-2026-000003", strings.Repeat("d", 64))
	token, err := SignToken(claims, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(token, ".")
	original, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	mutated := ""
	for _, candidate := range alphabet {
		if byte(candidate) == parts[2][len(parts[2])-1] {
			continue
		}
		variant := parts[2][:len(parts[2])-1] + string(candidate)
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(variant)
		if decodeErr == nil && string(decoded) == string(original) {
			mutated = variant
			break
		}
	}
	if mutated == "" {
		t.Fatal("failed to construct a non-canonical equivalent signature")
	}
	tampered := parts[0] + "." + parts[1] + "." + mutated
	if _, err := ParseAndVerify(tampered, map[string]ed25519.PublicKey{"cv-main-2026": publicKey}); !errors.Is(err, ErrBadTokenFormat) {
		t.Fatalf("got %v, want ErrBadTokenFormat", err)
	}
}

func TestManagerActivateAndReloadReceipt(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	machineID := strings.Repeat("c", 64)
	claims := NewPerpetualClaims("cv-main-2026", "CV-2026-000002", machineID)
	token, err := SignToken(claims, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/clipvault.lic"
	manager := NewManager(path, map[string]ed25519.PublicKey{"cv-main-2026": publicKey})
	manager.machineFn = func() (string, error) { return machineID, nil }
	ctx, err := manager.Activate(token)
	if err != nil {
		t.Fatal(err)
	}
	if !ctx.Valid || ctx.LicenseID != claims.LicenseID {
		t.Fatalf("unexpected activated context: %+v", ctx)
	}

	reloaded := NewManager(path, map[string]ed25519.PublicKey{"cv-main-2026": publicKey})
	reloaded.machineFn = func() (string, error) { return machineID, nil }
	ctx = reloaded.Load()
	if !ctx.Valid || ctx.Status != StatusLicensed {
		t.Fatalf("unexpected reloaded context: %+v", ctx)
	}
}
