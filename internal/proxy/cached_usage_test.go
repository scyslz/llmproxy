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

// TestConvertedStreamLogsCachedTokens 覆盖 chat->responses 转写路径：
// 上游用非标准 {"Type":...,"Payload":...} 信封返回终态事件，且缓存命中数
// 只出现在 input_tokens_details 里。修复前请求日志的 cached/token 恒为 0。
func TestConvertedStreamLogsCachedTokens(t *testing.T) {
	var sse strings.Builder
	sse.WriteString("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"up-model\",\"status\":\"in_progress\",\"usage\":null}}\n\n")
	sse.WriteString("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
	sse.WriteString("data: {\"Type\":\"response.completed\",\"Payload\":{\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"model\":\"up-model\",\"status\":\"completed\",\"usage\":{\"input_tokens\":5236,\"output_tokens\":16,\"total_tokens\":5252,\"prompt_tokens_details\":{\"cached_tokens\":0},\"input_tokens_details\":{\"cached_tokens\":4992}}}}}\n\n")
	sse.WriteString("data: [DONE]\n\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sse.String()))
	}))
	defer srv.Close()

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
			{
				ID: "up", Name: "Up P", BaseURL: srv.URL, APIKey: "k", Enabled: true,
				Models: []string{"m"}, DefaultModel: "m", Timeout: 5000,
				ModelProtocols: map[string]string{"m": "responses"},
			},
		},
		Keys: []domain.VirtualKey{{Key: "sk-cached", Name: "Cached Key", ProviderIDs: []string{"up"}}},
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

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-cached")
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
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
	if l.PromptTokens != 5236 || l.CompletionToks != 16 || l.CachedTokens != 4992 {
		t.Fatalf("usage wrong: prompt=%d completion=%d cached=%d", l.PromptTokens, l.CompletionToks, l.CachedTokens)
	}
	if l.UpstreamModel != "up-model" {
		t.Fatalf("upstream model = %q, want up-model", l.UpstreamModel)
	}
}
