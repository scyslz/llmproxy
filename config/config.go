package config

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Provider 提供商配置
type Provider struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	BaseURL string   `json:"base_url"`
	APIKey  string   `json:"api_key"`
	Models  []string `json:"models"`
	Enabled bool     `json:"enabled"`
}

// VirtualKey 虚拟密钥配置
type VirtualKey struct {
	ID          string   `json:"id"`
	ProviderIDs []string `json:"provider_ids"`
	RateLimit   int      `json:"rate_limit"`
	Enabled     bool     `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`

	// 运行时字段（不序列化）
	LastUsed time.Time `json:"-"`
	Mu       sync.Mutex `json:"-"`
}

// Config 配置
type Config struct {
	Listen         string      `json:"listen"`
	Debug          bool        `json:"debug"`
	EnableVirtualKey bool      `json:"enable_virtual_key"`
	Providers      []Provider  `json:"providers"`
	VirtualKeys    []VirtualKey `json:"virtual_keys"`
}

// Loader 配置加载器
type Loader struct {
	path string
}

// NewLoader 创建加载器
func NewLoader(path string) *Loader {
	return &Loader{path: path}
}

// Load 加载配置
func (l *Loader) Load(cfg *Config) error {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, cfg)
}

// Save 保存配置
func (l *Loader) Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(l.path, data, 0644)
}
