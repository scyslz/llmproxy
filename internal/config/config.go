package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llmproxy/internal/auth"
	"llmproxy/internal/domain"
)

// Manager holds the runtime configuration and guards all access with a lock.
// Every mutation persists an updated copy to disk immediately.
type Manager struct {
	mu             sync.RWMutex
	cfg            *domain.Config
	configFilePath string
	loadWarning    string
}

// LoadWarning reports a startup problem that forced a fallback to defaults.
// Empty means the config file was loaded as-is.
func (m *Manager) LoadWarning() string { return m.loadWarning }

// ConfigFile returns the path of the config file in use.
func (m *Manager) ConfigFile() string { return m.configFilePath }

// New loads the configuration from config/config.json relative to cwd, applying
// defaults and environment injection.
//
// Startup never fails because of file content: a missing, unreadable, malformed
// or invalid config falls back to the built-in defaults, keeps the original file
// as config.json.broken-<timestamp>, and records the reason in LoadWarning so the
// caller can surface it. Only I/O problems that prevent writing the config
// directory are returned as errors.
func New(configDir string) (*Manager, error) {
	path := filepath.Join(configDir, "config.json")
	cfg := defaultConfig()
	warning := ""

	if data, err := os.ReadFile(path); err == nil {
		loaded, loadErr := decodeConfig(data, cfg)
		if loadErr == nil {
			loaded = normalize(loaded)
			loadErr = validate(loaded)
			if loadErr == nil {
				cfg = loaded
			}
		}
		if loadErr != nil {
			kept := quarantine(path)
			warning = fmt.Sprintf("config %s could not be used (%v); started with defaults", path, loadErr)
			if kept != "" {
				warning += ", original kept as " + kept
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		warning = fmt.Sprintf("config %s is unreadable (%v); started with defaults", path, err)
	}

	injectEnv(cfg)
	cfg = normalize(cfg)
	if err := validate(cfg); err != nil {
		// Defaults are compiled in; failing here is a programming error.
		return nil, fmt.Errorf("config: built-in defaults are invalid: %w", err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	m := &Manager{cfg: cfg, configFilePath: path, loadWarning: warning}
	if warning != "" {
		// Persist what is actually running so the next start is clean.
		if err := writeFileSync(path, cfg); err != nil {
			m.loadWarning += fmt.Sprintf(" (and writing defaults failed: %v)", err)
		}
	}
	return m, nil
}

// decodeConfig parses data over base and returns the merged configuration.
func decodeConfig(data []byte, base *domain.Config) (*domain.Config, error) {
	var mapData map[string]json.RawMessage
	if err := json.Unmarshal(data, &mapData); err != nil {
		return nil, err
	}
	parsed := &rawConfig{}
	if err := json.Unmarshal(data, parsed); err != nil {
		return nil, err
	}
	return mergeConfig(base, parsed, mapData), nil
}

// quarantine moves an unusable config aside, preserving its content for the
// operator, and returns the new path (empty when it could not be kept).
func quarantine(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	target := fmt.Sprintf("%s.broken-%s", path, time.Now().UTC().Format("20060102-150405"))
	if err := os.Rename(path, target); err != nil {
		return ""
	}
	return target
}

// Get returns a deep copy of the current configuration.
func (m *Manager) Get() *domain.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneConfig(m.cfg)
}

// GetRef returns a snapshot copy, safe for handlers to read concurrently.
func (m *Manager) Settings() domain.Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.ToSettings()
}

// Update applies fn to a copy of the config and persists the result atomically.
func (m *Manager) Update(fn func(c *domain.Config)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := cloneConfig(m.cfg)
	fn(next)
	if err := writeFileSync(m.configFilePath, next); err != nil {
		return err
	}
	m.cfg = next
	return nil
}

// Replace writes the given config to disk and installs it as current.
func (m *Manager) Replace(next *domain.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := writeFileSync(m.configFilePath, next); err != nil {
		return err
	}
	m.cfg = next
	return nil
}

func writeFileSync(path string, cfg *domain.Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func defaultConfig() *domain.Config {
	return &domain.Config{
		Listen:           ":3000",
		EnableVirtualKey: false,
		EnableAdminAuth:  false,
		AdminPassword:    "admin",
		LogDetail:        "basic",
		LogBody:          false,
		MaxLogSizeMB:     2,
		MaxRequestLogs:   100000,
		ActiveLogFile:    1,
		MaxRequestBodyMB: 10,
		AllowedIPs:       []string{},
		Providers: []domain.Provider{
			{ID: "gemini", Name: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", APIKey: os.Getenv("GEMINI_API_KEY"), Enabled: true, Models: domain.Models("gemini-2.5-flash", "gemini-2.5-pro", "gemini-1.5-flash"), Timeout: 120000},
			{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com", Enabled: false, Models: domain.Models("gpt-4o", "gpt-4o-mini", "o1-mini"), Timeout: 120000},
			{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", Enabled: false, Models: domain.Models("deepseek-chat", "deepseek-reasoner"), Timeout: 120000},
		},
		Keys: []domain.VirtualKey{
			{Key: "sk-proxy-demo-key", Name: "Demo Virtual Key", ProviderIDs: []string{"gemini", "openai", "deepseek"}},
		},
	}
}

// rawConfig mirrors the JSON config allowing snake_case/Go-camel variants.
type rawConfig struct {
	Listen           *string       `json:"listen"`
	EnableVirtualKey *bool         `json:"enableVirtualKey"`
	EnableAdminAuth  *bool         `json:"enableAdminAuth"`
	AdminPassword    *string       `json:"adminPassword"`
	LogDetail        *string       `json:"logDetail"`
	LogBody          *bool         `json:"logBody"`
	MaxLogSizeMB     *int          `json:"maxLogSizeMB"`
	MaxRequestLogs   *int          `json:"maxRequestLogs"`
	ActiveLogFile    *int          `json:"activeLogFile"`
	MaxRequestBodyMB *int          `json:"maxRequestBodyMB"`
	AllowedIPs       []string      `json:"allowedIps"`
	Providers        []rawProvider `json:"providers"`
	Groups           []rawGroup    `json:"groups"`
	Keys             []rawKey      `json:"keys"`
}

type rawProvider struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	BaseURL           string         `json:"baseUrl"`
	APIKey            string         `json:"apiKey"`
	Enabled           *bool          `json:"enabled"`
	Models            []domain.Model `json:"models"`
	Concurrency       *int           `json:"concurrency"`
	Timeout           *int           `json:"timeout"`
	OpenAIEndpoint    string         `json:"openaiEndpoint"` // legacy alias, migrated to ChatEndpoint
	ChatEndpoint      string         `json:"chatEndpoint"`
	ResponsesEndpoint string         `json:"responsesEndpoint"`
	DefaultModel      string         `json:"defaultModel"`
	// Legacy/alternate field names kept for compatibility.
	BaseUrl2 string `json:"base_url"`
	APIKey2  string `json:"api_key"`
	OpenA2   string `json:"openai_endpoint"`
	Default2 string `json:"default_model"`
}

type rawKey struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	GroupID     string   `json:"groupId"`
	ProviderIDs []string `json:"providerIds"`
	CreatedAt   string   `json:"createdAt"`
}

type rawGroupEntry struct {
	ProviderID string   `json:"providerId"`
	Models     []string `json:"models,omitempty"`
}

type rawGroup struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Entries   []rawGroupEntry `json:"entries"`
	CreatedAt string          `json:"createdAt"`
}

func boolOr(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

func intOr(v *int, def int) int {
	if v == nil {
		return def
	}
	return *v
}

func mergeConfig(base *domain.Config, raw *rawConfig, m map[string]json.RawMessage) *domain.Config {
	out := *base
	if raw.Listen != nil {
		out.Listen = *raw.Listen
	}
	out.EnableVirtualKey = boolOr(raw.EnableVirtualKey, base.EnableVirtualKey)
	out.EnableAdminAuth = boolOr(raw.EnableAdminAuth, base.EnableAdminAuth)
	if raw.AdminPassword != nil {
		out.AdminPassword = *raw.AdminPassword
	}
	if raw.LogDetail != nil {
		out.LogDetail = *raw.LogDetail
	}
	out.LogBody = boolOr(raw.LogBody, base.LogBody)
	out.MaxLogSizeMB = intOr(raw.MaxLogSizeMB, base.MaxLogSizeMB)
	out.MaxRequestLogs = intOr(raw.MaxRequestLogs, base.MaxRequestLogs)
	out.ActiveLogFile = intOr(raw.ActiveLogFile, base.ActiveLogFile)
	out.MaxRequestBodyMB = intOr(raw.MaxRequestBodyMB, base.MaxRequestBodyMB)
	if _, ok := m["allowedIps"]; ok {
		if raw.AllowedIPs == nil {
			out.AllowedIPs = []string{}
		} else {
			out.AllowedIPs = raw.AllowedIPs
		}
	}
	if raw.Providers != nil {
		ps := make([]domain.Provider, 0, len(raw.Providers))
		for _, rp := range raw.Providers {
			p := rp.toProvider()
			ps = append(ps, p)
		}
		out.Providers = ps
	}
	if raw.Groups != nil {
		gs := make([]domain.ProviderGroup, 0, len(raw.Groups))
		for _, rg := range raw.Groups {
			gs = append(gs, domain.ProviderGroup{ID: rg.ID, Name: rg.Name, CreatedAt: rg.CreatedAt})
			for _, re := range rg.Entries {
				gs[len(gs)-1].Entries = append(gs[len(gs)-1].Entries, domain.GroupEntry{ProviderID: re.ProviderID, Models: re.Models})
			}
		}
		out.Groups = gs
	}
	if raw.Keys != nil {
		ks := make([]domain.VirtualKey, 0, len(raw.Keys))
		for _, rk := range raw.Keys {
			ks = append(ks, domain.VirtualKey{Key: rk.Key, Name: rk.Name, GroupID: rk.GroupID, ProviderIDs: rk.ProviderIDs, CreatedAt: rk.CreatedAt})
		}
		out.Keys = ks
	}
	return &out
}

func (rp rawProvider) toProvider() domain.Provider {
	p := domain.Provider{
		ID:                rp.ID,
		Name:              rp.Name,
		BaseURL:           orString(rp.BaseURL, rp.BaseUrl2),
		APIKey:            orString(rp.APIKey, rp.APIKey2),
		ChatEndpoint:      orString(rp.ChatEndpoint, orString(rp.OpenAIEndpoint, rp.OpenA2)),
		ResponsesEndpoint: rp.ResponsesEndpoint,
		DefaultModel:      orString(rp.DefaultModel, rp.Default2),
		Models:            domain.SanitizeModels(rp.Models),
	}
	if rp.Enabled != nil {
		p.Enabled = *rp.Enabled
	}
	p.Concurrency = intOr(rp.Concurrency, 0)
	p.Timeout = intOr(rp.Timeout, 0)
	return p
}

func orString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func injectEnv(cfg *domain.Config) {
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == "gemini" && cfg.Providers[i].APIKey == "" {
			if k := os.Getenv("GEMINI_API_KEY"); k != "" {
				cfg.Providers[i].APIKey = k
			}
		}
	}
}

func normalize(cfg *domain.Config) *domain.Config {
	if cfg.Listen == "" {
		cfg.Listen = ":3000"
	}
	if cfg.AdminPassword == "" {
		cfg.AdminPassword = "admin"
	}
	if cfg.Providers == nil {
		cfg.Providers = []domain.Provider{}
	}
	for i := range cfg.Providers {
		cfg.Providers[i].Models = domain.SanitizeModels(cfg.Providers[i].Models)
	}
	if cfg.Groups == nil {
		cfg.Groups = []domain.ProviderGroup{}
	}
	if cfg.Keys == nil {
		cfg.Keys = []domain.VirtualKey{}
	}
	if cfg.AllowedIPs == nil {
		cfg.AllowedIPs = []string{}
	}
	return cfg
}

// validate performs startup-time schema checks and returns an error for
// configuration that would fail at runtime or is clearly mistyped.
func validate(cfg *domain.Config) error {
	if !strings.HasPrefix(cfg.Listen, ":") {
		return fmt.Errorf("config: listen %q must start with ':' (e.g. :3000)", cfg.Listen)
	}
	switch cfg.LogDetail {
	case "", "off", "basic", "error", "all":
	default:
		return fmt.Errorf("config: logDetail %q must be one of off|basic|error|all", cfg.LogDetail)
	}
	if cfg.MaxLogSizeMB < 0 {
		return fmt.Errorf("config: maxLogSizeMB must be >= 0, got %d", cfg.MaxLogSizeMB)
	}
	if cfg.MaxRequestLogs < 0 {
		return fmt.Errorf("config: maxRequestLogs must be >= 0, got %d", cfg.MaxRequestLogs)
	}
	if cfg.MaxRequestBodyMB < 0 {
		return fmt.Errorf("config: maxRequestBodyMB must be >= 0, got %d", cfg.MaxRequestBodyMB)
	}
	seen := map[string]bool{}
	for _, p := range cfg.Providers {
		if p.ID == "" {
			return fmt.Errorf("config: provider %q missing id", p.Name)
		}
		if seen[p.ID] {
			return fmt.Errorf("config: duplicate provider id %q", p.ID)
		}
		seen[p.ID] = true
		if p.BaseURL == "" {
			return fmt.Errorf("config: provider %q missing baseUrl", p.ID)
		}
		if p.Timeout < 0 {
			return fmt.Errorf("config: provider %q timeout must be >= 0, got %d", p.ID, p.Timeout)
		}
		if p.Concurrency < 0 {
			return fmt.Errorf("config: provider %q concurrency must be >= 0, got %d", p.ID, p.Concurrency)
		}
		for _, m := range p.Models {
			if m.ID == "" {
				return fmt.Errorf("config: provider %q has a model without id", p.ID)
			}
			if m.ContextLength < 0 || m.MaxOutputTokens < 0 {
				return fmt.Errorf("config: provider %q model %q has negative limits", p.ID, m.ID)
			}
		}
	}
	seenGroups := map[string]bool{}
	for _, g := range cfg.Groups {
		if g.ID == "" {
			return fmt.Errorf("config: group %q missing id", g.Name)
		}
		if seenGroups[g.ID] {
			return fmt.Errorf("config: duplicate group id %q", g.ID)
		}
		seenGroups[g.ID] = true
	}
	for _, k := range cfg.Keys {
		if k.Key == "" {
			return fmt.Errorf("config: virtual key %q missing key", k.Name)
		}
	}
	normalized, err := auth.NormalizeAllowedIPs(cfg.AllowedIPs)
	if err != nil {
		return fmt.Errorf("config: allowedIps: %w", err)
	}
	cfg.AllowedIPs = normalized
	return nil
}

func cloneConfig(c *domain.Config) *domain.Config {
	if c == nil {
		return nil
	}
	b, _ := json.Marshal(c)
	out := &domain.Config{}
	_ = json.Unmarshal(b, out)
	return out
}
