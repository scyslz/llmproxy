package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

func TestNewValidConfig(t *testing.T) {
	dir := writeConfig(t, `{
		"listen": ":4000",
		"logDetail": "all",
		"maxLogSizeMB": 10,
		"providers": [{"id":"a","name":"A","baseUrl":"http://a","apiKey":"k","enabled":true}],
		"keys": [{"key":"sk-proxy-x","name":"x"}]
	}`)
	m, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cfg := m.Get()
	if cfg.Listen != ":4000" || cfg.LogDetail != "all" {
		t.Fatalf("config mismatch: %+v", cfg)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Providers))
	}
}

func TestNewRejectsBadLogDetail(t *testing.T) {
	dir := writeConfig(t, `{"logDetail":"verbose"}`)
	if _, err := New(dir); err == nil {
		t.Fatal("expected error for invalid logDetail")
	}
}

func TestNewRejectsBadListen(t *testing.T) {
	dir := writeConfig(t, `{"listen":"localhost:3000"}`)
	if _, err := New(dir); err == nil {
		t.Fatal("expected error for listen without colon")
	}
}

func TestNewRejectsProviderWithoutID(t *testing.T) {
	dir := writeConfig(t, `{"providers":[{"name":"A","baseUrl":"http://a"}]}`)
	if _, err := New(dir); err == nil {
		t.Fatal("expected error for provider without id")
	}
}

func TestNewRejectsDuplicateProviderID(t *testing.T) {
	dir := writeConfig(t, `{"providers":[
		{"id":"a","baseUrl":"http://a"},
		{"id":"a","baseUrl":"http://b"}
	]}`)
	if _, err := New(dir); err == nil {
		t.Fatal("expected error for duplicate provider id")
	}
}

func TestNewRejectsMissingBaseURL(t *testing.T) {
	dir := writeConfig(t, `{"providers":[{"id":"a","name":"A"}]}`)
	if _, err := New(dir); err == nil {
		t.Fatal("expected error for provider without baseUrl")
	}
}

func TestNewRejectsNegativeTimeout(t *testing.T) {
	dir := writeConfig(t, `{"providers":[{"id":"a","baseUrl":"http://a","timeout":-5}]}`)
	if _, err := New(dir); err == nil {
		t.Fatal("expected error for negative timeout")
	}
}

func TestNewRejectsEmptyKey(t *testing.T) {
	dir := writeConfig(t, `{"keys":[{"name":"nokey"}]}`)
	if _, err := New(dir); err == nil {
		t.Fatal("expected error for key without key value")
	}
}

func TestNewFallsBackToDefaultsWithoutFile(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cfg := m.Get()
	if cfg.Listen != ":3000" {
		t.Fatalf("default listen = %q", cfg.Listen)
	}
	if cfg.LogDetail != "basic" {
		t.Fatalf("default logDetail = %q", cfg.LogDetail)
	}
	if cfg.MaxRequestBodyMB != 10 {
		t.Fatalf("default maxRequestBodyMB = %d, want 10", cfg.MaxRequestBodyMB)
	}
}

func TestNewLoadsMaxRequestBodyMB(t *testing.T) {
	dir := writeConfig(t, `{
		"listen": ":3000",
		"maxRequestBodyMB": 25,
		"providers": [{"id":"a","name":"A","baseUrl":"http://a","apiKey":"k","enabled":true}]
	}`)
	m, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cfg := m.Get()
	if cfg.MaxRequestBodyMB != 25 {
		t.Fatalf("maxRequestBodyMB = %d, want 25", cfg.MaxRequestBodyMB)
	}
}

func TestNewRejectsNegativeMaxRequestBodyMB(t *testing.T) {
	dir := writeConfig(t, `{"listen":":3000","maxRequestBodyMB":-1}`)
	if _, err := New(dir); err == nil {
		t.Fatal("expected error for negative maxRequestBodyMB")
	}
}

func TestNewRejectsInvalidAllowedIP(t *testing.T) {
	dir := writeConfig(t, `{"listen":":3000","allowedIps":["nope"]}`)
	if _, err := New(dir); err == nil {
		t.Fatal("expected error for invalid allowedIps")
	}
}

func TestNewIgnoresKeyPrefixSetting(t *testing.T) {
	// keyPrefix is no longer configurable: old configs must still load, and the
	// prefix in the file must have no effect on existing keys.
	dir := writeConfig(t, `{"listen":":3000","keyPrefix":"sk-mine-","keys":[{"key":"sk-proxy-old","name":"old"}]}`)
	m, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cfg := m.Get()
	if len(cfg.Keys) != 1 || cfg.Keys[0].Key != "sk-proxy-old" {
		t.Fatalf("existing key was modified: %+v", cfg.Keys)
	}
	blob, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), "keyPrefix") {
		t.Fatal("keyPrefix should not be persisted any more")
	}
}

func TestEnsureKeyPrefix(t *testing.T) {
	cases := map[string]string{
		"team-abc":            "sk-proxy-team-abc",
		"sk-proxy-team-abc":   "sk-proxy-team-abc",
		"  sk-proxy-spaced  ": "sk-proxy-spaced",
		"My_Key.1":            "sk-proxy-My_Key.1",
	}
	for in, want := range cases {
		got, err := EnsureKeyPrefix(in)
		if err != nil {
			t.Fatalf("EnsureKeyPrefix(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("EnsureKeyPrefix(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "   ", "has space", "slash/key", "quote\"key"} {
		if _, err := EnsureKeyPrefix(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
	// Prefix is not duplicated when the value is exactly the prefix.
	if got, err := EnsureKeyPrefix("sk-proxy-"); err != nil || got != "sk-proxy-" {
		t.Fatalf("EnsureKeyPrefix(sk-proxy-) = %q, %v", got, err)
	}
}

func TestGenerateKeyHasFixedPrefix(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(k) <= len("sk-proxy-") || k[:len("sk-proxy-")] != "sk-proxy-" {
		t.Fatalf("generated key %q missing fixed prefix", k)
	}
}
