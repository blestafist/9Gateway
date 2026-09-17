package session

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionStoreLifecycle(t *testing.T) {
	fakeNow := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return fakeNow }

	store := NewStore(StoreOptions{
		MaxSessions:     3,
		IdleTimeout:     10 * time.Minute,
		AbsoluteTimeout: 1 * time.Hour,
		Now:             nowFn,
	})

	// Create session
	sess, err := store.Create()
	if err != nil {
		t.Fatalf("unexpected error creating session: %v", err)
	}
	if len(sess.ID) != 64 {
		t.Fatalf("session ID length = %d; want 64", len(sess.ID))
	}
	if len(sess.CSRFToken) != 64 {
		t.Fatalf("CSRF token length = %d; want 64", len(sess.CSRFToken))
	}

	// Retrieve session
	retrieved := store.Get(sess.ID)
	if retrieved == nil || retrieved.ID != sess.ID {
		t.Fatalf("failed to retrieve valid session")
	}

	// CSRF validation
	if !store.ValidateCSRF(sess.ID, sess.CSRFToken) {
		t.Fatalf("expected CSRF token to validate")
	}
	if store.ValidateCSRF(sess.ID, "wrong-token") {
		t.Fatalf("expected invalid CSRF token to be rejected")
	}
	if store.ValidateCSRF("unknown-session", sess.CSRFToken) {
		t.Fatalf("expected unknown session CSRF to fail")
	}

	// Advance time within idle timeout
	fakeNow = fakeNow.Add(5 * time.Minute)
	if store.Get(sess.ID) == nil {
		t.Fatalf("session should still be valid after 5 minutes")
	}

	// Advance time past idle timeout (10m from last activity at 12:05 -> 12:16)
	fakeNow = fakeNow.Add(11 * time.Minute)
	if store.Get(sess.ID) != nil {
		t.Fatalf("session should have expired past idle timeout")
	}

	// Create new session for absolute timeout test
	sess2, _ := store.Create()
	fakeNow = fakeNow.Add(5 * time.Minute)
	if store.Get(sess2.ID) == nil {
		t.Fatalf("session 2 should be valid after touch")
	}
	// Touch again
	fakeNow = fakeNow.Add(5 * time.Minute)
	if store.Get(sess2.ID) == nil {
		t.Fatalf("session 2 should be valid after second touch")
	}
	// Advance by 5 minutes repeatedly until 1 hour absolute timeout is passed
	for i := 0; i < 11; i++ {
		fakeNow = fakeNow.Add(5 * time.Minute)
		_ = store.Get(sess2.ID)
	}
	// Now absolute timeout (1 hour from creation) is reached
	fakeNow = fakeNow.Add(5 * time.Minute)
	if store.Get(sess2.ID) != nil {
		t.Fatalf("session 2 should expire past absolute timeout despite recent touch")
	}
}

func TestSessionCapacityEviction(t *testing.T) {
	fakeNow := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return fakeNow }

	store := NewStore(StoreOptions{
		MaxSessions:     2,
		IdleTimeout:     1 * time.Hour,
		AbsoluteTimeout: 10 * time.Hour,
		Now:             nowFn,
	})

	s1, _ := store.Create()
	fakeNow = fakeNow.Add(1 * time.Minute)
	s2, _ := store.Create()

	if store.Count() != 2 {
		t.Fatalf("store count = %d; want 2", store.Count())
	}

	// Touch s1 so s2 becomes the least recently used
	fakeNow = fakeNow.Add(1 * time.Minute)
	store.Get(s1.ID)

	// Creating s3 should evict s2 (oldest LastActiveAt)
	fakeNow = fakeNow.Add(1 * time.Minute)
	s3, _ := store.Create()

	if store.Count() != 2 {
		t.Fatalf("store count = %d; want 2 after eviction", store.Count())
	}
	if store.Get(s2.ID) != nil {
		t.Fatalf("s2 should have been evicted")
	}
	if store.Get(s1.ID) == nil {
		t.Fatalf("s1 should still be present")
	}
	if store.Get(s3.ID) == nil {
		t.Fatalf("s3 should still be present")
	}
}

func TestSessionRevocation(t *testing.T) {
	store := NewStore(StoreOptions{})
	s1, _ := store.Create()
	s2, _ := store.Create()

	store.Revoke(s1.ID)
	if store.Get(s1.ID) != nil {
		t.Fatalf("s1 should be revoked")
	}
	if store.Get(s2.ID) == nil {
		t.Fatalf("s2 should still exist")
	}

	store.RevokeAll()
	if store.Get(s2.ID) != nil {
		t.Fatalf("s2 should be revoked by RevokeAll")
	}
	if store.Count() != 0 {
		t.Fatalf("count after RevokeAll = %d; want 0", store.Count())
	}
}

