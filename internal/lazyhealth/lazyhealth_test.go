package lazyhealth

import (
	"sync"
	"testing"
	"time"
)

func TestCooldownSequenceArithmetic(t *testing.T) {
	tracker := New()
	now := time.Now().UnixMilli()

	tracker.RecordFailure("openai", "gpt-4o")
	first := tracker.m["openai|gpt-4o"]
	if first.cooldownUntil-now != 30000 {
		t.Fatalf("first failure cooldown = %dms, want 30000ms", first.cooldownUntil-now)
	}

	tracker.RecordFailure("openai", "gpt-4o")
	second := tracker.m["openai|gpt-4o"]
	if second.cooldownUntil-now != 60000 {
		t.Fatalf("second failure cooldown = %dms, want 60000ms", second.cooldownUntil-now)
	}

	tracker.RecordFailure("openai", "gpt-4o")
	third := tracker.m["openai|gpt-4o"]
	if third.cooldownUntil-now != 90000 {
		t.Fatalf("third failure cooldown = %dms, want 90000ms", third.cooldownUntil-now)
	}

	for i := 0; i < 30; i++ {
		tracker.RecordFailure("openai", "gpt-4o")
	}
	capped := tracker.m["openai|gpt-4o"]
	if capped.cooldownUntil-now != MaxMS {
		t.Fatalf("capped cooldown = %dms, want %dms", capped.cooldownUntil-now, MaxMS)
	}
}

func TestInCooldownAndAutoExpire(t *testing.T) {
	tracker := New()
	if tracker.InCooldown("openai", "gpt-4o") {
		t.Fatal("unknown provider/model must not be in cooldown")
	}

	tracker.RecordFailure("openai", "gpt-4o")
	if !tracker.InCooldown("openai", "gpt-4o") {
		t.Fatal("failed provider/model must be in cooldown")
	}

	e := tracker.m["openai|gpt-4o"]
	e.cooldownUntil = time.Now().UnixMilli() - 1

	if tracker.InCooldown("openai", "gpt-4o") {
		t.Fatal("expired cooldown must be treated as recovered")
	}
	if _, ok := tracker.m["openai|gpt-4o"]; ok {
		t.Fatal("expired entry must be removed")
	}
}

func TestRecordSuccessClears(t *testing.T) {
	tracker := New()
	tracker.RecordFailure("openai", "gpt-4o")
	tracker.RecordFailure("openai", "gpt-4o")
	if !tracker.InCooldown("openai", "gpt-4o") {
		t.Fatal("expected in cooldown before success")
	}

	tracker.RecordSuccess("openai", "gpt-4o")
	if tracker.InCooldown("openai", "gpt-4o") {
		t.Fatal("success must clear cooldown")
	}
	if _, ok := tracker.m["openai|gpt-4o"]; ok {
		t.Fatal("entry must be removed after success")
	}
}

func TestPerModelIsolation(t *testing.T) {
	tracker := New()
	tracker.RecordFailure("openai", "gpt-4o")
	if !tracker.InCooldown("openai", "gpt-4o") {
		t.Fatal("expected in cooldown for gpt-4o")
	}
	if tracker.InCooldown("openai", "gpt-4o-turbo") {
		t.Fatal("different model must not share cooldown state")
	}
	tracker.RecordFailure("openai", "gpt-4o-turbo")
	if !tracker.InCooldown("openai", "gpt-4o-turbo") {
		t.Fatal("expected in cooldown for gpt-4o-turbo")
	}
}

func TestConcurrentSafety(t *testing.T) {
	tracker := New()
	const goroutines = 32
	const iterations = 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			model := "model-" + string(rune('a'+g%26))
			for i := 0; i < iterations; i++ {
				tracker.RecordFailure("p", model)
				tracker.InCooldown("p", model)
				if i%10 == 0 {
					tracker.RecordSuccess("p", model)
				}
			}
		}(g)
	}
	wg.Wait()

	_ = tracker.Snapshot()
}

func TestSnapshotSkipsExpired(t *testing.T) {
	tracker := New()
	tracker.RecordFailure("openai", "gpt-4o")
	tracker.RecordFailure("deepseek", "deepseek-chat")
	tracker.m["openai|gpt-4o"].cooldownUntil = time.Now().UnixMilli() - 1

	states := tracker.Snapshot()
	if len(states) != 1 {
		t.Fatalf("Snapshot len = %d, want 1 (expired entry dropped)", len(states))
	}
	s, ok := states["deepseek|deepseek-chat"]
	if !ok {
		t.Fatal("active entry missing from snapshot")
	}
	if !s.InCooldown || s.RemainingMs <= 0 {
		t.Fatalf("active entry state wrong: %+v", s)
	}
	if s.FailCount != 1 {
		t.Fatalf("active entry failCount = %d, want 1", s.FailCount)
	}
}
