package clipvault

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuthClientIPIgnoresPortAndProxyHeaders(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"192.0.2.1:1000", "192.0.2.1"},
		{"192.0.2.1:2000", "192.0.2.1"},
		{"[::ffff:192.0.2.1]:3000", "192.0.2.1"},
		{"[2001:0db8::1]:4000", "2001:db8::1"},
		{"2001:db8::1", "2001:db8::1"},
		{"invalid", "unknown"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Forwarded-For", "203.0.113.99")
		r.Header.Set("X-Real-IP", "203.0.113.98")
		r.Header.Set("Forwarded", "for=203.0.113.97")
		if got := authClientIP(r); got != tc.want {
			t.Fatalf("%s: got %s, want %s", tc.remote, got, tc.want)
		}
	}
}

func TestPasswordRateLimitIsolationAndExpiry(t *testing.T) {
	auth := NewAuthManager()
	for range 40 {
		auth.verifyPasswordAttempt(ratePurposeLock, "192.0.2.1", func() bool { return false })
	}
	if _, status := auth.verifyPasswordAttempt(ratePurposeLock, "192.0.2.1", func() bool { t.Fatal("blocked verifier ran"); return true }); status.Allowed {
		t.Fatal("offending peer was not blocked")
	}
	for _, key := range []rateBucketKey{{ratePurposeLock, "192.0.2.2"}, {ratePurposeSecondary, "192.0.2.1"}} {
		if valid, got := auth.verifyPasswordAttempt(key.purpose, key.clientIP, func() bool { return true }); !valid || !got.Allowed || got.Remaining != rateLimitThreshold {
			t.Fatalf("unrelated allowance changed: %+v", got)
		}
	}
	key := rateBucketKey{ratePurposeLock, "192.0.2.1"}
	b := auth.rateBuckets[key]
	if len(b.failures) != rateLimitThreshold {
		t.Fatalf("blocked requests grew failure history: %d", len(b.failures))
	}
	if _, status := auth.verifyPasswordAttempt(key.purpose, key.clientIP, func() bool { t.Fatal("blocked verifier ran"); return true }); status.Allowed {
		t.Fatal("unrelated success reset the blocked peer")
	}
	for i := range b.failures {
		b.failures[i] = time.Now().Add(-rateLimitWindow - time.Second)
	}
	auth.CleanExpiredRateBuckets()
	if len(auth.rateBuckets) != 0 {
		t.Fatal("expired peer buckets were not removed")
	}
	if valid, got := auth.verifyPasswordAttempt(key.purpose, key.clientIP, func() bool { return true }); !valid || !got.Allowed || got.Remaining != rateLimitThreshold {
		t.Fatalf("expired allowance did not recover: %+v", got)
	}
}

func TestPasswordVerificationAdmissionIsAtomicAndPeerLocal(t *testing.T) {
	auth := NewAuthManager()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		auth.verifyPasswordAttempt(ratePurposeLock, "192.0.2.1", func() bool {
			close(started)
			<-release
			return false
		})
	}()
	<-started
	defer func() { close(release); <-done }()
	// The cleaner must not remove an in-flight reservation, even with no failures.
	auth.CleanExpiredRateBuckets()
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			valid, status := auth.verifyPasswordAttempt(ratePurposeLock, "192.0.2.1", func() bool { t.Error("concurrent verifier ran"); return true })
			if valid || status.Allowed || status.RetryAfter != time.Second {
				t.Errorf("bad admission result: %+v", status)
			}
		})
	}
	wg.Wait()
	for _, key := range []rateBucketKey{{ratePurposeLock, "192.0.2.2"}, {ratePurposeSecondary, "192.0.2.1"}} {
		if valid, _ := auth.verifyPasswordAttempt(key.purpose, key.clientIP, func() bool { return true }); !valid {
			t.Fatal("unrelated verifier blocked")
		}
	}
}

func TestPasswordVerificationSuccessAndPanicReleaseReservation(t *testing.T) {
	auth := NewAuthManager()
	auth.verifyPasswordAttempt(ratePurposeLock, "192.0.2.1", func() bool { return false })
	if valid, status := auth.verifyPasswordAttempt(ratePurposeLock, "192.0.2.1", func() bool { return true }); !valid || status.Remaining != rateLimitThreshold {
		t.Fatalf("success did not reset allowance: %+v", status)
	}
	if len(auth.rateBuckets) != 0 {
		t.Fatal("successful peer retained a bucket")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected verifier panic")
			}
		}()
		auth.verifyPasswordAttempt(ratePurposeLock, "192.0.2.1", func() bool { panic("verifier failed") })
	}()
	if _, status := auth.verifyPasswordAttempt(ratePurposeLock, "192.0.2.1", func() bool { return false }); !status.Allowed || status.Remaining != rateLimitThreshold-2 {
		t.Fatalf("panic leaked reservation or lost accounting: %+v", status)
	}
}

func TestPasswordEndpointsIsolatePeersAndSharePurpose(t *testing.T) {
	cfg := DefaultConfig()
	hash, err := newPasswordHash("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Security.LockPasswordHash = hash
	cfg.Security.SecondaryPasswordHash = hash
	auth := NewAuthManager()
	server := &SearchServer{cfgMgr: &ConfigManager{cfg: cfg}, auth: auth}
	for _, purpose := range []string{ratePurposeLock, ratePurposeSecondary} {
		for range rateLimitThreshold {
			auth.verifyPasswordAttempt(purpose, "192.0.2.1", func() bool { return false })
		}
	}
	session, err := auth.CreateSession(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		handler http.HandlerFunc
		body    string
	}{
		{server.handleAuthLogin, `{"password":"wrong"}`},
		{server.handleAuthSecondary, `{"password":"wrong"}`},
		{server.handleAuthSetPassword, `{"type":"lock","action":"change","oldPassword":"wrong","newPassword":"replacement-password"}`},
		{server.handleAuthSetPassword, `{"type":"secondary","action":"change","oldPassword":"wrong","newPassword":"replacement-password"}`},
	} {
		for _, peer := range []string{"192.0.2.1:2000", "192.0.2.2:2000"} {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			r.RemoteAddr = peer
			r.Header.Set("X-Forwarded-For", "203.0.113.1")
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
			w := httptest.NewRecorder()
			tc.handler(w, r)
			want := http.StatusUnauthorized
			if strings.HasPrefix(peer, "192.0.2.1:") {
				want = http.StatusTooManyRequests
			}
			if w.Code != want {
				t.Fatalf("peer=%s body=%s: status=%d want=%d response=%s", peer, tc.body, w.Code, want, w.Body.String())
			}
		}
	}
}
