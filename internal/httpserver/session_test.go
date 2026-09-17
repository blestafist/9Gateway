package httpserver

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/session"
	"github.com/pestit/9gateway/internal/storage"
)

type testSessionKeyRepository struct {
	records []storage.APIKeyRecord
}

func (r *testSessionKeyRepository) Insert(ctx context.Context, record storage.APIKeyRecord) error {
	r.records = append(r.records, record)
	return nil
}

func (r *testSessionKeyRepository) List(ctx context.Context) ([]storage.APIKeyRecord, error) {
	return append([]storage.APIKeyRecord(nil), r.records...), nil
}

func (r *testSessionKeyRepository) ListAPIKeys(ctx context.Context, limit int, cursor string) ([]storage.KeyListRecord, string, error) {
	var items []storage.KeyListRecord
	for _, rec := range r.records {
		items = append(items, storage.KeyListRecord{
			ID:            rec.ID,
			Name:          rec.Name,
			DisplayPrefix: rec.DisplayPrefix,
			Enabled:       rec.Enabled,
		})
	}
	return items, "", nil
}

func newTestAdminHandler(t *testing.T, credential string, opts ...func(*adminHandler)) (*adminHandler, *testSessionKeyRepository) {
	t.Helper()
	repo := &testSessionKeyRepository{}
	service, err := newAdminKeyService(repo, []byte("test-pepper"))
	if err != nil {
		t.Fatalf("newAdminKeyService failed: %v", err)
	}
	handler, err := newAdminHandler(credential, service)
	if err != nil {
		t.Fatalf("newAdminHandler failed: %v", err)
	}
	for _, opt := range opts {
		opt(handler)
	}
	handler.initSessions()
	return handler, repo
}

