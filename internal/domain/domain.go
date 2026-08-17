package domain

// Provider describes a single upstream LLM provider.
type Provider struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	BaseURL        string   `json:"baseUrl"`
	APIKey         string   `json:"apiKey,omitempty"`
	Enabled        bool     `json:"enabled"`
	Models         []string `json:"models,omitempty"`
	Concurrency    int      `json:"concurrency,omitempty"` // 0 or unset = unlimited
	Timeout        int      `json:"timeout,omitempty"`     // upstream request timeout in ms, 0 or unset = none
	OpenAIEndpoint string   `json:"openaiEndpoint,omitempty"`
	DefaultModel   string   `json:"defaultModel,omitempty"`
}

// VirtualKey maps a client-facing key to a set of authorized providers.
type VirtualKey struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	ProviderIDs []string `json:"providerIds,omitempty"`
	CreatedAt   string   `json:"createdAt"`
}

// Settings is the subset of config exposed to the dashboard settings API.
type Settings struct {
	EnableVirtualKey bool   `json:"enableVirtualKey"`
	EnableAdminAuth  bool   `json:"enableAdminAuth"`
	LogDetail        string `json:"logDetail"`
	LogBody          bool   `json:"logBody"`
	MaxLogSizeMB     int    `json:"maxLogSizeMB"`
	MaxRequestLogs   int    `json:"maxRequestLogs"`
	ActiveLogFile    int    `json:"activeLogFile"`
}

// Config is the runtime configuration persisted to config/config.json.
type Config struct {
	Listen           string       `json:"listen"`
	EnableVirtualKey bool         `json:"enableVirtualKey"`
	EnableAdminAuth  bool         `json:"enableAdminAuth"`
	AdminPassword    string       `json:"adminPassword,omitempty"`
	LogDetail        string       `json:"logDetail"`
	LogBody          bool         `json:"logBody"`
	MaxLogSizeMB     int          `json:"maxLogSizeMB"`
	MaxRequestLogs   int          `json:"maxRequestLogs"`
	ActiveLogFile    int          `json:"activeLogFile"`
	Providers        []Provider   `json:"providers"`
	Keys             []VirtualKey `json:"keys"`
}

// ToSettings converts the config into the dashboard-facing settings object.
func (c *Config) ToSettings() Settings {
	return Settings{
		EnableVirtualKey: c.EnableVirtualKey,
		EnableAdminAuth:  c.EnableAdminAuth,
		LogDetail:        orString(c.LogDetail, "basic"),
		LogBody:          c.LogBody,
		MaxLogSizeMB:     orInt(c.MaxLogSizeMB, 2),
		MaxRequestLogs:   orInt(c.MaxRequestLogs, 10000),
		ActiveLogFile:    orInt(c.ActiveLogFile, 1),
	}
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