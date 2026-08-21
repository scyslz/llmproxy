package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"llmproxy/internal/circuit"
	"llmproxy/internal/config"
	"llmproxy/internal/domain"
	"llmproxy/internal/lazyhealth"
	"llmproxy/internal/logging"
	"llmproxy/internal/logstore"
	"llmproxy/internal/proxy"
)

func doJSON(m *Manager, method, path string, body interface{}) (*httptest.ResponseRecorder, *http.Request) {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	w := httptest.NewRecorder()
	return w, req
}

func newGroupTestManager(t *testing.T, providers []domain.Provider) *Manager {
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

	cfg := &domain.Config{Listen: ":3000", MaxLogSizeMB: 2, MaxRequestLogs: 1000, ActiveLogFile: 1,
		Providers: providers}
	cfgDir := t.TempDir()
	mgr, err := config.New(cfgDir)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	_ = mgr.Replace(cfg)

	app := &proxy.App{Cfg: mgr, Breaker: circuit.NewBreaker(), Client: proxy.NewClient(),
		Logger: logging.New(mgr, sys), ReqLog: req, Health: lazyhealth.New()}

	return &Manager{Cfg: mgr, SysStore: sys, ReqStore: req, ProxyApp: app,
		Log: logging.New(mgr, sys)}
}

func TestGroupCRUD(t *testing.T) {
	m := newGroupTestManager(t, nil)

	// create
	w, r := doJSON(m, http.MethodPost, "/api/groups", domain.ProviderGroup{
		ID:   "g-1",
		Name: "Primary",
		Entries: []domain.GroupEntry{
			{ProviderID: "p1", Models: []string{"m1"}},
		},
	})
	m.HandleCreateGroup(w, r)
	if w.Code != 201 {
		t.Fatalf("create status = %d, body=%s", w.Code, w.Body.String())
	}

	// duplicate create acts as upsert, keeps original createdAt
	w2, r2 := doJSON(m, http.MethodPost, "/api/groups", domain.ProviderGroup{
		ID:   "g-1",
		Name: "Primary v2",
		Entries: []domain.GroupEntry{
			{ProviderID: "p2"},
		},
	})
	m.HandleCreateGroup(w2, r2)
	if w2.Code != 201 {
		t.Fatalf("upsert status = %d", w2.Code)
	}

	// list
	wl, rl := doJSON(m, http.MethodGet, "/api/groups", nil)
	m.HandleListGroups(wl, rl)
	if wl.Code != 200 {
		t.Fatalf("list status = %d", wl.Code)
	}
	var groups []domain.ProviderGroup
	_ = json.Unmarshal(wl.Body.Bytes(), &groups)
	if len(groups) != 1 {
		t.Fatalf("groups len = %d, want 1", len(groups))
	}
	if groups[0].Name != "Primary v2" {
		t.Fatalf("upserted name = %q", groups[0].Name)
	}
	if groups[0].CreatedAt == "" {
		t.Fatal("createdAt missing")
	}

	// update
	w3, r3 := doJSON(m, http.MethodPut, "/api/groups/g-1", map[string]interface{}{
		"name": "Renamed",
		"entries": []map[string]interface{}{
			{"providerId": "p3", "models": []string{"m3"}},
		},
	})
	m.HandleUpdateGroup(w3, r3, "g-1")
	if w3.Code != 200 {
		t.Fatalf("update status = %d, body=%s", w3.Code, w3.Body.String())
	}
	var updated domain.ProviderGroup
	_ = json.Unmarshal(w3.Body.Bytes(), &updated)
	if updated.Name != "Renamed" || len(updated.Entries) != 1 || updated.Entries[0].ProviderID != "p3" {
		t.Fatalf("updated wrong: %+v", updated)
	}

	// update nonexistent -> 404
	w4, r4 := doJSON(m, http.MethodPut, "/api/groups/nope", map[string]interface{}{"name": "x"})
	m.HandleUpdateGroup(w4, r4, "nope")
	if w4.Code != 404 {
		t.Fatalf("update missing status = %d, want 404", w4.Code)
	}

	// delete
	w5, r5 := doJSON(m, http.MethodDelete, "/api/groups/g-1", nil)
	m.HandleDeleteGroup(w5, r5, "g-1")
	if w5.Code != 200 {
		t.Fatalf("delete status = %d", w5.Code)
	}
	w6, r6 := doJSON(m, http.MethodDelete, "/api/groups/g-1", nil)
	m.HandleDeleteGroup(w6, r6, "g-1")
	if w6.Code != 404 {
		t.Fatalf("delete missing status = %d, want 404", w6.Code)
	}
}

func TestGroupCreateValidation(t *testing.T) {
	m := newGroupTestManager(t, nil)

	// missing id
	w, r := doJSON(m, http.MethodPost, "/api/groups", domain.ProviderGroup{Name: "x"})
	m.HandleCreateGroup(w, r)
	if w.Code != 400 {
		t.Fatalf("missing id status = %d, want 400", w.Code)
	}

	// missing name
	w2, r2 := doJSON(m, http.MethodPost, "/api/groups", domain.ProviderGroup{ID: "g"})
	m.HandleCreateGroup(w2, r2)
	if w2.Code != 400 {
		t.Fatalf("missing name status = %d, want 400", w2.Code)
	}

	// invalid json
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodPost, "/api/groups", bytes.NewBufferString("{bad"))
	m.HandleCreateGroup(w3, r3)
	if w3.Code != 400 {
		t.Fatalf("invalid json status = %d, want 400", w3.Code)
	}
}

