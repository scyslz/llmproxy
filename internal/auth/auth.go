package auth

import (
	"strings"
	"sync"
	"time"

	"crypto/rand"
	"encoding/hex"
)

// MaxFailedAttempts is the number of failed logins allowed within the window
// before admin login is brute-force blocked.
const MaxFailedAttempts = 5

// FailWindow is the sliding window for failed login tracking.
const FailWindow = 10 * time.Minute

// Admin tracks login sessions and brute-force protection.
type Admin struct {
	mu       sync.Mutex
	sessions map[string]bool
	failures []time.Time
}

// NewAdmin creates an empty admin auth store.
func NewAdmin() *Admin {
	return &Admin{sessions: make(map[string]bool)}
}

// CreateSession issues a new random admin token.
func (a *Admin) CreateSession() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := "admin_sk_" + hex.EncodeToString(b)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions) > 1000 {
		a.sessions = make(map[string]bool)
	}
	a.sessions[token] = true
	return token
}

// Validate reports whether token is a live admin session.
func (a *Admin) Validate(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[token]
}

// Destroy removes a session token.
func (a *Admin) Destroy(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// Clear terminates all sessions.
func (a *Admin) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions = make(map[string]bool)
}

// RecordFailure adds a failed login attempt timestamp.
func (a *Admin) RecordFailure() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures = append(a.failures, time.Now())
	a.prune()
}

// Blocked reports whether login is currently locked out after too many failures.
func (a *Admin) Blocked() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prune()
	return len(a.failures) >= MaxFailedAttempts
}

// FailureCount returns the number of failures within the window.
func (a *Admin) FailureCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prune()
	return len(a.failures)
}

// ResetFailures clears the failure ledger.
func (a *Admin) ResetFailures() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures = nil
}

func (a *Admin) prune() {
	cutoff := time.Now().Add(-FailWindow)
	keep := a.failures[:0]
	for _, t := range a.failures {
		if !t.Before(cutoff) {
			keep = append(keep, t)
		}
	}
	a.failures = keep
}

// ExtractBearer returns the API key / token from an Authorization header,
// trimming a "Bearer " prefix like the legacy behavior.
func ExtractBearer(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return header
}