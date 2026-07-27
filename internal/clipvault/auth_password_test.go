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

func TestRefreshSessionExpiryUpdatesExpiry(t *testing.T) {
	auth := NewAuthManager()
	token, err := auth.CreateSession(0) // 浏览器会话 -> maxSessionLifetime
	if err != nil {
		t.Fatal(err)
	}
	sess := auth.GetSession(token)
	if sess == nil {
		t.Fatal("session missing")
	}
	oldExpiry := sess.ExpiresAt

	// 改成 1 分钟：过期时间应被重置为约 now+1min，远小于旧的 24h
	if !auth.RefreshSessionExpiry(token, 1) {
		t.Fatal("RefreshSessionExpiry returned false for existing session")
	}
	refreshed := auth.GetSession(token)
	if refreshed == nil {
		t.Fatal("session disappeared after refresh")
	}
	if !refreshed.ExpiresAt.Before(oldExpiry) {
		t.Fatalf("expiry not shortened: old=%v new=%v", oldExpiry, refreshed.ExpiresAt)
	}
	want := time.Now().Add(time.Minute)
	lo, hi := want.Add(-5*time.Second), want.Add(5*time.Second)
	if refreshed.ExpiresAt.Before(lo) || refreshed.ExpiresAt.After(hi) {
		t.Fatalf("expiry not ~now+1min: %v (want near %v)", refreshed.ExpiresAt, want)
	}

	// 改回 0：过期时间应恢复为约 now+maxSessionLifetime
	if !auth.RefreshSessionExpiry(token, 0) {
		t.Fatal("RefreshSessionExpiry returned false on second call")
	}
	zeroRefreshed := auth.GetSession(token)
	if zeroRefreshed == nil {
		t.Fatal("session disappeared after second refresh")
	}
	wantZero := time.Now().Add(maxSessionLifetime)
	loZ, hiZ := wantZero.Add(-5*time.Second), wantZero.Add(5*time.Second)
	if zeroRefreshed.ExpiresAt.Before(loZ) || zeroRefreshed.ExpiresAt.After(hiZ) {
		t.Fatalf("expiry not ~now+maxSessionLifetime: %v (want near %v)", zeroRefreshed.ExpiresAt, wantZero)
	}
}

func TestRefreshSessionExpiryMissingSession(t *testing.T) {
	auth := NewAuthManager()
	if auth.RefreshSessionExpiry("nonexistent", 1) {
		t.Fatal("RefreshSessionExpiry returned true for missing session")
	}
}

func TestRefreshSecondaryExpiryUpdatesCache(t *testing.T) {
	auth := NewAuthManager()
	token, err := auth.CreateSession(0)
	if err != nil {
		t.Fatal(err)
	}
	auth.GrantSecondaryAuth(token, 10) // 初始缓存 10 分钟
	if !auth.HasSecondaryAuth(token) {
		t.Fatal("secondary auth not granted")
	}

	// 改成 1 分钟：缓存到期应被重置为约 now+1min
	if !auth.RefreshSecondaryExpiry(token, 1) {
		t.Fatal("RefreshSecondaryExpiry returned false for existing session")
	}
	sess := auth.GetSession(token)
	if sess == nil || !sess.SecondaryAuth {
		t.Fatalf("secondary auth lost after refresh: %+v", sess)
	}
	want := time.Now().Add(time.Minute)
	lo, hi := want.Add(-5*time.Second), want.Add(5*time.Second)
	if sess.SecondaryExpiry.Before(lo) || sess.SecondaryExpiry.After(hi) {
		t.Fatalf("secondary expiry not ~now+1min: %v (want near %v)", sess.SecondaryExpiry, want)
	}

	// 改成 0：应立即撤销二次认证缓存
	if !auth.RefreshSecondaryExpiry(token, 0) {
		t.Fatal("RefreshSecondaryExpiry returned false on revoke")
	}
	if auth.HasSecondaryAuth(token) {
		t.Fatal("secondary auth not revoked after timeout set to 0")
	}
}

func TestRefreshSecondaryExpiryWithoutExistingCache(t *testing.T) {
	auth := NewAuthManager()
	token, err := auth.CreateSession(0)
	if err != nil {
		t.Fatal(err)
	}
	// 未通过二次认证时刷新：不应授予缓存，但会话存在应返回 true
	if !auth.RefreshSecondaryExpiry(token, 5) {
		t.Fatal("RefreshSecondaryExpiry returned false for existing session without cache")
	}
	if auth.HasSecondaryAuth(token) {
		t.Fatal("refresh granted secondary auth without prior verification")
	}
}

func TestRefreshSecondaryExpiryMissingSession(t *testing.T) {
	auth := NewAuthManager()
	if auth.RefreshSecondaryExpiry("nonexistent", 1) {
		t.Fatal("RefreshSecondaryExpiry returned true for missing session")
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
