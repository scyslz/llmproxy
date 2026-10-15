package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"llmproxy/internal/circuit"
	"llmproxy/internal/config"
	"llmproxy/internal/domain"
	"llmproxy/internal/lazyhealth"
	"llmproxy/internal/logging"
	"llmproxy/internal/logstore"
)

func TestExtractModelsOpenRouterShape(t *testing.T) {
	body := `{"data":[{"id":"openai/gpt-4o","name":"GPT-4o","context_length":128000,"architecture":{"modality":"text->text","input_modalities":["text"]},"pricing":{"prompt":"0.000001"}},{"id":"anthropic/claude-3","name":"Claude 3","context_length":200000}],"total_count":2,"links":{"self":"/api/v1/models"}}`
	ids, err := extractModelsFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0].ID != "openai/gpt-4o" || ids[1].ID != "anthropic/claude-3" {
		t.Fatalf("ids = %+v", ids)
	}
	if ids[0].ContextLength != 128000 || ids[1].ContextLength != 200000 {
		t.Fatalf("context = %+v", ids)
	}
}

func TestExtractModelsGeminiShape(t *testing.T) {
	body := `{"models":[{"name":"models/gemini-2.5-flash","displayName":"Gemini","inputTokenLimit":1048576},{"name":"models/gemini-pro"}]}`
	ids, err := extractModelsFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0].ID != "gemini-2.5-flash" || ids[1].ID != "gemini-pro" {
		t.Fatalf("ids = %+v", ids)
	}
	if ids[0].ContextLength != 1048576 {
		t.Fatalf("context = %+v", ids)
	}
}

