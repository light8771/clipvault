package clipvault

import (
	"sync"
	"testing"
	"time"
)

func TestPasswordHashRoundtrip(t *testing.T) {
	hash, err := newPasswordHash("Correct Horse Battery Staple 42!")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPasswordHash(hash, "Correct Horse Battery Staple 42!") {
		t.Fatal("new password hash did not verify")
	}
	if verifyPasswordHash(hash, "wrong password") {
		t.Fatal("wrong password verified")
	}
}

func TestPasswordHashRejectsUnsupportedFormat(t *testing.T) {
	if verifyPasswordHash("0000000000000000000000000000000000000000000000000000000000000000", "password") {
		t.Fatal("non-scrypt password hash was accepted")
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

func TestGetSessionReturnsSnapshot(t *testing.T) {
	auth := NewAuthManager()
	token, err := auth.CreateSession(10)
	if err != nil {
		t.Fatal(err)
	}
	sess := auth.GetSession(token)
	if sess == nil || !sess.Authenticated {
		t.Fatalf("session not authenticated: %+v", sess)
	}
	sess.Authenticated = false
	sess.SecondaryAuth = true

	fresh := auth.GetSession(token)
	if fresh == nil {
		t.Fatal("session disappeared")
	}
	if !fresh.Authenticated {
		t.Fatal("mutating returned session snapshot changed stored authentication state")
	}
	if fresh.SecondaryAuth {
		t.Fatal("mutating returned session snapshot changed stored secondary state")
	}
}

func TestAuthManagerConcurrentSessionAccess(t *testing.T) {
	auth := NewAuthManager()
	token, err := auth.CreateSession(10)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				auth.GrantSecondaryAuth(token, 1)
				_ = auth.HasSecondaryAuth(token)
				_ = auth.GetSession(token)
				auth.RevokeAllSecondaryAuth()
			}
		}()
	}
	wg.Wait()
}