func TestSessionLoginAndState(t *testing.T) {
	const adminSecret = "secret-admin-pass"
	handler, _ := newTestAdminHandler(t, adminSecret)

	// 1. Successful login
	loginBody := `{"credential":"` + adminSecret + `"}`
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var loginResp struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrf_token"`
		IdleExpiresAt string `json:"idle_expires_at"`
		ExpiresAt     string `json:"expires_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("failed to decode login response: %v", err)
	}
	if !loginResp.Authenticated || loginResp.CSRFToken == "" {
		t.Fatalf("login response invalid: %+v", loginResp)
	}

	// Verify Set-Cookie header
	cookie := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookie {
		if c.Name == session.CookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("missing session cookie")
	}
	if !sessionCookie.HttpOnly {
		t.Errorf("cookie must be HttpOnly")
	}
	if sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie must be SameSite=Strict")
	}
	if sessionCookie.Path != "/admin" {
		t.Errorf("cookie Path = %q; want /admin", sessionCookie.Path)
	}

	// 2. GET /admin/ui/v1/session with cookie reports authenticated state
	getReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/ui/v1/session", nil)
	getReq.AddCookie(sessionCookie)
	getRec := httptest.NewRecorder()

	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get session status = %d; want 200", getRec.Code)
	}
	var getResp struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrf_token"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&getResp); err != nil {
		t.Fatalf("failed to decode get session response: %v", err)
	}
	if !getResp.Authenticated || getResp.CSRFToken != loginResp.CSRFToken {
		t.Fatalf("unexpected session state: %+v", getResp)
	}

	// 3. GET /admin/ui/v1/session without cookie reports unauthenticated
	noCookieReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/ui/v1/session", nil)
	noCookieRec := httptest.NewRecorder()
	handler.ServeHTTP(noCookieRec, noCookieReq)
	if noCookieRec.Code != http.StatusOK {
		t.Fatalf("unauthenticated status = %d; want 200", noCookieRec.Code)
	}
	var unauthResp struct {
		Authenticated bool `json:"authenticated"`
	}
	_ = json.NewDecoder(noCookieRec.Body).Decode(&unauthResp)
	if unauthResp.Authenticated {
		t.Errorf("expected unauthenticated state")
	}
}

func TestSessionLogout(t *testing.T) {
	const adminSecret = "secret-admin-pass"
	handler, _ := newTestAdminHandler(t, adminSecret)

	// Log in
	loginReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)

	var loginResp struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(loginRec.Body).Decode(&loginResp)
	cookie := loginRec.Result().Cookies()[0]

	// Logout with active session requires Origin and CSRF
	logoutReq := httptest.NewRequest(http.MethodDelete, "http://localhost:8080/admin/ui/v1/session", nil)
	logoutReq.AddCookie(cookie)
	logoutReq.Header.Set("Origin", "http://localhost:8080")
	logoutReq.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
	logoutRec := httptest.NewRecorder()

	handler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d; want 200; body = %s", logoutRec.Code, logoutRec.Body.String())
	}

	// Verify cookie cleared
	clearedCookie := logoutRec.Result().Cookies()[0]
	if clearedCookie.MaxAge > 0 {
		t.Errorf("cleared cookie MaxAge should be <= 0")
	}

	// Ensure session is gone
	getReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/ui/v1/session", nil)
	getReq.AddCookie(cookie)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	var getResp struct {
		Authenticated bool `json:"authenticated"`
	}
	_ = json.NewDecoder(getRec.Body).Decode(&getResp)
	if getResp.Authenticated {
		t.Errorf("session should be inactive after logout")
	}

	// Logout when already expired or without session still clears cookie and returns 200
	cleanReq := httptest.NewRequest(http.MethodDelete, "http://localhost:8080/admin/ui/v1/session", nil)
	cleanRec := httptest.NewRecorder()
	handler.ServeHTTP(cleanRec, cleanReq)
	if cleanRec.Code != http.StatusOK {
		t.Errorf("idempotent logout status = %d; want 200", cleanRec.Code)
	}
}

func TestSessionAuthenticationOnAdminAPI(t *testing.T) {
	const adminSecret = "secret-admin-pass"
	handler, _ := newTestAdminHandler(t, adminSecret)

	// Log in to obtain cookie and CSRF token
	loginReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)

	var loginResp struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(loginRec.Body).Decode(&loginResp)
	cookie := loginRec.Result().Cookies()[0]

	// 1. Safe method GET /admin/v1/keys with session cookie
	getReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/v1/keys", nil)
	getReq.AddCookie(cookie)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /admin/v1/keys with cookie status = %d; want 200", getRec.Code)
	}

	// 2. Unsafe method POST /admin/v1/keys with session cookie, correct Origin and CSRF
	postReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/v1/keys", strings.NewReader(`{"name":"session-key"}`))
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("Origin", "http://localhost:8080")
	postReq.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
	postReq.AddCookie(cookie)
	postRec := httptest.NewRecorder()
	handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST /admin/v1/keys with session status = %d; want 201; body = %s", postRec.Code, postRec.Body.String())
	}

	// 3. Unsafe method with missing CSRF token -> 403 Forbidden
	noCsrfReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/v1/keys", strings.NewReader(`{"name":"fail-key"}`))
	noCsrfReq.Header.Set("Content-Type", "application/json")
	noCsrfReq.Header.Set("Origin", "http://localhost:8080")
	noCsrfReq.AddCookie(cookie)
	noCsrfRec := httptest.NewRecorder()
	handler.ServeHTTP(noCsrfRec, noCsrfReq)
	if noCsrfRec.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF status = %d; want 403", noCsrfRec.Code)
	}

	// 4. Unsafe method with cross-origin Origin -> 403 Forbidden
	crossOriginReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/v1/keys", strings.NewReader(`{"name":"fail-key"}`))
	crossOriginReq.Header.Set("Content-Type", "application/json")
	crossOriginReq.Header.Set("Origin", "http://attacker.example.com")
	crossOriginReq.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
	crossOriginReq.AddCookie(cookie)
	crossOriginRec := httptest.NewRecorder()
	handler.ServeHTTP(crossOriginRec, crossOriginReq)
	if crossOriginRec.Code != http.StatusForbidden {
		t.Fatalf("POST with cross-origin status = %d; want 403", crossOriginRec.Code)
	}

	// 5. Unsafe method with missing Origin and Referer -> 403 Forbidden
	noOriginReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/v1/keys", strings.NewReader(`{"name":"fail-key"}`))
	noOriginReq.Header.Set("Content-Type", "application/json")
	noOriginReq.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
	noOriginReq.AddCookie(cookie)
	noOriginRec := httptest.NewRecorder()
	handler.ServeHTTP(noOriginRec, noOriginReq)
	if noOriginRec.Code != http.StatusForbidden {
		t.Fatalf("POST without Origin/Referer status = %d; want 403", noOriginRec.Code)
	}

	// 6. Bearer token clients remain completely unaffected (no cookie, no CSRF, no Origin needed)
	bearerReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/v1/keys", strings.NewReader(`{"name":"cli-key"}`))
	bearerReq.Header.Set("Content-Type", "application/json")
	bearerReq.Header.Set("Authorization", "Bearer "+adminSecret)
	bearerRec := httptest.NewRecorder()
	handler.ServeHTTP(bearerRec, bearerReq)
	if bearerRec.Code != http.StatusCreated {
		t.Fatalf("Bearer POST status = %d; want 201; body = %s", bearerRec.Code, bearerRec.Body.String())
	}
}

func TestDuplicateCookieHeaderAmbiguity(t *testing.T) {
	const adminSecret = "secret-admin-pass"
	handler, _ := newTestAdminHandler(t, adminSecret)

	// Log in to get cookie
	loginReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	cookie := loginRec.Result().Cookies()[0]

	// Send both Authorization header AND session cookie
	ambiguousReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/v1/keys", nil)
	ambiguousReq.Header.Set("Authorization", "Bearer "+adminSecret)
	ambiguousReq.AddCookie(cookie)
	ambiguousRec := httptest.NewRecorder()

	handler.ServeHTTP(ambiguousRec, ambiguousReq)
	if ambiguousRec.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous request status = %d; want 400", ambiguousRec.Code)
	}
	if !strings.Contains(ambiguousRec.Body.String(), "ambiguous_credentials") {
		t.Errorf("error body should mention ambiguous_credentials: %s", ambiguousRec.Body.String())
	}

	// Ambiguity on /admin/ui/v1/session as well
	sessionAmbiguous := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/ui/v1/session", nil)
	sessionAmbiguous.Header.Set("Authorization", "Bearer "+adminSecret)
	sessionAmbiguous.AddCookie(cookie)
	sessionRec := httptest.NewRecorder()
	handler.ServeHTTP(sessionRec, sessionAmbiguous)
	if sessionRec.Code != http.StatusBadRequest {
		t.Fatalf("session ambiguous status = %d; want 400", sessionRec.Code)
	}

	// Multiple Authorization headers -> 400
	multiAuthReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/v1/keys", nil)
	multiAuthReq.Header.Add("Authorization", "Bearer "+adminSecret)
	multiAuthReq.Header.Add("Authorization", "Bearer other-secret")
	multiAuthRec := httptest.NewRecorder()
	handler.ServeHTTP(multiAuthRec, multiAuthReq)
	if multiAuthRec.Code != http.StatusBadRequest {
		t.Fatalf("multiple Authorization headers status = %d; want 400", multiAuthRec.Code)
	}

	// Multiple session cookies -> 400
	multiCookieReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/v1/keys", nil)
	multiCookieReq.AddCookie(cookie)
	multiCookieReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: "other-session"})
	multiCookieRec := httptest.NewRecorder()
	handler.ServeHTTP(multiCookieRec, multiCookieReq)
	if multiCookieRec.Code != http.StatusBadRequest {
		t.Fatalf("multiple session cookies status = %d; want 400", multiCookieRec.Code)
	}

	// Multiple / conflicting CSRF tokens on unsafe method -> 400
	multiCsrfReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/v1/keys", strings.NewReader(`{"name":"csrf-test"}`))
	multiCsrfReq.Header.Set("Content-Type", "application/json")
	multiCsrfReq.Header.Set("Origin", "http://localhost:8080")
	multiCsrfReq.Header.Add("X-CSRF-Token", "token-1")
	multiCsrfReq.Header.Add("X-CSRF-Token", "token-2")
	multiCsrfReq.AddCookie(cookie)
	multiCsrfRec := httptest.NewRecorder()
	handler.ServeHTTP(multiCsrfRec, multiCsrfReq)
	if multiCsrfRec.Code != http.StatusBadRequest {
		t.Fatalf("multiple CSRF tokens status = %d; want 400", multiCsrfRec.Code)
	}
}

func TestFixationResistanceRotation(t *testing.T) {
	const adminSecret = "secret-admin-pass"
	handler, _ := newTestAdminHandler(t, adminSecret)

	// First login
	req1 := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	cookie1 := rec1.Result().Cookies()[0]

	// Second login providing the first cookie
	req2 := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.AddCookie(cookie1)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	cookie2 := rec2.Result().Cookies()[0]
	if cookie1.Value == cookie2.Value {
		t.Fatalf("session ID was not rotated on new login!")
	}

	// Old session must be invalid
	oldReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/ui/v1/session", nil)
	oldReq.AddCookie(cookie1)
	oldRec := httptest.NewRecorder()
	handler.ServeHTTP(oldRec, oldReq)
	var oldResp struct {
		Authenticated bool `json:"authenticated"`
	}
	_ = json.NewDecoder(oldRec.Body).Decode(&oldResp)
	if oldResp.Authenticated {
		t.Errorf("previous session should have been revoked")
	}

	// New session must be valid
	newReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/ui/v1/session", nil)
	newReq.AddCookie(cookie2)
	newRec := httptest.NewRecorder()
	handler.ServeHTTP(newRec, newReq)
	var newResp struct {
		Authenticated bool `json:"authenticated"`
	}
	_ = json.NewDecoder(newRec.Body).Decode(&newResp)
	if !newResp.Authenticated {
		t.Errorf("new session should be active")
	}
}

func TestIdleAndAbsoluteExpiry(t *testing.T) {
	fakeNow := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return fakeNow }

	store := session.NewStore(session.StoreOptions{
		IdleTimeout:     15 * time.Minute,
		AbsoluteTimeout: 1 * time.Hour,
		Now:             nowFn,
	})

	const adminSecret = "secret-admin-pass"
	handler, _ := newTestAdminHandler(t, adminSecret, func(h *adminHandler) {
		h.sessionStore = store
	})

	// Log in
	loginReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	cookie := loginRec.Result().Cookies()[0]

	// Advance 10 minutes (within idle timeout)
	fakeNow = fakeNow.Add(10 * time.Minute)
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/ui/v1/session", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var state struct {
		Authenticated bool `json:"authenticated"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&state)
	if !state.Authenticated {
		t.Fatalf("session should be active at 10m")
	}

	// Advance 16 minutes from 10m (idle timeout is 15m)
	fakeNow = fakeNow.Add(16 * time.Minute)
	idleReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/ui/v1/session", nil)
	idleReq.AddCookie(cookie)
	idleRec := httptest.NewRecorder()
	handler.ServeHTTP(idleRec, idleReq)
	_ = json.NewDecoder(idleRec.Body).Decode(&state)
	if state.Authenticated {
		t.Fatalf("session should have expired due to idle timeout")
	}

	// Verify cookie was cleared on expiry
	cleared := idleRec.Result().Cookies()
	if len(cleared) == 0 || cleared[0].MaxAge > 0 {
		t.Errorf("cookie should be cleared upon expired session lookup")
	}
}

