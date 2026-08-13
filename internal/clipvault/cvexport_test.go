package clipvault

import (
	"bytes"
	"io"
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

	if bytes.Contains(encData, []byte(secret)) {
		t.Fatal("encrypted export contains plaintext secret!")
	}
	if bytes.Contains(encData, []byte(password)) {
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
			offset := cvexportMagicLen + 1 + 3 + saltLen + cvexportNoncePrefixSize
			data[offset] ^= 1
			return data
		}},
		{name: "frame", mutate: func(data []byte) []byte { data[cvexportHeaderSize+cvexportFrameHeaderSize] ^= 1; return data }},
		{name: "truncated", mutate: func(data []byte) []byte { return data[:len(data)-1] }},
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

func TestCvExportStreamingMultipleChunks(t *testing.T) {
	original := bytes.Repeat([]byte("streaming-data-0123456789\n"), 20000)
	var encrypted bytes.Buffer
	if err := WriteCVExport(&encrypted, "StreamPass1", func(w io.Writer) error {
		for offset := 0; offset < len(original); offset += 997 {
			end := min(offset+997, len(original))
			if _, err := w.Write(original[offset:end]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("WriteCVExport: %v", err)
	}
	var restored bytes.Buffer
	if err := DecryptCVExport(&restored, bytes.NewReader(encrypted.Bytes()), "StreamPass1"); err != nil {
		t.Fatalf("DecryptCVExport: %v", err)
	}
	if !bytes.Equal(restored.Bytes(), original) {
		t.Fatal("streaming roundtrip mismatch")
	}
}

func TestCvExportRejectsUnpublishedWholeBufferFormat(t *testing.T) {
	old := append([]byte("CVEXP001"), make([]byte, 128)...)
	if _, err := DecryptExport(old, "password"); err == nil {
		t.Fatal("former whole-buffer format was accepted")
	}
	if IsCVExport(old) {
		t.Fatal("former whole-buffer magic was identified as current cvexport")
	}
}
