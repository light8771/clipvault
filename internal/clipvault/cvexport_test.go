package clipvault

import (
	"bytes"
	"testing"
)

func TestCvExportRoundtrip(t *testing.T) {
	original := []byte(`{"items":[{"id":1,"text":"hello world 🎉"},{"id":2,"text":"secret data"}],"groups":[]}`)
	password := "ExportPass@2026"

	// Encrypt
	encData, err := EncryptExport(original, password)
	if err != nil {
		t.Fatalf("EncryptExport: %v", err)
	}
	t.Logf("[OK] encrypted %d bytes -> %d bytes", len(original), len(encData))

	// Verify magic
	if !IsCVExport(encData) {
		t.Fatal("IsCVExport should return true for encrypted data")
	}

	// Decrypt with correct password
	decData, err := DecryptExport(encData, password)
	if err != nil {
		t.Fatalf("DecryptExport: %v", err)
	}
	if !bytes.Equal(original, decData) {
		t.Fatalf("roundtrip mismatch: original=%q decrypted=%q", original, decData)
	}
	t.Logf("[OK] roundtrip: decrypted data matches original")

	// Wrong password should fail
	_, err = DecryptExport(encData, "wrongpassword")
	if err == nil {
		t.Fatal("wrong password should fail decryption")
	}
	t.Logf("[OK] wrong password rejected")

	// IsCVExport on random data should return false
	if IsCVExport(original) {
		t.Fatal("IsCVExport should return false for non-cvexport data")
	}
	t.Logf("[OK] IsCVExport correctly identifies non-cvexport data")
}

func TestCvExportDoesNotLeakPlaintext(t *testing.T) {
	secret := "ThisIsVerySecretData123!@#"
	data := []byte(`{"text":"` + secret + `"}`)
	password := "LeakTest@Pass1"

	encData, err := EncryptExport(data, password)
	if err != nil {
		t.Fatal(err)
	}

	if containsStr(encData, secret) {
		t.Fatal("encrypted export contains plaintext secret!")
	}
	if containsStr(encData, password) {
		t.Fatal("encrypted export contains plaintext password!")
	}
	t.Logf("[OK] encrypted export does not leak plaintext or password")
}

func TestCvExportRejectsTamperedEnvelope(t *testing.T) {
	original, err := EncryptExport([]byte(`{"items":[]}`), "EnvelopePass1")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "version", mutate: func(data []byte) []byte { data[cvexportMagicLen]++; return data }},
		{name: "reserved", mutate: func(data []byte) []byte { data[cvexportMagicLen+1] = 1; return data }},
		{name: "KDF", mutate: func(data []byte) []byte {
			offset := cvexportMagicLen + 1 + 3 + saltLen + nonceLen
			data[offset] ^= 1
			return data
		}},
		{name: "trailing bytes", mutate: func(data []byte) []byte { return append(data, 0) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := test.mutate(append([]byte(nil), original...))
			if _, err := DecryptExport(data, "EnvelopePass1"); err == nil {
				t.Fatal("tampered cvexport envelope was accepted")
			}
		})
	}
	if _, err := EncryptExport([]byte("data"), ""); err == nil {
		t.Fatal("empty export password was accepted")
	}
}
