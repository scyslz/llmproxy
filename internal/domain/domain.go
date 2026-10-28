package domain

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Model is one model a provider serves, together with the metadata the proxy
// needs for it: context window, max output tokens, and the upstream protocol it
// speaks. Protocol is empty when it is still unknown and must be auto-probed.
type Model struct {
	ID              string `json:"id"`
	ContextLength   int    `json:"contextLength,omitempty"`
	MaxOutputTokens int    `json:"maxOutputTokens,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
}

// UnmarshalJSON accepts both the object form and the legacy bare-id form
// ("gpt-4o"), which older configs and API clients still use. A bare id becomes
// a Model with no limits and no pinned protocol.
func (m *Model) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*m = Model{}
		return nil
	}
	if trimmed[0] == '"' {
		var id string
		if err := json.Unmarshal(trimmed, &id); err != nil {
			return err
		}
		*m = Model{ID: id}
		return nil
	}
	type plain Model // avoid recursing into this method
	var aux plain
	if err := json.Unmarshal(trimmed, &aux); err != nil {
		return err
	}
	*m = Model(aux)
	return nil
}

// Models builds a model list from bare ids. It keeps test fixtures and defaults
// readable when no limits are known yet.
func Models(ids ...string) []Model {
	out := make([]Model, 0, len(ids))
	for _, id := range ids {
		out = append(out, Model{ID: id})
	}
	return out
}

// SanitizeModels trims model ids, drops blanks and duplicates (first occurrence
// wins, preserving fallback order) and clamps negative limits to zero.
func SanitizeModels(in []Model) []Model {
	out := make([]Model, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, m := range in {
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		if m.ContextLength < 0 {
			m.ContextLength = 0
		}
		if m.MaxOutputTokens < 0 {
			m.MaxOutputTokens = 0
		}
		switch m.Protocol {
		case "chat", "responses":
		default:
			m.Protocol = ""
		}
		out = append(out, m)
	}
	return out
}

// Provider describes a single upstream LLM provider.
type Provider struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	BaseURL           string  `json:"baseUrl"`
	APIKey            string  `json:"apiKey,omitempty"`
	Enabled           bool    `json:"enabled"`
	Models            []Model `json:"models,omitempty"`
	Concurrency       int     `json:"concurrency,omitempty"` // 0 or unset = unlimited
	Timeout           int     `json:"timeout,omitempty"`     // upstream request timeout in ms, 0 or unset = none
	ChatEndpoint      string  `json:"chatEndpoint,omitempty"`
	ResponsesEndpoint string  `json:"responsesEndpoint,omitempty"`
	DefaultModel      string  `json:"defaultModel,omitempty"`
	// Protocol is the provider-wide default: chat (/v1/chat/completions) or
	// responses (/v1/responses). Empty means unknown; each model can override it
	// through Model.Protocol, which the 404 probe fills in automatically.
	Protocol string `json:"protocol,omitempty"`
}

// ModelByID returns the model entry with the given id, or nil.
func (p *Provider) ModelByID(id string) *Model {
	for i := range p.Models {
		if p.Models[i].ID == id {
			return &p.Models[i]
		}
	}
	return nil
}

// ModelIDs returns the model ids in fallback order.
func (p *Provider) ModelIDs() []string {
	out := make([]string, 0, len(p.Models))
	for _, m := range p.Models {
		out = append(out, m.ID)
	}
	return out
}

// DefaultModelID returns defaultModel, or the first configured model.
func (p *Provider) DefaultModelID() string {
	if p.DefaultModel != "" {
		return p.DefaultModel
	}
	if len(p.Models) > 0 {
		return p.Models[0].ID
	}
	return ""
}

// DefaultKeyPrefix is the fixed prefix of every virtual key. Generated keys
// always start with it, and caller-supplied keys get it prepended when missing.
const DefaultKeyPrefix = "sk-proxy-"

// VirtualKey maps a client-facing key to a set of authorized providers.
type VirtualKey struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	GroupID     string   `json:"groupId,omitempty"`
	ProviderIDs []string `json:"providerIds,omitempty"`
	CreatedAt   string   `json:"createdAt"`
}

// GroupEntry is one link in a provider fallback chain, at provider+models granularity.
type GroupEntry struct {
	ProviderID string   `json:"providerId"`
	Models     []string `json:"models,omitempty"` // empty = use provider all models
}

// ProviderGroup is an ordered set of provider/model entries used as a fallback chain.
type ProviderGroup struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Entries   []GroupEntry `json:"entries"`
	CreatedAt string       `json:"createdAt"`
}

// Settings is the subset of config exposed to the dashboard settings API.
type Settings struct {
	EnableVirtualKey bool     `json:"enableVirtualKey"`
	EnableAdminAuth  bool     `json:"enableAdminAuth"`
	LogDetail        string   `json:"logDetail"`
	LogBody          bool     `json:"logBody"`
	MaxLogSizeMB     int      `json:"maxLogSizeMB"`
	MaxRequestLogs   int      `json:"maxRequestLogs"`
	ActiveLogFile    int      `json:"activeLogFile"`
	MaxRequestBodyMB int      `json:"maxRequestBodyMB"`
	AllowedIPs       []string `json:"allowedIps"`
}

// Config is the runtime configuration persisted to config/config.json.
type Config struct {
	Listen           string          `json:"listen"`
	EnableVirtualKey bool            `json:"enableVirtualKey"`
	EnableAdminAuth  bool            `json:"enableAdminAuth"`
	AdminPassword    string          `json:"adminPassword,omitempty"`
	LogDetail        string          `json:"logDetail"`
	LogBody          bool            `json:"logBody"`
	MaxLogSizeMB     int             `json:"maxLogSizeMB"`
	MaxRequestLogs   int             `json:"maxRequestLogs"`
	ActiveLogFile    int             `json:"activeLogFile"`
	MaxRequestBodyMB int             `json:"maxRequestBodyMB"`
	AllowedIPs       []string        `json:"allowedIps,omitempty"`
	Providers        []Provider      `json:"providers"`
	Groups           []ProviderGroup `json:"groups,omitempty"`
	Keys             []VirtualKey    `json:"keys"`
}

// ToSettings converts the config into the dashboard-facing settings object.
func (c *Config) ToSettings() Settings {
	return Settings{
		EnableVirtualKey: c.EnableVirtualKey,
		EnableAdminAuth:  c.EnableAdminAuth,
		LogDetail:        orString(c.LogDetail, "basic"),
		LogBody:          c.LogBody,
		MaxLogSizeMB:     orInt(c.MaxLogSizeMB, 2),
		MaxRequestLogs:   orInt(c.MaxRequestLogs, 100000),
		ActiveLogFile:    orInt(c.ActiveLogFile, 1),
		MaxRequestBodyMB: orInt(c.MaxRequestBodyMB, 10),
		AllowedIPs:       orStringSlice(c.AllowedIPs),
	}
}

func orStringSlice(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func orString(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func orInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
