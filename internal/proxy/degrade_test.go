package proxy

import (
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
	"llmproxy/internal/logging"
	"llmproxy/internal/logstore"
)

func TestHandleProxyDegradeLogs(t *testing.T) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"model":"ok-model","usage":{"prompt_tokens":3,"completion_tokens":2}}`))
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
		Keys: []domain.VirtualKey{
			{Key: "sk-degrade", Name: "Degrade Key", ProviderIDs: []string{"fail", "ok"}},
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

	log := logging.New(cm, sysStore)
	a := &App{
		Cfg:     cm,
		Breaker: circuit.NewBreaker(),
		Client:  NewClient(),
		Logger:  log,
		ReqLog:  reqStore,
	}

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-degrade")
	rec := httptest.NewRecorder()

	a.HandleProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	logs, err := reqStore.Query(logstore.QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("request logs = %d, want 2 (degrade + success), got: %+v", len(logs), logs)
	}
	var degrade, success *logstore.RequestLog
	for i := range logs {
		l := &logs[i]
		if l.Provider == "Fail P" {
			degrade = l
		}
		if l.Provider == "Ok P" {
			success = l
		}
	}
	if degrade == nil {
		t.Fatal("no degrade log for Fail P recorded")
	}
	if degrade.Status != 500 {
		t.Fatalf("degrade log status = %d, want 500", degrade.Status)
	}
	if !strings.Contains(degrade.Error, "HTTP 500") {
		t.Fatalf("degrade log error = %q, want HTTP 500", degrade.Error)
	}
	if success == nil {
		t.Fatal("no success log for Ok P recorded")
	}
	if success.Status != 200 {
		t.Fatalf("success log status = %d, want 200", success.Status)
	}
	if degrade.RequestID == "" {
		t.Fatal("degrade log has empty request ID")
	}
	if degrade.RequestID != success.RequestID {
		t.Fatalf("request IDs differ across retry chain: degrade=%q success=%q", degrade.RequestID, success.RequestID)
	}

	sysLogs, _, err := sysStore.Query("", "", degrade.RequestID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(sysLogs) == 0 {
		t.Fatalf("no system logs found for request ID %q", degrade.RequestID)
	}
	for _, sl := range sysLogs {
		if sl.RequestID == nil || *sl.RequestID != degrade.RequestID {
			t.Fatalf("system log %d carries wrong request ID: %+v", sl.ID, sl.RequestID)
		}
	}
	_ = ctx
}

func TestHandleProxySingleProviderNoExtraLog(t *testing.T) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
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
		LogDetail:        "basic",
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

	log := logging.New(cm, sysStore)
	a := &App{
		Cfg:     cm,
		Breaker: circuit.NewBreaker(),
		Client:  NewClient(),
		Logger:  log,
		ReqLog:  reqStore,
	}

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-solo")
	rec := httptest.NewRecorder()

	a.HandleProxy(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	logs, err := reqStore.Query(logstore.QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("request logs = %d, want 1 for single provider", len(logs))
	}
	if logs[0].Provider != "Solo P" {
		t.Fatalf("final log provider = %q, want Solo P", logs[0].Provider)
	}
	if logs[0].Status != 503 {
		t.Fatalf("final log status = %d, want 503", logs[0].Status)
	}
}

// TestHandleProxyPerProviderModel: 每个 provider 每轮都用用户原始 model 判断，
// 不匹配才替换为该 provider 默认模型；下一个 provider 仍用原始 model。
func TestHandleProxyPerProviderModel(t *testing.T) {
	var capturedP1, capturedP2 string
	p1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		_ = r.Body.Close()
		capturedP1 = req.Model
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer p1.Close()

	p2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		_ = r.Body.Close()
		capturedP2 = req.Model
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"model": req.Model})
	}))
	defer p2.Close()

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
			{ID: "p1", Name: "P1", BaseURL: p1.URL, APIKey: "k", Enabled: true, Models: []string{"other-model"}, DefaultModel: "p1-fallback", Timeout: 2000},
			{ID: "p2", Name: "P2", BaseURL: p2.URL, APIKey: "k", Enabled: true, Models: []string{"target-model"}, DefaultModel: "p2-default", Timeout: 2000},
		},
		Keys: []domain.VirtualKey{
			{Key: "sk-multi", Name: "Multi Key", ProviderIDs: []string{"p1", "p2"}},
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

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"target-model","messages":[]}`))
	req.Header.Set("Authorization", "Bearer sk-multi")
	rec := httptest.NewRecorder()
	a.HandleProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if capturedP1 != "p1-fallback" {
		t.Fatalf("p1 received model %q, want p1-fallback (unmatched -> provider default)", capturedP1)
	}
	if capturedP2 != "target-model" {
		t.Fatalf("p2 received model %q, want original target-model (per-provider check)", capturedP2)
	}

	logs, err := reqStore.Query(logstore.QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("request logs = %d, want 2 (p1 fallback + p2 success)", len(logs))
	}
	var p1Log, p2Log *logstore.RequestLog
	for i := range logs {
		if logs[i].Provider == "P1" {
			p1Log = &logs[i]
		}
		if logs[i].Provider == "P2" {
			p2Log = &logs[i]
		}
	}
	if p1Log == nil || p2Log == nil {
		t.Fatalf("missing logs: p1=%v p2=%v", p1Log, p2Log)
	}
	if p1Log.Model != "p1-fallback" {
		t.Fatalf("p1 log model = %q, want p1-fallback", p1Log.Model)
	}
	if p2Log.Model != "target-model" {
		t.Fatalf("p2 log model = %q, want target-model (not polluted by p1)", p2Log.Model)
	}
}

var _ = json.Marshal
