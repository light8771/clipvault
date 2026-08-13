package clipvault

import (
	"os"
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
	if exists, err := HasRecoveryPassword(dir); err != nil || exists {
		t.Fatal("recovery file should not exist initially")
	}
	if exists, err := hasRecoveryEnvelopeForKey(dir, dbKey); err != nil || exists {
		t.Fatal("valid recovery envelope should not exist initially")
	}

	// Set recovery password
	password := "MyStr0ng@Pass"
	if err := SetRecoveryPassword(dir, dbKey, password); err != nil {
		t.Fatalf("SetRecoveryPassword: %v", err)
	}
	t.Logf("[OK] recovery password set")

	if exists, err := HasRecoveryPassword(dir); err != nil || !exists {
		t.Fatal("recovery file should exist after set")
	}
	if valid, err := hasRecoveryEnvelopeForKey(dir, dbKey); err != nil || !valid {
		t.Fatalf("recovery envelope does not match database key: %v", err)
	}

	// Self-test: verify password works
	if err := VerifyRecoveryPassword(dir, password); err != nil {
		t.Fatalf("VerifyRecoveryPassword: %v", err)
	}
	if err := SetRecoveryPassword(dir, dbKey, "Replacement1Pass"); err == nil {
		t.Fatal("SetRecoveryPassword overwrote an existing recovery file")
	}
	if err := VerifyRecoveryPassword(dir, password); err != nil {
		t.Fatalf("rejected second setup changed the original recovery password: %v", err)
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

	if err := ChangeRecoveryPassword(dir, dbKey, newPassword, ""); err == nil {
		t.Fatal("empty recovery password should be rejected")
	}
	if err := VerifyRecoveryPassword(dir, newPassword); err != nil {
		t.Fatalf("rejected change must preserve current password: %v", err)
	}

	// Change with wrong old password should fail
	if err := ChangeRecoveryPassword(dir, dbKey, "wrongold", "anotherNew1Pass"); err == nil {
		t.Fatal("change with wrong old password should fail")
	}
	t.Logf("[OK] change with wrong old password rejected")

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
	recPath := activeDatabaseFile(dir, databaseRecoveryFileName)
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