func TestLoginRateLimiter(t *testing.T) {
	fakeNow := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return fakeNow }

	limiter := NewLoginRateLimiter(RateLimiterOptions{
		MaxTrackedIPs:   2,
		MaxAttempts:     3,
		Window:          5 * time.Minute,
		LockoutDuration: 10 * time.Minute,
		Now:             nowFn,
	})

	ip1 := "192.168.1.1"

	if !limiter.Allow(ip1) {
		t.Fatalf("initial attempt should be allowed")
	}

	// 2 failures
	limiter.RecordFailure(ip1)
	limiter.RecordFailure(ip1)
	if !limiter.Allow(ip1) {
		t.Fatalf("2 failures should still be allowed under threshold 3")
	}

	// 3rd failure locks it out
	limiter.RecordFailure(ip1)
	if limiter.Allow(ip1) {
		t.Fatalf("3 failures should lock out ip1")
	}

	// Fast forward 5 minutes (still within 10 min lockout)
	fakeNow = fakeNow.Add(5 * time.Minute)
	if limiter.Allow(ip1) {
		t.Fatalf("ip1 should still be locked out at 5 minutes")
	}

	// Fast forward past 10 min lockout
	fakeNow = fakeNow.Add(6 * time.Minute)
	if !limiter.Allow(ip1) {
		t.Fatalf("ip1 should be allowed after lockout expiration")
	}

	// Test success resets failures
	limiter.RecordFailure(ip1)
	limiter.RecordFailure(ip1)
	limiter.RecordSuccess(ip1)
	limiter.RecordFailure(ip1)
	if !limiter.Allow(ip1) {
		t.Fatalf("ip1 should be allowed since success reset prior failures")
	}
}

func TestLoginRateLimiterBoundedCapacity(t *testing.T) {
	limiter := NewLoginRateLimiter(RateLimiterOptions{
		MaxTrackedIPs:   2,
		MaxAttempts:     3,
		Window:          5 * time.Minute,
		LockoutDuration: 10 * time.Minute,
	})

	limiter.RecordFailure("10.0.0.1")
	limiter.RecordFailure("10.0.0.2")
	limiter.RecordFailure("10.0.0.3")

	// The map must not exceed MaxTrackedIPs (2)
	limiter.mu.Lock()
	count := len(limiter.failures)
	limiter.mu.Unlock()

	if count > 2 {
		t.Fatalf("rate limiter map count = %d; want <= 2", count)
	}
}

func TestTrustedProxyAndCookieSecurity(t *testing.T) {
	proxies, err := ParseTrustedProxies([]string{"127.0.0.1", "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("failed to parse trusted proxies: %v", err)
	}

	// 1. Direct TLS request -> Secure is true
	tlsReq := httptest.NewRequest(http.MethodGet, "https://gateway.local/admin/v1/keys", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	if !IsSecureRequest(tlsReq, nil) {
		t.Fatalf("direct TLS request must be secure")
	}

	// 2. Plain HTTP without trusted proxies -> never trust X-Forwarded-Proto
	plainReq := httptest.NewRequest(http.MethodGet, "http://gateway.local/admin/v1/keys", nil)
	plainReq.RemoteAddr = "127.0.0.1:12345"
	plainReq.Header.Set("X-Forwarded-Proto", "https")
	if IsSecureRequest(plainReq, nil) {
		t.Fatalf("untrusted proxy request must not be secure even with X-Forwarded-Proto")
	}

	// 3. Plain HTTP with trusted proxy and matching remote IP
	if !IsSecureRequest(plainReq, proxies) {
		t.Fatalf("request from trusted proxy 127.0.0.1 with X-Forwarded-Proto: https must be secure")
	}

	// 4. Plain HTTP from untrusted remote IP even with trusted proxies configured
	untrustedReq := httptest.NewRequest(http.MethodGet, "http://gateway.local/admin/v1/keys", nil)
	untrustedReq.RemoteAddr = "192.168.1.50:12345"
	untrustedReq.Header.Set("X-Forwarded-Proto", "https")
	if IsSecureRequest(untrustedReq, proxies) {
		t.Fatalf("request from untrusted remote IP must not be secure")
	}

	// 5. Test Cookie flags
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, plainReq, "test-session-id", 3600, proxies)
	cookieHeader := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookieHeader, "HttpOnly") {
		t.Fatalf("cookie missing HttpOnly")
	}
	if !strings.Contains(cookieHeader, "SameSite=Strict") {
		t.Fatalf("cookie missing SameSite=Strict")
	}
	if !strings.Contains(cookieHeader, "Path=/admin") {
		t.Fatalf("cookie missing Path=/admin")
	}
	if !strings.Contains(cookieHeader, "Secure") {
		t.Fatalf("cookie should have Secure flag for trusted proxy https")
	}

	// Clear cookie
	rec2 := httptest.NewRecorder()
	ClearSessionCookie(rec2, plainReq, proxies)
	cleared := rec2.Header().Get("Set-Cookie")
	if !strings.Contains(cleared, "Max-Age=0") && !strings.Contains(cleared, "Max-Age=-1") {
		t.Fatalf("clear cookie missing Max-Age=0 or -1: %s", cleared)
	}
}
