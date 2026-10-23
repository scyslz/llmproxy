package auth

import (
	"fmt"
	"net"
	"net/http"
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

func normalizeIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return normalizeIP(ip).String()
}

func parseIPLiteral(s string) net.IP {
	return normalizeIP(net.ParseIP(strings.TrimSpace(s)))
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := parseIPLiteral(host); ip != nil {
		return ip.String()
	}
	return strings.TrimSpace(host)
}

// ClientIP returns the request client address used for allowlist matching.
// When trustProxy is false only the TCP peer is used. When true, X-Real-IP
// is preferred, then the leftmost valid X-Forwarded-For hop. If trustProxy is
// true but no header yields a valid IP, an empty string is returned so callers
// can reject instead of falling back to the spoofable proxy address.
func ClientIP(r *http.Request, trustProxy bool) string {
	if r == nil {
		return ""
	}
	if trustProxy {
		if ip := parseIPLiteral(r.Header.Get("X-Real-IP")); ip != nil {
			return ip.String()
		}
		for _, part := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
			if ip := parseIPLiteral(part); ip != nil {
				return ip.String()
			}
		}
		return ""
	}
	return remoteIP(r)
}

// ProxyFlagPresent reports whether the reverse-proxy marker header carries the
// expected secret value. An empty configured value disables flag checking.
func ProxyFlagPresent(r *http.Request, header, value string) bool {
	if header == "" {
		return false
	}
	got := strings.TrimSpace(r.Header.Get(header))
	if value == "" {
		// No secret configured: require the header to exist and be non-empty.
		return got != ""
	}
	return got == value
}

// IPAllowed reports whether ipStr matches any exact IP or CIDR in cidrs.
// An empty list never matches.
func IPAllowed(ipStr string, cidrs []string) bool {
	if len(cidrs) == 0 {
		return false
	}
	ip := parseIPLiteral(ipStr)
	if ip == nil {
		return false
	}
	for _, item := range cidrs {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(item); err == nil {
			if n.Contains(ip) {
				return true
			}
			continue
		}
		if p := parseIPLiteral(item); p != nil && p.Equal(ip) {
			return true
		}
	}
	return false
}

// NormalizeAllowedIPs trims blanks and accepts only IP or CIDR entries.
func NormalizeAllowedIPs(items []string) ([]string, error) {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(item); err == nil {
			out = append(out, n.String())
			continue
		}
		if ip := parseIPLiteral(item); ip != nil {
			out = append(out, ip.String())
			continue
		}
		return nil, fmt.Errorf("invalid IP or CIDR: %s", item)
	}
	return out, nil
}