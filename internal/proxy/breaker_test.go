package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llmproxy/internal/circuit"
	"llmproxy/internal/config"
	"llmproxy/internal/domain"
	"llmproxy/internal/lazyhealth"
	"llmproxy/internal/logging"
	"llmproxy/internal/logstore"
)

func newBreakerTestApp(t *testing.T, cfg *domain.Config) (*App, func()) {
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
	app := &App{
		Cfg:     cm,
		Breaker: circuit.NewBreaker(),
		Client:  NewClient(),
		Logger:  logging.New(cm, sysStore),
		Health:  lazyhealth.New(),
	}
	return app, func() { _ = sysStore.Close() }
}

func singleProviderConfig(srv *httptest.Server) *domain.Config {
	return &domain.Config{
		Listen:           ":0",
		EnableVirtualKey: true,
		LogDetail:        "basic",
		Providers: []domain.Provider{
			{ID: "p1", Name: "P1", BaseURL: srv.URL, APIKey: "k", Enabled: true,
				Models: domain.Models("m"), DefaultModel: "m", Timeout: 2000},
		},
		Keys: []domain.VirtualKey{
			{Key: "sk-test", Name: "Test Key", ProviderIDs: []string{"p1"}},
		},
	}
}

func groupConfig(srv *httptest.Server) *domain.Config {
	cfg := singleProviderConfig(srv)
	cfg.Keys = []domain.VirtualKey{
		{Key: "sk-group", Name: "Group Key", GroupID: "g1"},
	}
	cfg.Groups = []domain.ProviderGroup{
		{ID: "g1", Name: "G1", Entries: []domain.GroupEntry{
			{ProviderID: "p1", Models: []string{"m"}},
		}},
	}
	return cfg
}

func doChat(app *App, key string) *httptest.ResponseRecorder {
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	app.HandleProxy(rec, req)
	return rec
}

func TestIsBreakerFailure(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   bool
	}{
		{http.StatusBadRequest, false},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusNotFound, false},
		{http.StatusUnprocessableEntity, false},
		{http.StatusRequestTimeout, true},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusGatewayTimeout, true},
	} {
		if got := isBreakerFailure(tc.status); got != tc.want {
			t.Errorf("isBreakerFailure(%d) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestNonGroup4xxDoesNotTripBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	app, cleanup := newBreakerTestApp(t, singleProviderConfig(srv))
	defer cleanup()

	rec := doChat(app, "sk-test")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if app.Breaker.InCooldown("p1", scopeChat) {
		t.Fatal("404 must not trip the chat breaker")
	}
}

func TestNonGroup5xxTripsOnlyItsScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	app, cleanup := newBreakerTestApp(t, singleProviderConfig(srv))
	defer cleanup()

	rec := doChat(app, "sk-test")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !app.Breaker.InCooldown("p1", scopeChat) {
		t.Fatal("500 should trip the chat breaker")
	}
	if app.Breaker.InCooldown("p1", scopeResponses) {
		t.Fatal("chat failure must not trip the responses breaker")
	}
	if app.Breaker.InCooldown("p1", scopePassthrough) {
		t.Fatal("chat failure must not trip the passthrough breaker")
	}
}

func TestGroupRequestIgnoresAndDoesNotWriteGlobalBreaker(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()

	app, cleanup := newBreakerTestApp(t, groupConfig(okSrv))
	defer cleanup()

	app.Breaker.RecordFailure("p1", scopeChat)
	rec := doChat(app, "sk-group")
	if rec.Code != http.StatusOK {
		t.Fatalf("group request status = %d, want 200 (must ignore global breaker)", rec.Code)
	}
	if !app.Breaker.InCooldown("p1", scopeChat) {
		t.Fatal("group success must not clear the global breaker")
	}

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	app2, cleanup2 := newBreakerTestApp(t, groupConfig(failSrv))
	defer cleanup2()

	rec2 := doChat(app2, "sk-group")
	if rec2.Code != http.StatusInternalServerError {
		t.Fatalf("group failure status = %d, want 500", rec2.Code)
	}
	if app2.Breaker.InCooldown("p1", scopeChat) {
		t.Fatal("group failure must not trip the global breaker")
	}
	if !app2.Health.InCooldown("p1", "m") {
		t.Fatal("group failure should still trip lazyhealth")
	}
}

func TestProbeChatDoesNotTripGlobalBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	app, cleanup := newBreakerTestApp(t, singleProviderConfig(srv))
	defer cleanup()

	cfg := app.Cfg.Get()
	p := ProviderFromDomain(&cfg.Providers[0])
	ok, status, _, _ := app.ProbeChat(context.Background(), p, "m", true)
	if ok {
		t.Fatal("probe should fail")
	}
	if status != http.StatusInternalServerError {
		t.Fatalf("probe status = %d, want 500", status)
	}
	if app.Breaker.InCooldown("p1", scopeChat) {
		t.Fatal("group test probe must not trip the global breaker")
	}
}