func TestExtractModelsTopLevelArray(t *testing.T) {
	ids, err := extractModelsFromReader(strings.NewReader(`["a","b",{"id":"c"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0].ID != "a" || ids[1].ID != "b" || ids[2].ID != "c" {
		t.Fatalf("ids = %+v", ids)
	}
}

func TestClientDoTimeoutKeepsBodyReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		time.Sleep(80 * time.Millisecond)
		_, _ = w.Write([]byte(`{"data":[`))
		for i := 0; i < 200; i++ {
			if i > 0 {
				_, _ = w.Write([]byte(`,`))
			}
			_, _ = w.Write([]byte(`{"id":"m-` + itoa(i) + `","architecture":{"input_modalities":["text","image"]},"pricing":{"prompt":"0"}}`))
		}
		_, _ = w.Write([]byte(`],"total_count":200}`))
	}))
	defer srv.Close()

	cl := NewClient()
	resp, err := cl.Do(context.Background(), "GET", srv.URL, http.Header{}, nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ids, err := extractModelsFromReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 200 {
		t.Fatalf("ids = %d, want 200", len(ids))
	}
}

func TestFetchRemoteModelsOpenRouterLike(t *testing.T) {
	payload := []byte(`{"object":"list","data":[{"id":"stealth/union-alpha","name":"Union Alpha","context_length":262144,"architecture":{"modality":"text+image->text"},"pricing":{"prompt":"0"}},{"id":"openai/gpt-4o","name":"GPT-4o","context_length":128000}],"total_count":2}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	sysStore, err := logstore.OpenSystem(tmp + "/system.db")
	if err != nil {
		t.Fatal(err)
	}
	defer sysStore.Close()
	a := &App{
		Cfg:    cm,
		Client: NewClient(),
		Logger: logging.New(cm, sysStore),
	}
	req := httptest.NewRequest("POST", "/api/providers/fetch-remote-models", bytes.NewReader([]byte(`{"baseUrl":"`+srv.URL+`/api/v1"}`)))
	rec := httptest.NewRecorder()
	a.FetchRemoteModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"stealth/union-alpha"`) || !strings.Contains(body, `"openai/gpt-4o"`) {
		t.Fatalf("unexpected body: %s", body)
	}
	if !strings.Contains(body, `"contextLengths"`) || !strings.Contains(body, "262144") {
		t.Fatalf("missing contextLengths: %s", body)
	}
}

func TestHandleModelsIncludesContextLength(t *testing.T) {
	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.EnableVirtualKey = false
	cfg.Providers = []domain.Provider{
		{ID: "or", Name: "OR", BaseURL: "http://example", Enabled: true, Models: []string{"openai/gpt-4o", "plain", "ctxonly", "outonly"}, ModelContextLengths: map[string]int{"openai/gpt-4o": 128000, "ctxonly": 100000}, ModelMaxOutputs: map[string]int{"openai/gpt-4o": 16384, "outonly": 8192}},
	}
	if err := cm.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	sysStore, err := logstore.OpenSystem(tmp + "/system.db")
	if err != nil {
		t.Fatal(err)
	}
	defer sysStore.Close()
	a := &App{Cfg: cm, Client: NewClient(), Logger: logging.New(cm, sysStore)}
	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	a.HandleModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"context_length":128000`) {
		t.Fatalf("missing context_length: %s", body)
	}
	if !strings.Contains(body, `"max_output_tokens":16384`) {
		t.Fatalf("missing max_output_tokens: %s", body)
	}
	if !strings.Contains(body, `"limit":{"context":128000,"output":16384}`) {
		t.Fatalf("missing limit object: %s", body)
	}
	if !strings.Contains(body, `"limit":{"context":100000,"output":6250}`) {
		t.Fatalf("ctxonly missing paired limit: %s", body)
	}
	if !strings.Contains(body, `"limit":{"context":131072,"output":8192}`) {
		t.Fatalf("outonly missing paired limit: %s", body)
	}
	if strings.Count(body, `"context_length"`) != 3 {
		t.Fatalf("ctxonly/outonly should get paired limits, plain omitted: %s", body)
	}
	if !strings.Contains(body, `"context_length":100000`) {
		t.Fatalf("ctxonly missing context_length: %s", body)
	}
	if !strings.Contains(body, `"max_output_tokens":6250`) {
		t.Fatalf("ctxonly missing derived max_output_tokens: %s", body)
	}
	if !strings.Contains(body, `"context_length":131072`) {
		t.Fatalf("outonly missing derived context_length: %s", body)
	}
}

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
	cands, bound, _ := a.selectCandidates(cfg, "unknown-key", nil)
	if bound {
		t.Fatal("unknown key should not bind")
	}
	if len(cands) != 2 {
		t.Fatalf("enabled candidates = %d, want 2", len(cands))
	}

	// bound key with specific providerIds
	var name string
	cands, bound, _ = a.selectCandidates(cfg, "sk-test-bound", &name)
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
	cands, bound, _ = a.selectCandidates(cfg, "sk-test-all", nil)
	if bound {
		t.Fatal("all key should not bind")
	}
	if len(cands) != 2 {
		t.Fatalf("all key candidates = %d, want 2", len(cands))
	}
}

func TestSelectCandidates_Group(t *testing.T) {
	a := &App{}
	cfg := testConfig()

	// bound key with groupId -> group providers in order
	cands, bound, fromGroup := a.selectCandidates(cfg, "sk-test-group", nil)
	if !bound || !fromGroup {
		t.Fatalf("group key should bind and flag fromGroup")
	}
	if len(cands) != 2 || cands[0].ID != "mock" || cands[1].ID != "backup" {
		t.Fatalf("group candidates wrong: %+v", cands)
	}

	// groupId pointing to non-existent group falls back to providerIds (empty) -> global enabled, not bound
	cands, bound, fromGroup = a.selectCandidates(cfg, "sk-test-group-miss", nil)
	if bound || fromGroup {
		t.Fatal("missing group should not bind")
	}
	if len(cands) != 2 {
		t.Fatalf("fallback candidates = %d, want 2", len(cands))
	}
}

