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

// TestProxyForwardsUserAgentOnConvertPath 客户端 UA 必须透传到上游：
// /v1/chat/completions 走 forwardOnce，过去只发 Content-Type/Authorization，
// Go 会把它替换成 Go-http-client/1.1。
func TestProxyForwardsUserAgentOnConvertPath(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
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
			{ID: "up", Name: "Up P", BaseURL: srv.URL, APIKey: "k", Enabled: true, Models: domain.Models("m"), DefaultModel: "m", Timeout: 5000},
		},
		Keys: []domain.VirtualKey{{Key: "sk-ua", Name: "UA Key", ProviderIDs: []string{"up"}}},
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

	a := &App{Cfg: cm, Breaker: circuit.NewBreaker(), Client: NewClient(), Logger: logging.New(cm, sysStore), ReqLog: reqStore}

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-ua")
	req.Header.Set("User-Agent", "MyClient/9.9")
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotUA != "MyClient/9.9" {
		t.Fatalf("upstream User-Agent = %q, want MyClient/9.9", gotUA)
	}
}
