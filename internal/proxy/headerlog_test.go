package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llmproxy/internal/circuit"
	"llmproxy/internal/config"
	"llmproxy/internal/domain"
	"llmproxy/internal/logging"
	"llmproxy/internal/logstore"
)

// TestHandleProxyNon2xxLogsHeaders: logDetail=error 时，上游返回非 2xx，
// 系统日志中应出现请求头与响应头细节。
func TestHandleProxyNon2xxLogsHeaders(t *testing.T) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "test")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer failSrv.Close()

	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &domain.Config{
		Listen:           ":0",
		EnableVirtualKey: true,
		LogDetail:        "error",
		Providers: []domain.Provider{
			{ID: "solo", Name: "Solo P", BaseURL: failSrv.URL, APIKey: "k", Enabled: true, Models: []string{"m"}, DefaultModel: "m", Timeout: 2000},
		},
		Keys: []domain.VirtualKey{
			{Key: "sk-solo", Name: "Solo Key", ProviderIDs: []string{"solo"}},
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

	a := &App{
		Cfg:     cm,
		Breaker: circuit.NewBreaker(),
		Client:  NewClient(),
		Logger:  logging.New(cm, sysStore),
		ReqLog:  reqStore,
	}

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-solo")
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}

	logs, _, err := sysStore.Query("", "proxy", "", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var reqHdrs, respHdrs bool
	for _, l := range logs {
		if strings.Contains(l.Message, "[API Proxy Request Headers]") {
			reqHdrs = true
		}
		if strings.Contains(l.Message, "[API Proxy Response Headers]") {
			respHdrs = true
		}
	}
	if !reqHdrs {
		t.Fatal("non-2xx in error mode: request headers not logged")
	}
	if !respHdrs {
		t.Fatal("non-2xx in error mode: response headers not logged")
	}
}

// TestHandleDirectChatLogsRequest: Playground 直连测试应写入请求用量日志。
func TestHandleDirectChatLogsRequest(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"direct-model","usage":{"prompt_tokens":5,"completion_tokens":6,"total_tokens":11}}`))
	}))
	defer okSrv.Close()

	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &domain.Config{
		Listen:    ":0",
		LogDetail: "basic",
		Providers: []domain.Provider{
			{ID: "ok", Name: "Ok P", BaseURL: okSrv.URL, APIKey: "k", Enabled: true, Models: []string{"m"}, DefaultModel: "m", Timeout: 2000},
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

	a := &App{
		Cfg:     cm,
		Breaker: circuit.NewBreaker(),
		Client:  NewClient(),
		Logger:  logging.New(cm, sysStore),
		ReqLog:  reqStore,
	}

	body := `{"model":"direct-model","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest("POST", "/api/providers/ok/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.HandleDirectChat(rec, req, "ok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	logs, err := reqStore.Query(logstore.QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("request logs = %d, want 1", len(logs))
	}
	l := logs[0]
	if l.Provider != "Ok P" {
		t.Fatalf("provider = %q, want Ok P", l.Provider)
	}
	if l.Status != 200 {
		t.Fatalf("status = %d, want 200", l.Status)
	}
	if l.Model != "direct-model" {
		t.Fatalf("model = %q, want direct-model", l.Model)
	}
	if l.PromptTokens != 5 || l.CompletionToks != 6 {
		t.Fatalf("usage wrong: prompt=%d completion=%d", l.PromptTokens, l.CompletionToks)
	}
}

// TestHandleDirectChatNon2xxLogsHeaders: 直连测试遇到上游 4xx 且 logDetail=error 时，
// 系统日志中应出现请求头与响应头细节。
func TestHandleDirectChatNon2xxLogsHeaders(t *testing.T) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "test")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer failSrv.Close()

	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &domain.Config{
		Listen:    ":0",
		LogDetail: "error",
		Providers: []domain.Provider{
			{ID: "bad", Name: "Bad P", BaseURL: failSrv.URL, APIKey: "k", Enabled: true, Models: []string{"m"}, DefaultModel: "m", Timeout: 2000},
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

	a := &App{
		Cfg:     cm,
		Breaker: circuit.NewBreaker(),
		Client:  NewClient(),
		Logger:  logging.New(cm, sysStore),
		ReqLog:  reqStore,
	}

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest("POST", "/api/providers/bad/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.HandleDirectChat(rec, req, "bad")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	logs, _, err := sysStore.Query("", "proxy", "", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var reqHdrs, respHdrs bool
	for _, l := range logs {
		if strings.Contains(l.Message, "[Provider Test Request Headers]") {
			reqHdrs = true
		}
		if strings.Contains(l.Message, "[Provider Test Response Headers]") {
			respHdrs = true
		}
	}
	if !reqHdrs {
		t.Fatal("direct 4xx in error mode: request headers not logged")
	}
	if !respHdrs {
		t.Fatal("direct 4xx in error mode: response headers not logged")
	}

	// 同时应写入请求日志
	reqLogs, err := reqStore.Query(logstore.QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqLogs) != 1 || reqLogs[0].Status != 401 {
		t.Fatalf("request logs = %+v, want one 401 entry", reqLogs)
	}
}

// TestHandleDirectResponsesLogsRequest: Playground responses 直连应命中 /responses，
// 原样透传上游响应并解析 usage 写入请求日志。
func TestHandleDirectResponsesLogsRequest(t *testing.T) {
	upstreamPath := ""
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_1","model":"r2","object":"response","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10,"input_tokens_details":{"cached_tokens":2}}}`))
	}))
	defer okSrv.Close()

	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &domain.Config{
		Listen:    ":0",
		LogDetail: "basic",
		Providers: []domain.Provider{
			{ID: "ok", Name: "Ok P", BaseURL: okSrv.URL, APIKey: "k", Enabled: true, Models: []string{"m"}, DefaultModel: "m", Timeout: 2000},
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

	a := &App{
		Cfg:     cm,
		Breaker: circuit.NewBreaker(),
		Client:  NewClient(),
		Logger:  logging.New(cm, sysStore),
		ReqLog:  reqStore,
	}

	body := `{"model":"r1","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":false}`
	req := httptest.NewRequest("POST", "/api/providers/ok/responses", strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.HandleDirectResponses(rec, req, "ok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"output_text"`) {
		t.Fatalf("body not passed through: %s", rec.Body.String())
	}
	if upstreamPath != "/v1/responses" {
		t.Fatalf("upstream path = %q, want /v1/responses", upstreamPath)
	}

	logs, err := reqStore.Query(logstore.QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("request logs = %d, want 1", len(logs))
	}
	l := logs[0]
	if l.Provider != "Ok P" || l.Status != 200 {
		t.Fatalf("provider/status = %q/%d, want Ok P/200", l.Provider, l.Status)
	}
	if l.Model != "r2" {
		t.Fatalf("model = %q, want r2", l.Model)
	}
	if l.PromptTokens != 7 || l.CompletionToks != 3 || l.CachedTokens != 2 {
		t.Fatalf("usage wrong: prompt=%d completion=%d cached=%d", l.PromptTokens, l.CompletionToks, l.CachedTokens)
	}
}
