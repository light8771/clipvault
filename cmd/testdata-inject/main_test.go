package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallManagedImage(t *testing.T) {
	dataDir := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "source.png")
	want := []byte("test png payload")
	if err := os.WriteFile(sourcePath, want, 0o600); err != nil {
		t.Fatal(err)
	}

	relativePath, err := installManagedImage(dataDir, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if relativePath != "images/e2e/managed.png" {
		t.Fatalf("relative path = %q", relativePath)
	}

	got, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("copied image = %q, want %q", got, want)
	}
}
