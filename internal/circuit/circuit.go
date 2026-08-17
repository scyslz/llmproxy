package circuit

import (
	"sync"
	"time"
)

const (
	// CooldownBaseMS is the initial cooldown after the first failure.
	CooldownBaseMS = 15000
	// CooldownMaxMS caps the exponential backoff.
	CooldownMaxMS = 300000
)

type entry struct {
	failCount    int
	cooldownUntil int64
}

// Breaker tracks per-provider failure state for fallback routing.
// A provider enters cooldown on failure; requests skip it until the cooldown
// expires (auto recovery), at which point it is retried and, on success, its
// failure record is cleared.
type Breaker struct {
	mu *sync.Mutex
	m  map[string]*entry
}

// NewBreaker creates an empty breaker.
func NewBreaker() *Breaker {
	return &Breaker{mu: &sync.Mutex{}, m: make(map[string]*entry)}
}

// InCooldown reports whether providerID is currently cooling down; if the
// cooldown has lapsed the record is removed (recovered) and false is returned.
func (b *Breaker) InCooldown(providerID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.m[providerID]
	if !ok {
		return false
	}
	if now := time.Now().UnixMilli(); now < e.cooldownUntil {
		return true
	}
	delete(b.m, providerID)
	return false
}

// RecordFailure marks a provider as failed, growing its cooldown exponentially
// (15s, 30s, 60s, ... capped at 5m).
func (b *Breaker) RecordFailure(providerID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.m[providerID]
	if !ok {
		e = &entry{}
		b.m[providerID] = e
	}
	e.failCount++
	coolMS := int64(CooldownBaseMS) << (e.failCount - 1)
	if coolMS > CooldownMaxMS {
		coolMS = CooldownMaxMS
	}
	e.cooldownUntil = time.Now().UnixMilli() + coolMS
}

// RecordSuccess clears the failure record for a provider.
func (b *Breaker) RecordSuccess(providerID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.m, providerID)
}

// Snapshot returns a copy of all tracked providers for diagnostics.
func (b *Breaker) Snapshot() map[string][]int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string][]int64, len(b.m))
	for id, e := range b.m {
		out[id] = []int64{int64(e.failCount), e.cooldownUntil}
	}
	return out
}