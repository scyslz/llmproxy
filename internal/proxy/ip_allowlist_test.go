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

func newAllowlistApp(t *testing.T, cfg *domain.Config) *App {
	t.Helper()
	tmp := t.TempDir()
	cm, err := config.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	sysStore, err := logstore.OpenSystem(tmp + "/system.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sysStore.Close() })
	return &App{
		Cfg:     cm,
		Breaker: circuit.NewBreaker(),
		Client:  NewClient(),
		Logger:  logging.New(cm, sysStore),
	}
}

func allowlistUpstream(t *testing.T) (*httptest.Server, domain.Provider) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"ok","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, domain.Provider{
		ID: "up", Name: "Up", BaseURL: srv.URL, APIKey: "k", Enabled: true,
		Models: domain.Models("m"), DefaultModel: "m", Timeout: 2000,
	}
}

func TestVirtualKeyRequiredWhenAllowlistEmpty(t *testing.T) {
	_, p := allowlistUpstream(t)
	a := newAllowlistApp(t, &domain.Config{
		Listen: ":0", EnableVirtualKey: true, LogDetail: "basic",
		Providers: []domain.Provider{p},
		Keys:      []domain.VirtualKey{{Key: "sk-ok", Name: "K", ProviderIDs: []string{"up"}}},
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req.RemoteAddr = "203.0.113.8:1"
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAllowlistedIPBypassesKey(t *testing.T) {
	_, p := allowlistUpstream(t)
	a := newAllowlistApp(t, &domain.Config{
		Listen: ":0", EnableVirtualKey: true, LogDetail: "basic",
		AllowedIPs: []string{"203.0.113.8"},
		Providers:  []domain.Provider{p},
		Keys:       []domain.VirtualKey{{Key: "sk-ok", Name: "K", ProviderIDs: []string{"up"}}},
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req.RemoteAddr = "203.0.113.8:1"
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestExternalTrafficRequiresKey(t *testing.T) {
	_, p := allowlistUpstream(t)
	// Requests carrying forwarding headers are treated as external traffic:
	// they need a valid key regardless of allowlist.
	a := newAllowlistApp(t, &domain.Config{
		Listen: ":0", EnableVirtualKey: true, LogDetail: "basic",
		AllowedIPs: []string{"198.51.100.9"},
		Providers:  []domain.Provider{p},
		Keys:       []domain.VirtualKey{{Key: "sk-ok", Name: "K", ProviderIDs: []string{"up"}}},
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req.RemoteAddr = "10.0.0.1:1"
	req.Header.Set("X-Real-IP", "198.51.100.9")
	req.Header.Set("Authorization", "Bearer sk-ok")
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("external + valid key status = %d body=%s", rec.Code, rec.Body.String())
	}
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req2.RemoteAddr = "10.0.0.1:1"
	req2.Header.Set("X-Real-IP", "198.51.100.9")
	req2.Header.Set("Authorization", "Bearer wrong")
	rec2 := httptest.NewRecorder()
	a.HandleProxy(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("external + bad key status = %d, want 401", rec2.Code)
	}
	req3 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req3.RemoteAddr = "10.0.0.1:1"
	req3.Header.Set("X-Real-IP", "198.51.100.9")
	rec3 := httptest.NewRecorder()
	a.HandleProxy(rec3, req3)
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("external + no key status = %d, want 401 (allowlist does not apply)", rec3.Code)
	}
}

func TestModelsAllowlistedIPBypassesKey(t *testing.T) {
	_, p := allowlistUpstream(t)
	a := newAllowlistApp(t, &domain.Config{
		Listen: ":0", EnableVirtualKey: true, LogDetail: "basic",
		AllowedIPs: []string{"192.0.2.10"},
		Providers:  []domain.Provider{p},
		Keys:       []domain.VirtualKey{{Key: "sk-ok", Name: "K", ProviderIDs: []string{"up"}}},
	})
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.RemoteAddr = "192.0.2.10:9"
	rec := httptest.NewRecorder()
	a.HandleModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInternalTrafficUsesAllowlistAndKey(t *testing.T) {
	_, p := allowlistUpstream(t)
	// No forwarding headers = internal direct traffic: TCP peer is matched
	// against the allowlist; a valid key must still be honored.
	a := newAllowlistApp(t, &domain.Config{
		Listen: ":0", EnableVirtualKey: true, LogDetail: "basic",
		AllowedIPs: []string{"198.51.100.9"},
		Providers:  []domain.Provider{p},
		Keys:       []domain.VirtualKey{{Key: "sk-ok", Name: "K", ProviderIDs: []string{"up"}}},
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req.RemoteAddr = "198.51.100.9:1"
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("allowlisted peer status = %d body=%s", rec.Code, rec.Body.String())
	}
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req2.RemoteAddr = "10.0.0.5:1"
	req2.Header.Set("Authorization", "Bearer sk-ok")
	rec2 := httptest.NewRecorder()
	a.HandleProxy(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("external valid key status = %d body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestLoopbackAlwaysAllowed(t *testing.T) {
	_, p := allowlistUpstream(t)
	a := newAllowlistApp(t, &domain.Config{
		Listen: ":0", EnableVirtualKey: true, LogDetail: "basic",
		Providers: []domain.Provider{p},
		Keys:      []domain.VirtualKey{{Key: "sk-ok", Name: "K", ProviderIDs: []string{"up"}}},
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("loopback status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTrustedProxyMissingHeadersFallsBackToRemoteAddr(t *testing.T) {
	_, p := allowlistUpstream(t)
	// No forwarding headers = internal direct traffic: TCP peer is matched
	// against the allowlist; a valid key must still be honored.
	a := newAllowlistApp(t, &domain.Config{
		Listen: ":0", EnableVirtualKey: true, LogDetail: "basic",
		AllowedIPs: []string{"198.51.100.9"},
		Providers:  []domain.Provider{p},
		Keys:       []domain.VirtualKey{{Key: "sk-ok", Name: "K", ProviderIDs: []string{"up"}}},
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req.RemoteAddr = "198.51.100.9:1"
	req.Header.Set("X-Proxy-Flag", "s3cret")
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("allowlisted peer status = %d body=%s", rec.Code, rec.Body.String())
	}
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req2.RemoteAddr = "10.0.0.5:1"
	req2.Header.Set("X-Proxy-Flag", "s3cret")
	req2.Header.Set("Authorization", "Bearer sk-ok")
	rec2 := httptest.NewRecorder()
	a.HandleProxy(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("valid key status = %d body=%s", rec2.Code, rec2.Body.String())
	}
	req3 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req3.RemoteAddr = "10.0.0.5:1"
	rec3 := httptest.NewRecorder()
	a.HandleProxy(rec3, req3)
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("internal non-allowlisted no key status = %d, want 401", rec3.Code)
	}
}
