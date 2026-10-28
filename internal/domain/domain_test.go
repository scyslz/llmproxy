package domain

import "testing"

func TestSanitizeModels(t *testing.T) {
	in := []Model{
		{ID: " gpt-4o ", ContextLength: 128000, MaxOutputTokens: 16384},
		{ID: "gpt-4o", ContextLength: 1}, // duplicate, dropped
		{ID: ""},                         // blank, dropped
		{ID: "m2", ContextLength: -1, MaxOutputTokens: -5},
		{ID: "m3", Protocol: "bogus"},
		{ID: "m4", Protocol: "responses"},
	}
	got := SanitizeModels(in)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4: %+v", len(got), got)
	}
	if got[0].ID != "gpt-4o" || got[0].ContextLength != 128000 || got[0].MaxOutputTokens != 16384 {
		t.Fatalf("first model = %+v", got[0])
	}
	if got[1].ContextLength != 0 || got[1].MaxOutputTokens != 0 {
		t.Fatalf("negative limits not clamped: %+v", got[1])
	}
	if got[2].Protocol != "" {
		t.Fatalf("unsupported protocol kept: %+v", got[2])
	}
	if got[3].Protocol != "responses" {
		t.Fatalf("responses protocol lost: %+v", got[3])
	}
}

func TestProviderModelHelpers(t *testing.T) {
	p := Provider{Models: Models("a", "b"), DefaultModel: "b"}
	if m := p.ModelByID("b"); m == nil || m.ID != "b" {
		t.Fatal("ModelByID lookup failed")
	}
	if p.ModelByID("missing") != nil {
		t.Fatal("ModelByID should return nil for unknown ids")
	}
	if ids := p.ModelIDs(); len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("ModelIDs = %v", ids)
	}
	if p.DefaultModelID() != "b" {
		t.Fatalf("DefaultModelID = %q", p.DefaultModelID())
	}
	p.DefaultModel = ""
	if p.DefaultModelID() != "a" {
		t.Fatalf("DefaultModelID fallback = %q", p.DefaultModelID())
	}
	empty := Provider{}
	if empty.DefaultModelID() != "" {
		t.Fatal("empty provider should have empty default model")
	}
}