func TestRestartSemantics(t *testing.T) {
	const adminSecret = "secret-admin-pass"
	handler1, _ := newTestAdminHandler(t, adminSecret)

	// Create session on server 1
	loginReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler1.ServeHTTP(loginRec, loginReq)
	cookie := loginRec.Result().Cookies()[0]

	// Simulate restart by creating handler2 with fresh in-memory session store
	handler2, _ := newTestAdminHandler(t, adminSecret)

	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/ui/v1/session", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler2.ServeHTTP(rec, req)

	var resp struct {
		Authenticated bool `json:"authenticated"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Authenticated {
		t.Fatalf("previous session should not survive restart")
	}
}

func TestBruteForceRateLimiting(t *testing.T) {
	const adminSecret = "correct-secret"
	rateLimiter := session.NewLoginRateLimiter(session.RateLimiterOptions{
		MaxTrackedIPs:   10,
		MaxAttempts:     3,
		Window:          5 * time.Minute,
		LockoutDuration: 10 * time.Minute,
	})

	handler, _ := newTestAdminHandler(t, adminSecret, func(h *adminHandler) {
		h.rateLimiter = rateLimiter
	})

	clientIP := "203.0.113.195:45678"

	// 3 failed login attempts
	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = clientIP
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d; want 401", i, rec.Code)
		}
		// Safe non-enumerating error message
		if !strings.Contains(rec.Body.String(), "Incorrect API key provided.") {
			t.Errorf("attempt %d should return generic non-enumerating error", i)
		}
	}

	// 4th attempt must be 429 Too Many Requests
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = clientIP
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited attempt status = %d; want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "rate_limit_exceeded") {
		t.Errorf("expected rate_limit_exceeded in error response")
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler, _ := newTestAdminHandler(t, "secret")

	// Check on session endpoint
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/ui/v1/session", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors 'none': %s", csp)
	}
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("CSP script-src should be 'self' without unsafe-inline: %s", csp)
	}
	if !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Errorf("CSP style-src missing unsafe-inline: %s", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing X-Content-Type-Options: nosniff")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("missing X-Frame-Options: DENY")
	}
	if rec.Header().Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
		t.Errorf("missing Referrer-Policy")
	}

	// Check on UI endpoint
	ui := newUIHandler(testUIMockFS())
	uiReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/ui/", nil)
	uiRec := httptest.NewRecorder()
	ui.ServeHTTP(uiRec, uiReq)

	uiCSP := uiRec.Header().Get("Content-Security-Policy")
	if !strings.Contains(uiCSP, "frame-ancestors 'none'") {
		t.Errorf("UI CSP missing frame-ancestors 'none'")
	}
	if !strings.Contains(uiCSP, "script-src 'self'") || strings.Contains(uiCSP, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("UI CSP script-src should be 'self' without unsafe-inline: %s", uiCSP)
	}
	if !strings.Contains(uiCSP, "style-src 'self' 'unsafe-inline'") {
		t.Errorf("UI CSP style-src missing unsafe-inline: %s", uiCSP)
	}
	if uiRec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("UI missing X-Content-Type-Options: nosniff")
	}
}

func TestTrustedProxySecureCookieHandling(t *testing.T) {
	proxies, err := session.ParseTrustedProxies([]string{"10.0.0.1"})
	if err != nil {
		t.Fatalf("parse proxies: %v", err)
	}

	const adminSecret = "secret-pass"
	handler, _ := newTestAdminHandler(t, adminSecret, func(h *adminHandler) {
		h.trustedProxies = proxies
	})

	// 1. Direct TLS request sets Secure
	tlsReq := httptest.NewRequest(http.MethodPost, "https://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	tlsReq.Header.Set("Content-Type", "application/json")
	tlsReq.TLS = &tls.ConnectionState{}
	tlsRec := httptest.NewRecorder()
	handler.ServeHTTP(tlsRec, tlsReq)
	cookie := tlsRec.Result().Cookies()[0]
	if !cookie.Secure {
		t.Errorf("cookie on direct TLS must be Secure")
	}

	// 2. Request from trusted proxy with X-Forwarded-Proto: https sets Secure
	proxyReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.RemoteAddr = "10.0.0.1:45678"
	proxyReq.Header.Set("X-Forwarded-Proto", "https")
	proxyRec := httptest.NewRecorder()
	handler.ServeHTTP(proxyRec, proxyReq)
	cookie = proxyRec.Result().Cookies()[0]
	if !cookie.Secure {
		t.Errorf("cookie from trusted proxy with X-Forwarded-Proto: https must be Secure")
	}

	// 3. Request from untrusted IP with forged X-Forwarded-Proto: https does NOT set Secure
	untrustedReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	untrustedReq.Header.Set("Content-Type", "application/json")
	untrustedReq.RemoteAddr = "192.168.1.50:45678"
	untrustedReq.Header.Set("X-Forwarded-Proto", "https")
	untrustedRec := httptest.NewRecorder()
	handler.ServeHTTP(untrustedRec, untrustedReq)
	cookie = untrustedRec.Result().Cookies()[0]
	if cookie.Secure {
		t.Errorf("cookie from untrusted remote IP must NOT be Secure despite X-Forwarded-Proto")
	}
}

func TestSessionCookieScopeAndPath(t *testing.T) {
	const adminSecret = "secret-pass"
	handler, _ := newTestAdminHandler(t, adminSecret)

	loginReq := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin/ui/v1/session", strings.NewReader(`{"credential":"`+adminSecret+`"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)

	var sessionCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == session.CookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("missing session cookie in login response")
	}

	// Exact Path must be /admin
	if sessionCookie.Path != "/admin" {
		t.Fatalf("cookie Path = %q; want exact /admin", sessionCookie.Path)
	}

	// RFC 6265 Section 5.1.4 Path-Match verification
	pathMatches := func(cookiePath, requestPath string) bool {
		if cookiePath == requestPath {
			return true
		}
		if strings.HasPrefix(requestPath, cookiePath) {
			if strings.HasSuffix(cookiePath, "/") {
				return true
			}
			if len(requestPath) > len(cookiePath) && requestPath[len(cookiePath)] == '/' {
				return true
			}
		}
		return false
	}

	// Must reach admin UI session endpoint
	if !pathMatches(sessionCookie.Path, "/admin/ui/v1/session") {
		t.Errorf("cookie Path %q must match /admin/ui/v1/session", sessionCookie.Path)
	}

	// Must reach admin API routes
	if !pathMatches(sessionCookie.Path, "/admin/v1/keys") {
		t.Errorf("cookie Path %q must match /admin/v1/keys", sessionCookie.Path)
	}
	if !pathMatches(sessionCookie.Path, "/admin/v1/requests") {
		t.Errorf("cookie Path %q must match /admin/v1/requests", sessionCookie.Path)
	}

	// Must NOT reach proxy routes
	if pathMatches(sessionCookie.Path, "/v1/chat/completions") {
		t.Errorf("cookie Path %q must NOT match proxy route /v1/chat/completions", sessionCookie.Path)
	}
	if pathMatches(sessionCookie.Path, "/v1/models") {
		t.Errorf("cookie Path %q must NOT match proxy route /v1/models", sessionCookie.Path)
	}

	// Must NOT reach UI static routes
	if pathMatches(sessionCookie.Path, "/ui/") {
		t.Errorf("cookie Path %q must NOT match /ui/", sessionCookie.Path)
	}
	if pathMatches(sessionCookie.Path, "/ui/overview") {
		t.Errorf("cookie Path %q must NOT match /ui/overview", sessionCookie.Path)
	}

	// Must NOT reach healthz
	if pathMatches(sessionCookie.Path, "/healthz") {
		t.Errorf("cookie Path %q must NOT match /healthz", sessionCookie.Path)
	}
}
