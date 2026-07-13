package clipvault

import (
	"testing"
	"time"
)

func TestPasswordHashRoundtrip(t *testing.T) {
	hash, err := newPasswordHash("Correct Horse Battery Staple 42!")
	if err != nil {
		t.Fatal(err)
	}
	if isLegacyPasswordHash(hash) {
		t.Fatal("new password hash was classified as legacy")
	}
	if !verifyPasswordHash(hash, "", "Correct Horse Battery Staple 42!") {
		t.Fatal("new password hash did not verify")
	}
	if verifyPasswordHash(hash, "", "wrong password") {
		t.Fatal("wrong password verified")
	}
}

func TestLegacyPasswordHashCompatibility(t *testing.T) {
	salt := "00112233445566778899aabbccddeeff"
	hash := hashPasswordLegacy(salt, "LegacyPassword1!")
	if !isLegacyPasswordHash(hash) {
		t.Fatal("legacy password hash was not detected")
	}
	if !verifyPasswordHash(hash, salt, "LegacyPassword1!") {
		t.Fatal("legacy password hash did not verify")
	}
	if verifyPasswordHash(hash, salt, "wrong password") {
		t.Fatal("wrong legacy password verified")
	}
}

func TestPasswordRateLimitAllowsTenAttemptsPerFiveMinutes(t *testing.T) {
	auth := NewAuthManager()
	for attempt := 1; attempt <= rateLimitThreshold; attempt++ {
		status := auth.RecordFailedAttempt()
		wantRemaining := rateLimitThreshold - attempt
		if status.Remaining != wantRemaining {
			t.Fatalf("attempt %d: remaining=%d, want %d", attempt, status.Remaining, wantRemaining)
		}
		if attempt < rateLimitThreshold && !status.Allowed {
			t.Fatalf("attempt %d was blocked too early", attempt)
		}
		if attempt == rateLimitThreshold {
			if status.Allowed {
				t.Fatal("tenth failed attempt did not trigger rate limit")
			}
			if status.RetryAfter <= 4*time.Minute || status.RetryAfter > rateLimitWindow {
				t.Fatalf("unexpected retry duration: %s", status.RetryAfter)
			}
		}
	}
	if status := auth.CheckRateLimit(); status.Allowed || status.Remaining != 0 {
		t.Fatalf("rate limit not retained: %+v", status)
	}
	auth.ResetRateLimit()
	if status := auth.CheckRateLimit(); !status.Allowed || status.Remaining != rateLimitThreshold {
		t.Fatalf("rate limit not reset: %+v", status)
	}
}