func TestGroup_DegradeWithLazyHealth(t *testing.T) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"model":"ok","usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer okSrv.Close()

	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &domain.Config{
		Listen:           ":0",
		EnableVirtualKey: true,
		LogDetail:        "basic",
		Providers: []domain.Provider{
			{ID: "fail", Name: "Fail P", BaseURL: failSrv.URL, APIKey: "k", Enabled: true, Models: []string{"m"}, DefaultModel: "m", Timeout: 2000},
			{ID: "ok", Name: "Ok P", BaseURL: okSrv.URL, APIKey: "k", Enabled: true, Models: []string{"m"}, DefaultModel: "m", Timeout: 2000},
		},
		Groups: []domain.ProviderGroup{
			{ID: "g", Name: "G", Entries: []domain.GroupEntry{{ProviderID: "fail"}, {ProviderID: "ok"}}},
		},
		Keys: []domain.VirtualKey{
			{Key: "sk", Name: "K", GroupID: "g"},
		},
	}
	if err := cm.Replace(cfg); err != nil {
		t.Fatal(err)
	}

	reqStore, err := logstore.OpenRequest(tmp+"/requests.db", 100)
	if err != nil {
		t.Fatal(err)
	}
	defer reqStore.Close()
	sysStore, err := logstore.OpenSystem(tmp + "/system.db")
	if err != nil {
		t.Fatal(err)
	}
	defer sysStore.Close()

	health := lazyhealth.New()
	a := &App{
		Cfg:     cm,
		Breaker: circuit.NewBreaker(),
		Client:  NewClient(),
		Logger:  logging.New(cm, sysStore),
		ReqLog:  reqStore,
		Health:  health,
	}

	// First request: fail then ok -> logs two entries (p1=500, p2=200)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req.Header.Set("Authorization", "Bearer sk")
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	logs, err := reqStore.Query(logstore.QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("total logs: %d", len(logs))
	for i, l := range logs {
		t.Logf("log[%d]: Provider=%s Model=%s Status=%d Error=%s", i, l.Provider, l.Model, l.Status, l.Error)
	}
	if len(logs) < 2 {
		t.Fatalf("request logs = %d, want >=2", len(logs))
	}
	// logs are newest-first; find fail and ok entries
	var failLog, okLog *logstore.RequestLog
	for i := range logs {
		if logs[i].Provider == "Fail P" {
			failLog = &logs[i]
		}
		if logs[i].Provider == "Ok P" {
			okLog = &logs[i]
		}
	}
	if failLog == nil || failLog.Status != 500 {
		t.Fatalf("missing fail log, got: %+v", failLog)
	}
	if okLog == nil || okLog.Status != 200 {
		t.Fatalf("missing ok log, got: %+v", okLog)
	}

	// Second request immediately: fail P should be in cooldown (30s)
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req2.Header.Set("Authorization", "Bearer sk")
	rec2 := httptest.NewRecorder()
	a.HandleProxy(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second request status = %d, want 200", rec2.Code)
	}

	logs2, err := reqStore.Query(logstore.QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Should be 3 total: fail+ok (first request) + ok (second request, fail skipped by cooldown)
	if len(logs2) != 3 {
		t.Fatalf("total logs after cooldown = %d, want 3", len(logs2))
	}
	// Find the second-request ok log (most recent ok)
	var secondOk *logstore.RequestLog
	for i := range logs2 {
		if logs2[i].Provider == "Ok P" && logs2[i].Error == "" {
			secondOk = &logs2[i]
		}
	}
	if secondOk == nil {
		t.Fatalf("missing second ok log: %+v", logs2)
	}
	// Verify health state
	snap := health.Snapshot()
	s, ok := snap["fail|m"]
	if !ok {
		t.Fatal("fail|m should be in health tracker")
	}
	if !s.InCooldown {
		t.Fatal("fail|m should be in cooldown")
	}
	if s.FailCount != 1 {
		t.Fatalf("failCount = %d, want 1 (single failure per provider, no double count on probe flip)", s.FailCount)
	}
}

func TestLookupModelContexts(t *testing.T) {
	db := `{
		"openai": {"models": {
			"gpt-4o": {"name": "GPT-4o", "limit": {"context": 128000, "output": 16384}},
			"gpt-4o-mini": {"name": "GPT-4o mini", "limit": {"context": 128000}}
		}},
		"anthropic": {"models": {
			"anthropic/claude-3-5-sonnet-20241022": {"name": "Claude 3.5 Sonnet", "limit": {"context": 200000}}
		}},
		"deepseek": {"models": {
			"deepseek-chat": {"name": "DeepSeek", "limit": {"context": 64000}},
			"deepseek-v4-pro": {"name": "DeepSeek V4 Pro", "limit": {"context": 1000000}}
		}}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(db))
	}))
	defer srv.Close()

	oldURL := modelDBURL
	modelDBURL = srv.URL
	resetModelDBCache()
	defer func() {
		modelDBURL = oldURL
		resetModelDBCache()
	}()

	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	sysStore, err := logstore.OpenSystem(tmp + "/system.db")
	if err != nil {
		t.Fatal(err)
	}
	defer sysStore.Close()
	a := &App{Cfg: cm, Client: NewClient(), Logger: logging.New(cm, sysStore)}

	req := httptest.NewRequest("POST", "/api/models/lookup-context", bytes.NewReader([]byte(
		`{"models":["openai/gpt-4o","claude-3-5-sonnet-latest","deepseek-v4-pro","unknown-model","gpt-4o"]}`)))
	rec := httptest.NewRecorder()
	a.LookupModelContexts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	var out struct {
		ContextLengths map[string]int `json:"contextLengths"`
		MaxOutputs     map[string]int `json:"maxOutputs"`
		Matched        int            `json:"matched"`
		Requested      int            `json:"requested"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Requested != 5 {
		t.Fatalf("requested = %d, want 5", out.Requested)
	}
	if out.Matched != 4 {
		t.Fatalf("matched = %d, want 4, body = %s", out.Matched, rec.Body.String())
	}
	if out.ContextLengths["gpt-4o"] != 128000 {
		t.Fatalf("gpt-4o = %d, want 128000", out.ContextLengths["gpt-4o"])
	}
	if out.ContextLengths["openai/gpt-4o"] != 128000 {
		t.Fatalf("openai/gpt-4o = %d, want 128000", out.ContextLengths["openai/gpt-4o"])
	}
	if out.MaxOutputs["openai/gpt-4o"] != 16384 {
		t.Fatalf("openai/gpt-4o output = %d, want 16384", out.MaxOutputs["openai/gpt-4o"])
	}
	if out.ContextLengths["claude-3-5-sonnet-latest"] != 200000 {
		t.Fatalf("claude = %d, want 200000", out.ContextLengths["claude-3-5-sonnet-latest"])
	}
	if out.ContextLengths["deepseek-v4-pro"] != 1000000 {
		t.Fatalf("deepseek = %d, want 1000000", out.ContextLengths["deepseek-v4-pro"])
	}
	// Default fallback applies to unmatched models
	if out.ContextLengths["unknown-model"] != defaultContextLength {
		t.Fatalf("unknown-model = %d, want default %d", out.ContextLengths["unknown-model"], defaultContextLength)
	}
	// Output fallback: default context 200000/16 = 12500
	if out.MaxOutputs["unknown-model"] != 12500 {
		t.Fatalf("unknown-model output = %d, want 12500", out.MaxOutputs["unknown-model"])
	}
	// DB-hit model without output gets fallback from its context
	if out.MaxOutputs["claude-3-5-sonnet-latest"] != 12500 {
		t.Fatalf("claude output = %d, want 12500", out.MaxOutputs["claude-3-5-sonnet-latest"])
	}
}

func TestLookupModelContextsNoFallback(t *testing.T) {
	db := `{"openai":{"models":{"gpt-4o":{"limit":{"context":128000}}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(db))
	}))
	defer srv.Close()

	oldURL := modelDBURL
	modelDBURL = srv.URL
	resetModelDBCache()
	defer func() {
		modelDBURL = oldURL
		resetModelDBCache()
	}()

	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	sysStore, err := logstore.OpenSystem(tmp + "/system.db")
	if err != nil {
		t.Fatal(err)
	}
	defer sysStore.Close()
	a := &App{Cfg: cm, Client: NewClient(), Logger: logging.New(cm, sysStore)}

	req := httptest.NewRequest("POST", "/api/models/lookup-context",
		bytes.NewReader([]byte(`{"models":["gpt-4o","unknown-model"],"useDefault":false}`)))
	rec := httptest.NewRecorder()
	a.LookupModelContexts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ContextLengths map[string]int `json:"contextLengths"`
		MaxOutputs map[string]int `json:"maxOutputs"`
		Fallbacks      []string       `json:"fallbacks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ContextLengths["gpt-4o"] != 128000 {
		t.Fatalf("gpt-4o = %d, want 128000", out.ContextLengths["gpt-4o"])
	}
	if _, ok := out.ContextLengths["unknown-model"]; ok {
		t.Fatalf("unknown-model should be omitted when useDefault=false: %s", rec.Body.String())
	}
	if len(out.Fallbacks) != 0 {
		t.Fatalf("fallbacks = %v, want empty", out.Fallbacks)
	}
}

