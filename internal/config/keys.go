package config

import (
	"crypto/rand"
	"fmt"
	"strings"

	"llmproxy/internal/domain"
)

// MaxVirtualKeyLen bounds a virtual key, including its mandatory prefix.
const MaxVirtualKeyLen = 128

// validKeyRunes reports whether every character of s may appear in a virtual
// key. The set is intentionally conservative (no spaces, slashes or quotes) so
// keys survive Authorization headers, shell quoting and dashboard URLs
// unchanged.
func validKeyRunes(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// EnsureKeyPrefix normalizes a caller-supplied virtual key: surrounding
// whitespace is trimmed and the fixed domain.DefaultKeyPrefix is prepended when
// the caller did not supply it. A key that already carries the prefix is kept
// exactly as given, so the prefix is never duplicated.
func EnsureKeyPrefix(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("key must not be empty")
	}
	if !strings.HasPrefix(s, domain.DefaultKeyPrefix) {
		s = domain.DefaultKeyPrefix + s
	}
	if len(s) > MaxVirtualKeyLen {
		return "", fmt.Errorf("key must be at most %d characters (including the %q prefix)", MaxVirtualKeyLen, domain.DefaultKeyPrefix)
	}
	if !validKeyRunes(s) {
		return "", fmt.Errorf("key may only contain letters, digits, '-', '_' and '.'")
	}
	return s, nil
}

// GenerateKey builds a random virtual key with the fixed prefix.
func GenerateKey() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	const bodyLen = 32
	buf := make([]byte, bodyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return domain.DefaultKeyPrefix + string(buf), nil
}
