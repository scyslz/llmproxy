package proxy

import (
	"testing"
)

func TestUsageParserStreaming(t *testing.T) {
	p := &UsageParser{}
	p.Push("data: {\"id\":\"1\",\"model\":\"mock-1234\"}\n\n")
	p.Push("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
	p.Push(`data: {"id":"1","model":"mock-1234","usage":{"prompt_tokens":12,"completion_tokens":34,"prompt_tokens_details":{"cached_tokens":7},"total_tokens":46}}` + "\n\n")
	p.Push("data: [DONE]\n\n")

	if p.Model != "mock-1234" {
		t.Fatalf("model = %q, want mock-1234", p.Model)
	}
	if p.Usage == nil {
		t.Fatal("usage not parsed")
	}
	if p.Usage.PromptTokens != 12 || p.Usage.CompletionTokens != 34 || p.Usage.CachedTokens != 7 {
		t.Fatalf("usage wrong: %+v", p.Usage)
	}
}

func TestUsageParserChunkBoundary(t *testing.T) {
	// usage split across two Push calls, second terminating with newline
	line := `data: {"id":"1","model":"m","usage":{"prompt_tokens":5,"completion_tokens":6,"prompt_tokens_details":{"cached_tokens":1},"total_tokens":11}}`
	p := &UsageParser{}
	p.Push(line[:40])
	p.Push(line[40:] + "\n\n")

	if p.Usage == nil || p.Usage.PromptTokens != 5 || p.Usage.CompletionTokens != 6 || p.Usage.CachedTokens != 1 {
		t.Fatalf("chunked usage wrong: %+v", p.Usage)
	}
}

func TestUsageParserCachedTopLevel(t *testing.T) {
	// Some providers (DeepSeek style) report cached tokens at the top level.
	p := &UsageParser{}
	p.Push(`data: {"model":"deepseek","usage":{"prompt_tokens":20,"completion_tokens":8,"prompt_cache_hit_tokens":9,"total_tokens":28}}` + "\n\n")
	if p.Usage == nil || p.Usage.CachedTokens != 9 {
		t.Fatalf("top-level cached not parsed: %+v", p.Usage)
	}
}

func TestUsageParserCachedUsageField(t *testing.T) {
	// Some gateways return usage.cached_tokens directly.
	p := &UsageParser{}
	p.Push(`data: {"model":"m","usage":{"prompt_tokens":10,"completion_tokens":5,"cached_tokens":4}}` + "\n\n")
	if p.Usage == nil || p.Usage.CachedTokens != 4 {
		t.Fatalf("usage.cached_tokens not parsed: %+v", p.Usage)
	}
}

func TestUsageParserCachedPrecedence(t *testing.T) {
	// prompt_tokens_details.cached_tokens should win over cache hit tokens.
	p := &UsageParser{}
	p.Push(`data: {"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_cache_hit_tokens":9,"prompt_tokens_details":{"cached_tokens":7}}}` + "\n\n")
	if p.Usage == nil || p.Usage.CachedTokens != 7 {
		t.Fatalf("precedence wrong: %+v", p.Usage)
	}
}

func TestUsageParserNonJSONLine(t *testing.T) {
	p := &UsageParser{}
	p.Push(": keepalive comment\n\n")
	p.Push("event: ping\n\n")
	if p.Model != "" || p.Usage != nil {
		t.Fatal("should ignore non data lines")
	}
}

func TestParseUsageJSON(t *testing.T) {
	body := `{"id":"1","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":2},"total_tokens":7}}`
	u, m := parseUsageJSON([]byte(body))
	if m != "gpt-4o" {
		t.Fatalf("model = %q", m)
	}
	if u == nil || u.PromptTokens != 3 || u.CompletionTokens != 4 || u.CachedTokens != 2 {
		t.Fatalf("usage wrong: %+v", u)
	}
}

func TestParseUsageJSONWithoutUsage(t *testing.T) {
	body := `{"id":"1","model":"m","choices":[]}`
	u, m := parseUsageJSON([]byte(body))
	if u != nil {
		t.Fatalf("expected nil usage, got %+v", u)
	}
	if m != "m" {
		t.Fatalf("model = %q, want m", m)
	}
}

func TestParseUsageJSONInvalid(t *testing.T) {
	u, m := parseUsageJSON([]byte("not json"))
	if u != nil || m != "" {
		t.Fatalf("expected nil/nil for invalid json, got %+v %q", u, m)
	}
}

func TestParseUsageJSONCachedTopLevel(t *testing.T) {
	body := `{"model":"deepseek","usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_cache_hit_tokens":6}}`
	u, _ := parseUsageJSON([]byte(body))
	if u == nil || u.CachedTokens != 6 {
		t.Fatalf("top-level cached not parsed: %+v", u)
	}
}

func TestParseUsageJSONCachedUsageField(t *testing.T) {
	body := `{"model":"m","usage":{"prompt_tokens":10,"completion_tokens":5,"cached_tokens":4}}`
	u, _ := parseUsageJSON([]byte(body))
	if u == nil || u.CachedTokens != 4 {
		t.Fatalf("usage.cached_tokens not parsed: %+v", u)
	}
}

func TestResolveTargetURL(t *testing.T) {
	cases := []struct {
		base, endpoint, path, want string
	}{
		{"https://api.openai.com", "", "/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1", "", "/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
		{"https://generativelanguage.googleapis.com/v1beta/openai", "", "/v1/chat/completions", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"https://api.deepseek.com", "", "/v1/chat/completions", "https://api.deepseek.com/v1/chat/completions"},
		{"https://api.example.com/v1", "/chat/completions", "/v1/chat/completions", "https://api.example.com/v1/chat/completions"},
		{"https://api.example.com", "/custom/endpoint", "/v1/anything", "https://api.example.com/custom/endpoint"},
	}
	for _, c := range cases {
		got := ResolveTargetURL(c.base, c.endpoint, c.path)
		if got != c.want {
			t.Errorf("ResolveTargetURL(%q,%q,%q) = %q, want %q", c.base, c.endpoint, c.path, got, c.want)
		}
	}
}

func TestSelectCandidates(t *testing.T) {
	a := &App{}
	cfg := testConfig()
	// unbound key -> enabled providers
	cands, bound := a.selectCandidates(cfg, "unknown-key", nil)
	if bound {
		t.Fatal("unknown key should not bind")
	}
	if len(cands) != 2 {
		t.Fatalf("enabled candidates = %d, want 2", len(cands))
	}

	// bound key with specific providerIds
	var name string
	cands, bound = a.selectCandidates(cfg, "sk-test-bound", &name)
	if !bound {
		t.Fatal("bound key should bind")
	}
	if name != "Bound Key" {
		t.Fatalf("keyName = %q, want Bound Key", name)
	}
	if len(cands) != 1 || cands[0].ID != "mock" {
		t.Fatalf("bound candidates wrong: %+v", cands)
	}

	// bound key with providerIds = ["all"] -> global enabled
	cands, bound = a.selectCandidates(cfg, "sk-test-all", nil)
	if bound {
		t.Fatal("all key should not bind")
	}
	if len(cands) != 2 {
		t.Fatalf("all key candidates = %d, want 2", len(cands))
	}
}
