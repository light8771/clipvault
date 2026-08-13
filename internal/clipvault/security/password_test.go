package security

import "testing"

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("Correct Horse 2026!")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "Correct Horse 2026!") || VerifyPassword(hash, "wrong password") {
		t.Fatal("password verification mismatch")
	}
	if err := ValidateStoredPasswordHash(hash); err != nil {
		t.Fatal(err)
	}
	if _, err := HashPassword(" short "); err == nil {
		t.Fatal("invalid password accepted")
	}
}
