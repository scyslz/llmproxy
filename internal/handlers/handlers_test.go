package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"llmproxy/internal/config"
	"llmproxy/internal/domain"
	"llmproxy/internal/logging"
	"llmproxy/internal/logstore"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "logs")
	sys, err := logstore.OpenSystem(dir)
	if err != nil {
		t.Fatalf("OpenSystem: %v", err)
	}
	req, err := logstore.OpenRequest(dir, 1000)
	if err != nil {
		t.Fatalf("OpenRequest: %v", err)
	}
	t.Cleanup(func() { sys.Close(); req.Close() })

	cfg := &domain.Config{Listen: ":3000", MaxLogSizeMB: 2, MaxRequestLogs: 1000, ActiveLogFile: 1}
	cfgDir := t.TempDir()
	mgr, err := config.New(cfgDir)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	_ = mgr.Replace(cfg)

	return &Manager{Cfg: mgr, SysStore: sys, ReqStore: req, Log: logging.New(mgr, sys)}
}

func doGet(m *Manager, path string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	return w, req
}

func TestHandleListRequestLogs(t *testing.T) {
	m := newTestManager(t)
	base := time.Now().UnixMilli()
	ins := func(ts int64, id, key, model string, status int) {
		_ = m.ReqStore.Insert(&logstore.RequestLog{
			ID: id, Timestamp: time.UnixMilli(ts).UTC().Format("2006-01-02T15:04:05.000Z"),
			KeyName: key, Model: model, Provider: "p", TotalTokens: 10, Status: status,
		})
	}
	ins(base, "id-1", "alice", "gpt", 200)
	ins(base+1, "id-2", "bob", "claude", 500)

	w, req := doGet(m, "/api/request-logs?limit=10")
	m.HandleListRequestLogs(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var logs []logstore.RequestLog
	if err := json.Unmarshal(w.Body.Bytes(), &logs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("len = %d, want 2", len(logs))
	}
	if logs[0].ID != "id-2" {
		t.Fatalf("newest first wrong: %s", logs[0].ID)
	}

	// status filter
	w2, req2 := doGet(m, "/api/request-logs?status=2xx")
	m.HandleListRequestLogs(w2, req2)
	var f []logstore.RequestLog
	_ = json.Unmarshal(w2.Body.Bytes(), &f)
	if len(f) != 1 || f[0].ID != "id-1" {
		t.Fatalf("2xx filter wrong: %+v", f)
	}

	// time range filter (from)
	from := time.UnixMilli(base + 1).UTC().Format("2006-01-02T15:04:05.000Z")
	w3, req3 := doGet(m, "/api/request-logs?from="+from)
	m.HandleListRequestLogs(w3, req3)
	var t2 []logstore.RequestLog
	_ = json.Unmarshal(w3.Body.Bytes(), &t2)
	if len(t2) != 1 || t2[0].ID != "id-2" {
		t.Fatalf("from filter wrong: %+v", t2)
	}
}

func TestHandleRequestLogsStats(t *testing.T) {
	m := newTestManager(t)
	base := time.Now().UnixMilli()
	ins := func(ts int64, id string, pt, ct, cached int) {
		_ = m.ReqStore.Insert(&logstore.RequestLog{
			ID: id, Timestamp: time.UnixMilli(ts).UTC().Format("2006-01-02T15:04:05.000Z"),
			KeyName: "k", Model: "m", PromptTokens: pt, CompletionToks: ct, CachedTokens: cached, TotalTokens: pt + ct,
		})
	}
	ins(base, "id-1", 10, 20, 5)
	ins(base+1, "id-2", 100, 50, 0)

	w, req := doGet(m, "/api/request-logs/stats")
	m.HandleRequestLogsStats(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var out map[string]int64
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["count"] != 2 || out["promptTokens"] != 110 || out["completionTokens"] != 70 || out["cachedTokens"] != 5 || out["totalTokens"] != 180 {
		t.Fatalf("stats wrong: %+v", out)
	}
}

func TestHandleLogsStatus(t *testing.T) {
	m := newTestManager(t)
	rid := "req-x"
	_ = m.SysStore.Insert(time.Now().UnixMilli(), "info", "proxy", 1, "log", &rid)

	w, req := doGet(m, "/api/logs/status")
	m.HandleLogsStatus(w, req)
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["totalLogs"].(float64) != 1 {
		t.Fatalf("totalLogs = %v, want 1", out["totalLogs"])
	}
	if out["maxLogSizeMB"].(float64) != 2 {
		t.Fatalf("maxLogSizeMB = %v, want 2", out["maxLogSizeMB"])
	}
	if out["totalSize"].(float64) <= 0 {
		t.Fatalf("totalSize should be > 0")
	}
}

func TestResolveLogDetailRealtime(t *testing.T) {
	m := newTestManager(t)
	base := time.Now().UnixMilli()
	_ = m.ReqStore.Insert(&logstore.RequestLog{
		ID: "id-1", Timestamp: time.UnixMilli(base).UTC().Format("2006-01-02T15:04:05.000Z"),
		KeyName: "k", Model: "m", RequestID: "req-1", HasDetail: true,
	})
	_ = m.ReqStore.Insert(&logstore.RequestLog{
		ID: "id-2", Timestamp: time.UnixMilli(base + 1).UTC().Format("2006-01-02T15:04:05.000Z"),
		KeyName: "k", Model: "m", RequestID: "req-2", HasDetail: true,
	})

	// no system logs yet -> both should resolve to false
	w, req := doGet(m, "/api/request-logs?limit=10")
	m.HandleListRequestLogs(w, req)
	var logs []logstore.RequestLog
	_ = json.Unmarshal(w.Body.Bytes(), &logs)
	if logs[0].HasDetail || logs[1].HasDetail {
		t.Fatalf("hasDetail should be false without system logs: %+v", logs)
	}

	// add system log for req-1
	rid := "req-1"
	_ = m.SysStore.Insert(time.Now().UnixMilli(), "info", "proxy", 1, "x", &rid)

	w2, req2 := doGet(m, "/api/request-logs?limit=10")
	m.HandleListRequestLogs(w2, req2)
	var logs2 []logstore.RequestLog
	_ = json.Unmarshal(w2.Body.Bytes(), &logs2)
	// newest (id-2/req-2) has no system logs -> false; id-1/req-1 has -> true
	if logs2[0].HasDetail {
		t.Fatalf("id-2 should resolve to false: %+v", logs2[0])
	}
	if !logs2[1].HasDetail {
		t.Fatalf("id-1 should resolve to true: %+v", logs2[1])
	}
}

func TestHandleUpdateSettingsMaxRequestBodyMB(t *testing.T) {
	m := newTestManager(t)
	body, _ := json.Marshal(map[string]int{"maxRequestBodyMB": 32})
	req := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body))
	w := httptest.NewRecorder()
	m.HandleUpdateSettings(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["maxRequestBodyMB"].(float64) != 32 {
		t.Fatalf("maxRequestBodyMB = %v, want 32", out["maxRequestBodyMB"])
	}
	if m.Cfg.Get().MaxRequestBodyMB != 32 {
		t.Fatalf("config maxRequestBodyMB = %d, want 32", m.Cfg.Get().MaxRequestBodyMB)
	}
}

func TestHandleUpdateSettingsAllowedIPs(t *testing.T) {
	m := newTestManager(t)
	body, _ := json.Marshal(map[string]interface{}{
		"allowedIps": []string{"127.0.0.1", "10.0.0.0/8"},
		"trustProxy": true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body))
	w := httptest.NewRecorder()
	m.HandleUpdateSettings(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	cfg := m.Cfg.Get()
	if !cfg.TrustProxy {
		t.Fatal("trustProxy want true")
	}
	if len(cfg.AllowedIPs) != 2 || cfg.AllowedIPs[0] != "127.0.0.1" || cfg.AllowedIPs[1] != "10.0.0.0/8" {
		t.Fatalf("allowedIps = %#v", cfg.AllowedIPs)
	}

	bad, _ := json.Marshal(map[string]interface{}{"allowedIps": []string{"not-an-ip"}})
	w2 := httptest.NewRecorder()
	m.HandleUpdateSettings(w2, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(bad)))
	if w2.Code != 400 {
		t.Fatalf("invalid CIDR status = %d, want 400", w2.Code)
	}
}

func TestHandleUpdateSettingsProxyFlagRoundTrip(t *testing.T) {
	m := newTestManager(t)
	body, _ := json.Marshal(map[string]interface{}{
		"proxyFlagHeader": "X-Proxy-Flag",
		"proxyFlagValue":  "s3cret",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body))
	w := httptest.NewRecorder()
	m.HandleUpdateSettings(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var out domain.Settings
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ProxyFlagHeader != "X-Proxy-Flag" || out.ProxyFlagValue != "s3cret" {
		t.Fatalf("PUT response flags = %q/%q", out.ProxyFlagHeader, out.ProxyFlagValue)
	}

	gw := httptest.NewRecorder()
	m.HandleGetSettings(gw, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if gw.Code != 200 {
		t.Fatalf("GET status = %d body=%s", gw.Code, gw.Body.String())
	}
	var got domain.Settings
	if err := json.Unmarshal(gw.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET unmarshal: %v", err)
	}
	if got.ProxyFlagHeader != "X-Proxy-Flag" || got.ProxyFlagValue != "s3cret" {
		t.Fatalf("GET flags = %q/%q, want X-Proxy-Flag/s3cret", got.ProxyFlagHeader, got.ProxyFlagValue)
	}
}
