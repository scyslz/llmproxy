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
	// maxShift caps the exponential shift to avoid integer overflow.
	// 15s << 5 = 480s, which is already above CooldownMaxMS.
	maxShift = 5
)

type entry struct {
	failCount     int
	cooldownUntil int64
}

// Breaker tracks per-provider/scope failure state for fallback routing.
// Scope separates independent capabilities (chat, responses, passthrough) so a
// failure on one endpoint does not cool down every route of the provider.
// A provider enters cooldown on failure; requests skip it until the cooldown
// expires (auto recovery), at which point it is retried and, on success, its
// failure record is cleared. The failure count survives cooldown expiry so the
// exponential backoff keeps growing across repeated failures.
type Breaker struct {
	mu *sync.Mutex
	m  map[string]*entry
}

// NewBreaker creates an empty breaker.
func NewBreaker() *Breaker {
	return &Breaker{mu: &sync.Mutex{}, m: make(map[string]*entry)}
}

func key(providerID, scope string) string {
	if scope == "" {
		return providerID
	}
	return providerID + "|" + scope
}

// InCooldown reports whether providerID/scope is currently cooling down.
// The record is kept after the cooldown lapses so that the next failure
// continues the exponential backoff; a successful request clears it.
func (b *Breaker) InCooldown(providerID, scope string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.m[key(providerID, scope)]
	if !ok {
		return false
	}
	return time.Now().UnixMilli() < e.cooldownUntil
}

// RecordFailure marks providerID/scope as failed, growing its cooldown
// exponentially (15s, 30s, 60s, ... capped at 5m).
func (b *Breaker) RecordFailure(providerID, scope string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := key(providerID, scope)
	e, ok := b.m[k]
	if !ok {
		e = &entry{}
		b.m[k] = e
	}
	e.failCount++
	shift := e.failCount - 1
	if shift > maxShift {
		shift = maxShift
	}
	coolMS := int64(CooldownBaseMS) << shift
	if coolMS > CooldownMaxMS {
		coolMS = CooldownMaxMS
	}
	e.cooldownUntil = time.Now().UnixMilli() + coolMS
}

// RecordSuccess clears the failure record for providerID/scope.
func (b *Breaker) RecordSuccess(providerID, scope string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.m, key(providerID, scope))
}

// Snapshot returns a copy of all tracked provider/scope entries for
// diagnostics. Keys use the "provider|scope" form.
func (b *Breaker) Snapshot() map[string][]int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string][]int64, len(b.m))
	for k, e := range b.m {
		out[k] = []int64{int64(e.failCount), e.cooldownUntil}
	}
	return out
}
