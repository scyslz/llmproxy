package lazyhealth

import (
	"sync"
	"time"
)

const (
	// BaseMS is the first cooldown length after the first failure (30s).
	BaseMS = 30000
	// MaxMS caps the arithmetic cooldown sequence (10 minutes).
	MaxMS = 600000
)

// State describes the cooldown state of a single provider/model.
type State struct {
	ProviderID   string `json:"providerId"`
	Model        string `json:"model"`
	FailCount    int    `json:"failCount"`
	CooldownMs   int64  `json:"cooldownMs"`
	RemainingMs  int64  `json:"remainingMs"`
	InCooldown   bool   `json:"inCooldown"`
}

type entry struct {
	failCount     int
	cooldownUntil int64
}

// Tracker stores provider/model level cooldown state for lazy health checks.
// The real request acts as the probe: success clears the state, failure grows
// the cooldown by an arithmetic sequence (30s, 60s, 90s, ... capped at 10min).
type Tracker struct {
	mu sync.Mutex
	m  map[string]*entry
}

// New creates an empty Tracker.
func New() *Tracker {
	return &Tracker{m: make(map[string]*entry)}
}

func key(providerID, model string) string {
	return providerID + "|" + model
}

// InCooldown reports whether provider/model is currently cooling down. A record
// whose cooldown has lapsed is removed (recovered) and false is returned.
func (t *Tracker) InCooldown(providerID, model string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[key(providerID, model)]
	if !ok {
		return false
	}
	if now := time.Now().UnixMilli(); now < e.cooldownUntil {
		return true
	}
	delete(t.m, key(providerID, model))
	return false
}

// RecordFailure marks provider/model as failed, growing its cooldown by an
// arithmetic sequence: BaseMS * failCount (30s, 60s, 90s, ... capped at MaxMS).
func (t *Tracker) RecordFailure(providerID, model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := key(providerID, model)
	e, ok := t.m[k]
	if !ok {
		e = &entry{}
		t.m[k] = e
	}
	e.failCount++
	coolMS := int64(BaseMS) * int64(e.failCount)
	if coolMS > MaxMS {
		coolMS = MaxMS
	}
	e.cooldownUntil = time.Now().UnixMilli() + coolMS
}

// RecordSuccess clears the failure record for provider/model.
func (t *Tracker) RecordSuccess(providerID, model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, key(providerID, model))
}

// Snapshot returns the current cooldown state for all tracked provider/models.
func (t *Tracker) Snapshot() map[string]State {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]State, len(t.m))
	now := time.Now().UnixMilli()
	for k, e := range t.m {
		id, model := splitKey(k)
		remaining := e.cooldownUntil - now
		if remaining < 0 {
			remaining = 0
		}
		inCool := remaining > 0
		if !inCool {
			delete(t.m, k)
			continue
		}
		out[k] = State{
			ProviderID:  id,
			Model:       model,
			FailCount:   e.failCount,
			CooldownMs:  remaining,
			RemainingMs: remaining,
			InCooldown:  inCool,
		}
	}
	return out
}

func splitKey(k string) (string, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == '|' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}
