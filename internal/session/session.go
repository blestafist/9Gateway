package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// CookieName is the opaque session identifier cookie.
	CookieName = "gw_session"

	// CookiePath scopes the session cookie strictly to administrative routes,
	// covering /admin/ui/v1/session and /admin/v1/* while excluding /v1/* proxy
	// endpoints, /ui/*, and health/metrics endpoints.
	CookiePath = "/admin"

	// DefaultMaxSessions caps in-memory session retention to avoid unbounded growth.
	DefaultMaxSessions = 256

	// DefaultIdleTimeout invalidates inactive sessions.
	DefaultIdleTimeout = 30 * time.Minute

	// DefaultAbsoluteTimeout forces re-authentication regardless of activity.
	DefaultAbsoluteTimeout = 12 * time.Hour

	// DefaultMaxTrackedIPs bounds the rate limiter's memory footprint.
	DefaultMaxTrackedIPs = 1024

	// DefaultMaxFailedAttempts before temporary IP lockout.
	DefaultMaxFailedAttempts = 5

	// DefaultLockoutDuration for rate-limited client IPs.
	DefaultLockoutDuration = 5 * time.Minute

	// DefaultRateLimitWindow for counting failed attempts.
	DefaultRateLimitWindow = 5 * time.Minute
)

// Session represents a cryptographically random, bounded in-memory session.
type Session struct {
	ID           string
	CSRFToken    string
	CreatedAt    time.Time
	LastActiveAt time.Time
}

// IsExpired checks whether the session has exceeded idle or absolute timeouts.
func (s *Session) IsExpired(now time.Time, idleTimeout, absoluteTimeout time.Duration) bool {
	if absoluteTimeout > 0 && now.Sub(s.CreatedAt) > absoluteTimeout {
		return true
	}
	if idleTimeout > 0 && now.Sub(s.LastActiveAt) > idleTimeout {
		return true
	}
	return false
}

// IdleExpiresAt returns the estimated expiration instant based on idle timeout.
func (s *Session) IdleExpiresAt(idleTimeout time.Duration) time.Time {
	return s.LastActiveAt.Add(idleTimeout)
}

// AbsoluteExpiresAt returns the hard ceiling expiration instant.
func (s *Session) AbsoluteExpiresAt(absoluteTimeout time.Duration) time.Time {
	return s.CreatedAt.Add(absoluteTimeout)
}

// StoreOptions configures the in-memory session store.
type StoreOptions struct {
	MaxSessions     int
	IdleTimeout     time.Duration
	AbsoluteTimeout time.Duration
	Now             func() time.Time
	RandReader      io.Reader
}

// Store provides thread-safe, bounded, in-memory session storage.
type Store struct {
	mu              sync.RWMutex
	sessions        map[string]*Session
	maxSessions     int
	idleTimeout     time.Duration
	absoluteTimeout time.Duration
	now             func() time.Time
	randReader      io.Reader
}

// NewStore initializes a new Store.
func NewStore(opts StoreOptions) *Store {
	maxSessions := opts.MaxSessions
	if maxSessions <= 0 {
		maxSessions = DefaultMaxSessions
	}
	idleTimeout := opts.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout
	}
	absoluteTimeout := opts.AbsoluteTimeout
	if absoluteTimeout <= 0 {
		absoluteTimeout = DefaultAbsoluteTimeout
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	randR := opts.RandReader
	if randR == nil {
		randR = rand.Reader
	}
	return &Store{
		sessions:        make(map[string]*Session),
		maxSessions:     maxSessions,
		idleTimeout:     idleTimeout,
		absoluteTimeout: absoluteTimeout,
		now:             nowFn,
		randReader:      randR,
	}
}

// IdleTimeout returns the configured idle timeout.
func (s *Store) IdleTimeout() time.Duration {
	return s.idleTimeout
}

// AbsoluteTimeout returns the configured absolute timeout.
func (s *Store) AbsoluteTimeout() time.Duration {
	return s.absoluteTimeout
}

