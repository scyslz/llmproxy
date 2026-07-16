package key

import (
	"fmt"
	"sync"
	"time"

	"llmproxy/config"
)

// Store 密钥存储
type Store struct {
	keys map[string]*config.VirtualKey
	mu   sync.RWMutex
}

// NewStore 创建存储
func NewStore() *Store {
	return &Store{
		keys: make(map[string]*config.VirtualKey),
	}
}

// Add 添加密钥
func (s *Store) Add(k *config.VirtualKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[k.ID] = k
}

// Validate 验证密钥，返回可用的 provider IDs
func (s *Store) Validate(keyID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	k, ok := s.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("invalid api key")
	}
	if !k.Enabled {
		return nil, fmt.Errorf("api key disabled")
	}

	// 速率限制检查
	if k.RateLimit > 0 {
		k.Mu.Lock()
		if time.Since(k.LastUsed) < time.Minute/time.Duration(k.RateLimit) {
			k.Mu.Unlock()
			return nil, fmt.Errorf("rate limit exceeded")
		}
		k.LastUsed = time.Now()
		k.Mu.Unlock()
	}

	return k.ProviderIDs, nil
}
