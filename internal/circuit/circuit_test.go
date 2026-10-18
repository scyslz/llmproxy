package circuit

import (
	"testing"
	"time"
)

func TestScopeIsolation(t *testing.T) {
	b := NewBreaker()
	b.RecordFailure("p1", "chat")

	if !b.InCooldown("p1", "chat") {
		t.Fatal("chat scope should be in cooldown")
	}
	if b.InCooldown("p1", "responses") {
		t.Fatal("responses scope must not share chat cooldown")
	}
	if b.InCooldown("p2", "chat") {
		t.Fatal("other providers must not share cooldown")
	}
}

func TestExpiredCooldownKeepsFailCount(t *testing.T) {
	b := NewBreaker()
	b.RecordFailure("p1", "chat")

	k := key("p1", "chat")
	b.m[k].cooldownUntil = time.Now().UnixMilli() - 1
	if b.InCooldown("p1", "chat") {
		t.Fatal("expired cooldown must allow requests")
	}
	if _, ok := b.m[k]; !ok {
		t.Fatal("expired entry must be kept to preserve backoff")
	}

	before := time.Now().UnixMilli()
	b.RecordFailure("p1", "chat")
	got := b.m[k].cooldownUntil - before
	if got < 29000 || got > 31000 {
		t.Fatalf("second failure cooldown = %dms, want about 30000ms", got)
	}
}

func TestRecordSuccessClearsOnlyItsScope(t *testing.T) {
	b := NewBreaker()
	b.RecordFailure("p1", "chat")
	b.RecordFailure("p1", "responses")

	b.RecordSuccess("p1", "chat")

	if b.InCooldown("p1", "chat") {
		t.Fatal("success must clear the chat scope")
	}
	if !b.InCooldown("p1", "responses") {
		t.Fatal("success on chat must not clear responses")
	}
}

func TestCooldownCapped(t *testing.T) {
	b := NewBreaker()
	for i := 0; i < 100; i++ {
		b.RecordFailure("p1", "chat")
	}
	e := b.m[key("p1", "chat")]
	remaining := e.cooldownUntil - time.Now().UnixMilli()
	if remaining > CooldownMaxMS {
		t.Fatalf("cooldown = %dms, want <= %dms", remaining, CooldownMaxMS)
	}
}