// Create generates a cryptographically random session and stores it.
// It evicts expired sessions and enforces capacity bounds via LRU eviction.
func (s *Store) Create() (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.purgeExpiredLocked(now)

	// Enforce capacity bounds
	if len(s.sessions) >= s.maxSessions {
		s.evictOldestLocked()
	}

	sessionID, err := randomHex(s.randReader, 32)
	if err != nil {
		return nil, err
	}
	csrfToken, err := randomHex(s.randReader, 32)
	if err != nil {
		return nil, err
	}

	sess := &Session{
		ID:           sessionID,
		CSRFToken:    csrfToken,
		CreatedAt:    now,
		LastActiveAt: now,
	}
	s.sessions[sessionID] = sess
	return sess, nil
}

// Get retrieves a session and touches its LastActiveAt timestamp if valid.
func (s *Store) Get(id string) *Session {
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, exists := s.sessions[id]
	if !exists {
		return nil
	}
	now := s.now()
	if sess.IsExpired(now, s.idleTimeout, s.absoluteTimeout) {
		delete(s.sessions, id)
		return nil
	}
	sess.LastActiveAt = now
	return sess
}

// GetWithoutTouch retrieves a session without updating its activity timestamp.
func (s *Store) GetWithoutTouch(id string) *Session {
	if id == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, exists := s.sessions[id]
	if !exists {
		return nil
	}
	now := s.now()
	if sess.IsExpired(now, s.idleTimeout, s.absoluteTimeout) {
		return nil
	}
	return sess
}

// Revoke deletes a specific session by ID.
func (s *Store) Revoke(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// RevokeAll clears all active sessions.
func (s *Store) RevokeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]*Session)
}

// Count returns the number of active unexpired sessions.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.purgeExpiredLocked(now)
	return len(s.sessions)
}

// ValidateCSRF checks if the supplied CSRF token matches the session in constant time.
func (s *Store) ValidateCSRF(sessionID, csrfToken string) bool {
	if sessionID == "" || csrfToken == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, exists := s.sessions[sessionID]
	if !exists {
		return false
	}
	now := s.now()
	if sess.IsExpired(now, s.idleTimeout, s.absoluteTimeout) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(sess.CSRFToken), []byte(csrfToken)) == 1
}

func (s *Store) purgeExpiredLocked(now time.Time) {
	for id, sess := range s.sessions {
		if sess.IsExpired(now, s.idleTimeout, s.absoluteTimeout) {
			delete(s.sessions, id)
		}
	}
}

func (s *Store) evictOldestLocked() {
	var oldestID string
	var oldestTime time.Time
	first := true
	for id, sess := range s.sessions {
		if first || sess.LastActiveAt.Before(oldestTime) {
			oldestID = id
			oldestTime = sess.LastActiveAt
			first = false
		}
	}
	if oldestID != "" {
		delete(s.sessions, oldestID)
	}
}

