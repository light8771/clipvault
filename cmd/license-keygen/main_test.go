package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestEncryptedPrivateKeyRoundtrip(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	passphrase := []byte("correct horse battery staple")
	encrypted, err := encryptPrivateKey(privateKey, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, privateKey) {
		t.Fatal("encrypted file contains plaintext private key")
	}
	decrypted, err := decryptPrivateKey(encrypted, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, privateKey) {
		t.Fatal("decrypted private key differs")
	}
	if _, err := decryptPrivateKey(encrypted, []byte("this is the wrong passphrase")); err == nil {
		t.Fatal("wrong passphrase unexpectedly succeeded")
	}
}
