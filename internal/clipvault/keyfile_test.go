package clipvault

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryEnvelopeValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clipvault.recovery")
	key := make([]byte, wrappingLen)
	for i := range key {
		key[i] = byte(i + 1)
	}
	password := []byte("EnvelopeTest1Pass")
	if err := WriteRecoveryFile(path, key, password); err != nil {
		t.Fatal(err)
	}
	recovered, _, err := ReadRecoveryFile(path, password)
	if err != nil || !bytes.Equal(recovered, key) {
		t.Fatalf("recovery round trip failed: %v", err)
	}
	if valid, err := hasRecoveryEnvelopeForKey(dir, key); err != nil || !valid {
		t.Fatalf("valid recovery envelope was rejected: %v", err)
	}
	otherKey := append([]byte(nil), key...)
	otherKey[0] ^= 0xff
	if valid, err := hasRecoveryEnvelopeForKey(dir, otherKey); err == nil || valid {
		t.Fatal("recovery envelope matched a different database key")
	}

	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "version", mutate: func(data []byte) []byte { data[6]++; return data }},
		{name: "fingerprint", mutate: func(data []byte) []byte { data[7] ^= 0xff; return data }},
		{name: "trailing bytes", mutate: func(data []byte) []byte { return append(data, 0) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := test.mutate(append([]byte(nil), original...))
			if err := os.WriteFile(path, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
			if valid, err := hasRecoveryEnvelopeForKey(dir, key); err == nil || valid {
				t.Fatal("tampered recovery envelope passed startup validation")
			}
			if _, _, err := ReadRecoveryFile(path, password); err == nil {
				t.Fatal("tampered recovery envelope was accepted")
			}
		})
	}
}
