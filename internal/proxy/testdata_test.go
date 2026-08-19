package proxy

import "llmproxy/internal/domain"

func testConfig() *domain.Config {
	return &domain.Config{
		Listen:           ":3000",
		EnableVirtualKey: true,
		Providers: []domain.Provider{
			{ID: "mock", Name: "Mock Upstream", BaseURL: "http://127.0.0.1:9100", APIKey: "up-key", Enabled: true, Models: []string{"1234"}, DefaultModel: "1234", Timeout: 5000},
			{ID: "disabled", Name: "Disabled", BaseURL: "http://127.0.0.1:9999", Enabled: false},
			{ID: "backup", Name: "Backup", BaseURL: "http://127.0.0.1:9101", APIKey: "bk", Enabled: true, Models: []string{"other"}, Timeout: 5000},
		},
		Keys: []domain.VirtualKey{
			{Key: "sk-test-bound", Name: "Bound Key", ProviderIDs: []string{"mock"}},
			{Key: "sk-test-all", Name: "All Key", ProviderIDs: []string{"all"}},
		},
	}
}
