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

// TestConvertedResponsesToChatLogsCachedTokens 覆盖 responses->chat 转写路径：
// 上游 chat usage 里的缓存命中数必须写入请求日志。标准网关放在
// prompt_tokens_details，DeepSeek 风格放在 prompt_cache_hit_tokens。
func TestConvertedResponsesToChatLogsCachedTokens(t *testing.T) {
	cases := []struct {
		name       string
		stream     bool
		upstream   string
		contentTyp string
		wantCached int
	}{
		{
			name:       "non-stream deepseek top-level",
			upstream:   `{"id":"c1","object":"chat.completion","created":1,"model":"up-model","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105,"prompt_cache_hit_tokens":80}}`,
			contentTyp: "application/json",
			wantCached: 80,
		},
		{
			name:   "stream standard details",
			stream: true,
			upstream: "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"up-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":null}]}\n\n" +
				"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"up-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"up-model\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":5,\"total_tokens\":105,\"prompt_tokens_details\":{\"cached_tokens\":70}}}\n\n" +
				"data: [DONE]\n\n",
			contentTyp: "text/event-stream",
			wantCached: 70,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstreamPath := ""
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamPath = r.URL.Path
				w.Header().Set("Content-Type", tc.contentTyp)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.upstream))
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
						Models: []domain.Model{{ID: "m", Protocol: "chat"}}, DefaultModel: "m", Timeout: 5000,
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

			body := `{"model":"m","input":"hi","stream":` + boolString(tc.stream) + `}`
			req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer sk-cached")
			rec := httptest.NewRecorder()
			a.HandleProxy(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if upstreamPath != "/v1/chat/completions" {
				t.Fatalf("upstream path = %q, want /v1/chat/completions", upstreamPath)
			}

			logs, err := reqStore.Query(logstore.QueryFilter{}, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(logs) != 1 {
				t.Fatalf("request logs = %d, want 1", len(logs))
			}
			l := logs[0]
			if l.PromptTokens != 100 || l.CompletionToks != 5 || l.CachedTokens != tc.wantCached {
				t.Fatalf("usage wrong: prompt=%d completion=%d cached=%d, want cached=%d", l.PromptTokens, l.CompletionToks, l.CachedTokens, tc.wantCached)
			}
			if l.UpstreamModel != "up-model" {
				t.Fatalf("upstream model = %q, want up-model", l.UpstreamModel)
			}
		})
	}
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

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
				Models: []domain.Model{{ID: "m", Protocol: "responses"}}, DefaultModel: "m", Timeout: 5000,
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
