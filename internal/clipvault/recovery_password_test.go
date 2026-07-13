package clipvault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryPasswordLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	dbKey := store.DBKey()

	// Initially no recovery password
	if HasRecoveryPassword(dir) {
		t.Fatal("recovery file should not exist initially")
	}

	// Set recovery password
	password := "MyStr0ng@Pass"
	if err := SetRecoveryPassword(dir, dbKey, password); err != nil {
		t.Fatalf("SetRecoveryPassword: %v", err)
	}
	t.Logf("[OK] recovery password set")

	if !HasRecoveryPassword(dir) {
		t.Fatal("recovery file should exist after set")
	}

	// Verify state file updated
	state, err := ReadStateFile(dir)
	if err != nil {
		t.Fatalf("ReadStateFile: %v", err)
	}
	if !state.HasRecovery {
		t.Fatal("state.HasRecovery should be true")
	}
	t.Logf("[OK] state file has HasRecovery=true")

	// Self-test: verify password works
	if err := VerifyRecoveryPassword(dir, password); err != nil {
		t.Fatalf("VerifyRecoveryPassword: %v", err)
	}
	t.Logf("[OK] self-test: password decrypts key correctly")

	// Wrong password should fail
	if err := VerifyRecoveryPassword(dir, "wrongpassword"); err == nil {
		t.Fatal("wrong password should fail verification")
	}
	t.Logf("[OK] wrong password rejected")

	// Change recovery password
	newPassword := "NewStr0ng#Pass2026"
	if err := ChangeRecoveryPassword(dir, dbKey, password, newPassword); err != nil {
		t.Fatalf("ChangeRecoveryPassword: %v", err)
	}
	t.Logf("[OK] recovery password changed")

	// Old password should no longer work
	if err := VerifyRecoveryPassword(dir, password); err == nil {
		t.Fatal("old password should fail after change")
	}
	t.Logf("[OK] old password rejected after change")

	// New password should work
	if err := VerifyRecoveryPassword(dir, newPassword); err != nil {
		t.Fatalf("new password verification: %v", err)
	}
	t.Logf("[OK] new password works after change")

	// Change with wrong old password should fail
	if err := ChangeRecoveryPassword(dir, dbKey, "wrongold", "anotherNew1Pass"); err == nil {
		t.Fatal("change with wrong old password should fail")
	}
	t.Logf("[OK] change with wrong old password rejected")

	// Remove recovery password
	if err := RemoveRecoveryPassword(dir); err != nil {
		t.Fatalf("RemoveRecoveryPassword: %v", err)
	}
	t.Logf("[OK] recovery password removed")

	if HasRecoveryPassword(dir) {
		t.Fatal("recovery file should not exist after remove")
	}

	// State file should reflect removal
	state, err = ReadStateFile(dir)
	if err != nil {
		t.Fatalf("ReadStateFile after remove: %v", err)
	}
	if state.HasRecovery {
		t.Fatal("state.HasRecovery should be false after remove")
	}
	t.Logf("[OK] state file has HasRecovery=false after remove")
}

func TestPasswordStrength(t *testing.T) {
	tests := []struct {
		password string
		wantErr  bool
	}{
		{"short", true},        // too short
		{"alllowercase", true}, // only lowercase (1 category)
		{"ALLUPPERCASE", true}, // only uppercase (1 category)
		{"12345678", true},     // only digits (1 category)
		{"abcdefgh1", false},   // lowercase + digit (2 categories)
		{"ABCdefgh", false},    // uppercase + lowercase (2 categories)
		{"Abc12345", false},    // uppercase + lowercase + digit (3 categories)
		{"Abc123!@#", false},   // all 4 categories
		{"Pass1", true},        // too short even with multiple categories
		{"", true},             // empty
	}

	for _, tt := range tests {
		err := CheckPasswordStrength(tt.password)
		if (err != nil) != tt.wantErr {
			t.Errorf("CheckPasswordStrength(%q) = %v, wantErr=%v", tt.password, err, tt.wantErr)
		}
	}
}

func TestRecoveryFileDoesNotLeakPassword(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	password := "UniqueTest@Pass2026"
	if err := SetRecoveryPassword(dir, store.DBKey(), password); err != nil {
		t.Fatal(err)
	}

	// Read raw recovery file and check it doesn't contain the password
	recPath := filepath.Join(dir, "clipvault.recovery")
	raw, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	if containsStr(raw, password) {
		t.Fatal("recovery file contains plaintext password!")
	}
	t.Logf("[OK] recovery file does not contain plaintext password")
}

func containsStr(data []byte, s string) bool {
	if len(s) == 0 || len(data) < len(s) {
		return false
	}
	for i := 0; i <= len(data)-len(s); i++ {
		match := true
		for j := 0; j < len(s); j++ {
			if data[i+j] != s[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
