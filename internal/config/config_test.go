package config

import (
	"os"
	"path/filepath"
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
}