func TestGroupHealth(t *testing.T) {
	m := newGroupTestManager(t, []domain.Provider{
		{ID: "p1", Name: "P1", Enabled: true, Models: []string{"m1", "m2"}},
		{ID: "p2", Name: "P2", Enabled: true, Models: []string{"m3"}, DefaultModel: "m3"},
	})
	_ = m.Cfg.Update(func(c *domain.Config) {
		c.Groups = []domain.ProviderGroup{
			{ID: "g", Name: "G", Entries: []domain.GroupEntry{{ProviderID: "p1"}, {ProviderID: "p2"}}},
		}
	})

	// put p1|m1 into cooldown
	m.ProxyApp.Health.RecordFailure("p1", "m1")

	w, r := doJSON(m, http.MethodGet, "/api/groups/g/health", nil)
	m.HandleGroupHealth(w, r, "g")
	if w.Code != 200 {
		t.Fatalf("health status = %d, body=%s", w.Code, w.Body.String())
	}
	var results []groupResult
	_ = json.Unmarshal(w.Body.Bytes(), &results)
	// p1 -> m1 (cooldown, available=false), m2 (available), p2 -> m3 (available)
	if len(results) != 3 {
		t.Fatalf("results len = %d, want 3: %+v", len(results), results)
	}
	byKey := map[string]groupResult{}
	for _, r := range results {
		byKey[r.ProviderID+"|"+r.Model] = r
	}
	if byKey["p1|m1"].Available {
		t.Fatal("p1|m1 should be unavailable (in cooldown)")
	}
	if byKey["p1|m1"].FailCount != 1 {
		t.Fatalf("p1|m1 failCount = %d, want 1", byKey["p1|m1"].FailCount)
	}
	if !byKey["p1|m2"].Available {
		t.Fatal("p1|m2 should be available")
	}
	if !byKey["p2|m3"].Available {
		t.Fatal("p2|m3 should be available")
	}

	// missing group -> 404
	w2, r2 := doJSON(m, http.MethodGet, "/api/groups/nope/health", nil)
	m.HandleGroupHealth(w2, r2, "nope")
	if w2.Code != 404 {
		t.Fatalf("health missing status = %d, want 404", w2.Code)
	}

	// missing provider in group -> marked unavailable
	_ = m.Cfg.Update(func(c *domain.Config) {
		c.Groups = []domain.ProviderGroup{
			{ID: "g2", Name: "G2", Entries: []domain.GroupEntry{{ProviderID: "ghost"}}},
		}
	})
	w3, r3 := doJSON(m, http.MethodGet, "/api/groups/g2/health", nil)
	m.HandleGroupHealth(w3, r3, "g2")
	var results3 []groupResult
	_ = json.Unmarshal(w3.Body.Bytes(), &results3)
	if len(results3) != 1 || results3[0].Available {
		t.Fatalf("ghost provider should be marked unavailable: %+v", results3)
	}
}

func TestGroupTest(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"m"}`))
	}))
	defer up.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()

	m := newGroupTestManager(t, []domain.Provider{
		{ID: "ok", Name: "OK", BaseURL: up.URL, APIKey: "k", Enabled: true, Models: []string{"m"}, DefaultModel: "m", Timeout: 3000},
		{ID: "bad", Name: "Bad", BaseURL: down.URL, APIKey: "k", Enabled: true, Models: []string{"m"}, DefaultModel: "m", Timeout: 3000},
	})
	_ = m.Cfg.Update(func(c *domain.Config) {
		c.Groups = []domain.ProviderGroup{
			{ID: "g", Name: "G", Entries: []domain.GroupEntry{{ProviderID: "ok"}, {ProviderID: "bad"}}},
		}
	})

	w, r := doJSON(m, http.MethodPost, "/api/groups/g/test", nil)
	m.HandleGroupTest(w, r, "g")
	if w.Code != 200 {
		t.Fatalf("test status = %d, body=%s", w.Code, w.Body.String())
	}
	var results []testResult
	_ = json.Unmarshal(w.Body.Bytes(), &results)
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2: %+v", len(results), results)
	}
	var ok, bad *testResult
	for i := range results {
		switch results[i].ProviderID {
		case "ok":
			ok = &results[i]
		case "bad":
			bad = &results[i]
		}
	}
	if ok == nil || !ok.OK || ok.Status != 200 {
		t.Fatalf("ok provider result wrong: %+v", ok)
	}
	if bad == nil || bad.OK || bad.Status != 500 {
		t.Fatalf("bad provider result wrong: %+v", bad)
	}

	// missing group -> 404
	w2, r2 := doJSON(m, http.MethodPost, "/api/groups/nope/test", nil)
	m.HandleGroupTest(w2, r2, "nope")
	if w2.Code != 404 {
		t.Fatalf("test missing status = %d, want 404", w2.Code)
	}
}