func TestLookupModelContextsViaLLM(t *testing.T) {
	db := `{"openai":{"models":{"gpt-4o":{"limit":{"context":128000}}}}}`
	dbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(db))
	}))
	defer dbSrv.Close()

	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		content := "{\"my-private-model\": {\"context\": 64000, \"output\": 8192}}"
		respBody := map[string]interface{}{}
		respBody["choices"] = []interface{}{map[string]interface{}{
			"message": map[string]string{"role": "assistant", "content": content},
		}}
		b, _ := json.Marshal(respBody)
		_, _ = w.Write(b)
	}))
	defer chatSrv.Close()

	oldURL := modelDBURL
	modelDBURL = dbSrv.URL
	resetModelDBCache()
	defer func() {
		modelDBURL = oldURL
		resetModelDBCache()
	}()

	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	sysStore, err := logstore.OpenSystem(tmp + "/system.db")
	if err != nil {
		t.Fatal(err)
	}
	defer sysStore.Close()
	a := &App{Cfg: cm, Client: NewClient(), Logger: logging.New(cm, sysStore)}

	reqBody := map[string]interface{}{
		"models": []string{"gpt-4o", "my-private-model"},
		"useLLM": true,
		"llm": map[string]string{
			"baseUrl": chatSrv.URL,
			"apiKey":  "test-key",
			"model":   "helper-model",
		},
	}
	raw, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/models/lookup-context", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	a.LookupModelContexts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ContextLengths map[string]int `json:"contextLengths"`
		MaxOutputs map[string]int `json:"maxOutputs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ContextLengths["gpt-4o"] != 128000 {
		t.Fatalf("gpt-4o = %d, want 128000 (from db): %s", out.ContextLengths["gpt-4o"], rec.Body.String())
	}
	if out.ContextLengths["my-private-model"] != 64000 {
		t.Fatalf("my-private-model = %d, want 64000 (from llm): %s", out.ContextLengths["my-private-model"], rec.Body.String())
	}
	if out.MaxOutputs["my-private-model"] != 8192 {
		t.Fatalf("my-private-model output = %d, want 8192 (from llm): %s", out.MaxOutputs["my-private-model"], rec.Body.String())
	}
}

func TestParseContextLengthsJSON(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]modelLimit
	}{
		{"```json\n{\"a\": 1000}\n```", map[string]modelLimit{"a": {Context: 1000}}},
		{"Sure: {\"b\": \"200000\"} done", map[string]modelLimit{"b": {Context: 200000}}},
		{"{\"c\": {\"context\": 128000, \"output\": 16384}}", map[string]modelLimit{"c": {Context: 128000, Output: 16384}}},
		{"no json here", map[string]modelLimit{}},
	}
	for _, c := range cases {
		got := parseContextLengthsJSON(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("parse(%q) = %v, want %v", c.in, got, c.want)
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Fatalf("parse(%q)[%q] = %+v, want %+v", c.in, k, got[k], v)
			}
		}
	}
}
