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

func TestNewLoadsModelMetadata(t *testing.T) {
	dir := writeConfig(t, `{
		"listen": ":3000",
		"providers": [{
			"id": "p", "name": "P", "baseUrl": "http://p",
			"models": [
				{"id": "m1", "contextLength": 128000, "maxOutputTokens": 16384},
				{"id": "m1", "contextLength": 1},
				{"id": " m2 ", "protocol": "responses"},
				{"id": "m3", "protocol": "bogus"}
			]
		}]
	}`)
	m, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	models := m.Get().Providers[0].Models
	if len(models) != 3 {
		t.Fatalf("models = %+v, want 3", models)
	}
	if models[0].ID != "m1" || models[0].ContextLength != 128000 || models[0].MaxOutputTokens != 16384 {
		t.Fatalf("first model = %+v", models[0])
	}
	if models[1].ID != "m2" || models[1].Protocol != "responses" {
		t.Fatalf("second model = %+v", models[1])
	}
	if models[2].Protocol != "" {
		t.Fatalf("invalid protocol was kept: %+v", models[2])
	}
}

func TestNewMigratesLegacyStringModelList(t *testing.T) {
	dir := writeConfig(t, `{"listen":":3000","providers":[{"id":"p","name":"P","baseUrl":"http://p",
		"models":["gpt-4o"," gpt-4o-mini ","gpt-4o"]}]}`)
	m, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w := m.LoadWarning(); w != "" {
		t.Fatalf("legacy models should load cleanly, got warning: %s", w)
	}
	models := m.Get().Providers[0].Models
	if len(models) != 2 || models[0].ID != "gpt-4o" || models[1].ID != "gpt-4o-mini" {
		t.Fatalf("models = %+v, want legacy ids migrated and deduped", models)
	}
}

func TestNewFallsBackToDefaultsOnUnusableConfig(t *testing.T) {
	cases := map[string]string{
		"bad logDetail":       `{"logDetail":"verbose"}`,
		"bad listen":          `{"listen":"localhost:3000"}`,
		"provider without id": `{"providers":[{"name":"A","baseUrl":"http://a"}]}`,
		"duplicate provider":  `{"providers":[{"id":"a","baseUrl":"http://a"},{"id":"a","baseUrl":"http://b"}]}`,
		"provider no baseUrl": `{"providers":[{"id":"a","name":"A"}]}`,
		"negative timeout":    `{"providers":[{"id":"a","baseUrl":"http://a","timeout":-5}]}`,
		"key without value":   `{"keys":[{"name":"nokey"}]}`,
		"negative body limit": `{"listen":":3000","maxRequestBodyMB":-1}`,
		"invalid allowed ip":  `{"listen":":3000","allowedIps":["nope"]}`,
		"not json":            `{ this is not json`,
		"wrong types":         `{"providers":"nope"}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeConfig(t, content)

			// Startup must succeed no matter how broken the file is.
			m, err := New(dir)
			if err != nil {
				t.Fatalf("New returned error, startup would fail: %v", err)
			}
			if m.LoadWarning() == "" {
				t.Fatal("expected a LoadWarning explaining the fallback")
			}
			cfg := m.Get()
			if cfg.Listen != ":3000" || cfg.LogDetail != "basic" {
				t.Fatalf("did not fall back to defaults: listen=%q logDetail=%q", cfg.Listen, cfg.LogDetail)
			}

			// The original content is preserved and a valid config is written back.
			kept, err := filepath.Glob(filepath.Join(dir, "config.json.broken-*"))
			if err != nil || len(kept) != 1 {
				t.Fatalf("broken file not quarantined: %v %v", kept, err)
			}
			raw, _ := os.ReadFile(kept[0])
			if string(raw) != content {
				t.Fatalf("quarantined file changed: %q", raw)
			}
			reloaded, err := New(dir)
			if err != nil {
				t.Fatalf("reload after repair: %v", err)
			}
			if reloaded.LoadWarning() != "" {
				t.Fatalf("repaired config should load cleanly: %s", reloaded.LoadWarning())
			}
		})
	}
}