func randomHex(r io.Reader, byteCount int) (string, error) {
	buf := make([]byte, byteCount)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// RateLimiterOptions configures the bounded login rate limiter.
type RateLimiterOptions struct {
	MaxTrackedIPs   int
	MaxAttempts     int
	Window          time.Duration
	LockoutDuration time.Duration
	Now             func() time.Time
}

type rateLimitEntry struct {
	count       int
	firstFailed time.Time
	lastFailed  time.Time
	lockedUntil time.Time
}

// LoginRateLimiter enforces bounded failed-login rate limiting without unbounded cardinality.
type LoginRateLimiter struct {
	mu              sync.Mutex
	failures        map[string]*rateLimitEntry
	maxTrackedIPs   int
	maxAttempts     int
	window          time.Duration
	lockoutDuration time.Duration
	now             func() time.Time
}

// NewLoginRateLimiter initializes a bounded failed-login rate limiter.
func NewLoginRateLimiter(opts RateLimiterOptions) *LoginRateLimiter {
	maxIPs := opts.MaxTrackedIPs
	if maxIPs <= 0 {
		maxIPs = DefaultMaxTrackedIPs
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxFailedAttempts
	}
	window := opts.Window
	if window <= 0 {
		window = DefaultRateLimitWindow
	}
	lockout := opts.LockoutDuration
	if lockout <= 0 {
		lockout = DefaultLockoutDuration
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	return &LoginRateLimiter{
		failures:        make(map[string]*rateLimitEntry),
		maxTrackedIPs:   maxIPs,
		maxAttempts:     maxAttempts,
		window:          window,
		lockoutDuration: lockout,
		now:             nowFn,
	}
}

// Allow reports whether a login attempt from this IP is permitted.
func (l *LoginRateLimiter) Allow(ip string) bool {
	if ip == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	entry, exists := l.failures[ip]
	if !exists {
		return true
	}
	if now.Before(entry.lockedUntil) {
		return false
	}
	// If lockout has elapsed or window has elapsed, entry is clean
	if now.Sub(entry.firstFailed) > l.window && now.After(entry.lockedUntil) {
		delete(l.failures, ip)
		return true
	}
	return true
}

// RecordFailure records a failed login attempt for the given IP.
func (l *LoginRateLimiter) RecordFailure(ip string) {
	if ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	// Purge expired entries if approaching cardinality limit
	if len(l.failures) >= l.maxTrackedIPs {
		l.purgeExpiredLocked(now)
		// If still full, evict the entry with the oldest lastFailed
		if len(l.failures) >= l.maxTrackedIPs {
			l.evictOldestLocked()
		}
	}

	entry, exists := l.failures[ip]
	if !exists || now.Sub(entry.firstFailed) > l.window {
		l.failures[ip] = &rateLimitEntry{
			count:       1,
			firstFailed: now,
			lastFailed:  now,
		}
		return
	}

	entry.count++
	entry.lastFailed = now
	if entry.count >= l.maxAttempts {
		entry.lockedUntil = now.Add(l.lockoutDuration)
	}
}

// RecordSuccess clears any failure record for the IP upon successful authentication.
func (l *LoginRateLimiter) RecordSuccess(ip string) {
	if ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, ip)
}

func (l *LoginRateLimiter) purgeExpiredLocked(now time.Time) {
	for ip, entry := range l.failures {
		if now.After(entry.lockedUntil) && now.Sub(entry.firstFailed) > l.window {
			delete(l.failures, ip)
		}
	}
}

func (l *LoginRateLimiter) evictOldestLocked() {
	var oldestIP string
	var oldestTime time.Time
	first := true
	for ip, entry := range l.failures {
		if first || entry.lastFailed.Before(oldestTime) {
			oldestIP = ip
			oldestTime = entry.lastFailed
			first = false
		}
	}
	if oldestIP != "" {
		delete(l.failures, oldestIP)
	}
}

// ParseTrustedProxies parses IP or CIDR strings into []*net.IPNet for proxy verification.
func ParseTrustedProxies(proxies []string) ([]*net.IPNet, error) {
	var result []*net.IPNet
	for _, p := range proxies {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "/") {
			_, ipNet, err := net.ParseCIDR(trimmed)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", trimmed, err)
			}
			result = append(result, ipNet)
		} else {
			ip := net.ParseIP(trimmed)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP address %q", trimmed)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			mask := net.CIDRMask(bits, bits)
			result = append(result, &net.IPNet{IP: ip, Mask: mask})
		}
	}
	return result, nil
}

// ClientIPFromRemoteAddr extracts the bare IP string from a RemoteAddr (host:port).
func ClientIPFromRemoteAddr(remoteAddr string) string {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// IsSecureRequest determines whether a request is HTTPS, inspecting X-Forwarded-Proto
// ONLY if the remote IP matches a configured trusted proxy.
func IsSecureRequest(r *http.Request, trustedProxies []*net.IPNet) bool {
	if r.TLS != nil {
		return true
	}
	if len(trustedProxies) == 0 {
		return false
	}
	clientIP := net.ParseIP(ClientIPFromRemoteAddr(r.RemoteAddr))
	if clientIP == nil {
		return false
	}
	isTrusted := false
	for _, ipNet := range trustedProxies {
		if ipNet.Contains(clientIP) {
			isTrusted = true
			break
		}
	}
	if !isTrusted {
		return false
	}
	proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	return proto == "https"
}

// SetSessionCookie sets an opaque HttpOnly SameSite=Strict cookie scoped to CookiePath (/admin).
func SetSessionCookie(w http.ResponseWriter, r *http.Request, sessionID string, maxAge int, trustedProxies []*net.IPNet) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sessionID,
		Path:     CookiePath,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
		Secure:   IsSecureRequest(r, trustedProxies),
	})
}

// ClearSessionCookie clears the session cookie with immediate expiration.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request, trustedProxies []*net.IPNet) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     CookiePath,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		Secure:   IsSecureRequest(r, trustedProxies),
	})
}

var ErrSessionNotFound = errors.New("session not found")
