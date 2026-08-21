package handlers

import (
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"llmproxy/internal/auth"
	"llmproxy/internal/config"
	"llmproxy/internal/domain"
	"llmproxy/internal/logging"
	"llmproxy/internal/logstore"
	"llmproxy/internal/proxy"
)

// Manager 持有管理 API 所需的所有依赖。
type Manager struct {
	Cfg        *config.Manager
	SysStore   *logstore.SystemStore
	ReqStore   *logstore.RequestStore
	Log        *logging.Logger
	Auth       *auth.Admin
	ProxyApp   *proxy.App
}

// NewManager 构造管理 API 处理者。
func NewManager(cfg *config.Manager, sys *logstore.SystemStore, req *logstore.RequestStore,
	log *logging.Logger, adm *auth.Admin, app *proxy.App) *Manager {
	return &Manager{Cfg: cfg, SysStore: sys, ReqStore: req, Log: log, Auth: adm, ProxyApp: app}
}

// --- Admin ---

func (m *Manager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request"})
		return
	}
	cfg := m.Cfg.Get()
	if !cfg.EnableAdminAuth {
		writeJSON(w, 200, map[string]interface{}{"success": true, "token": "no-auth-required", "enableAdminAuth": false})
		return
	}
	if m.Auth.Blocked() {
		m.Log.Log(logging.LevelWarn, "Login blocked: too many failed attempts (5 in 10 minutes). Restart required.", "system", "")
		writeJSON(w, 429, map[string]string{"error": "Too many failed login attempts. Service restart required."})
		return
	}
	target := cfg.AdminPassword
	if target == "" {
		target = "admin"
	}
	if body.Password == target {
		m.Auth.ResetFailures()
		token := m.Auth.CreateSession()
		m.Log.Log(logging.LevelInfo, "Admin login successful.", "system", "")
		writeJSON(w, 200, map[string]interface{}{"success": true, "token": token, "enableAdminAuth": true})
		return
	}
	m.Auth.RecordFailure()
	m.Log.Log(logging.LevelWarn, "Failed admin login attempt ("+itoa(m.Auth.FailureCount())+"/5 in 10 minutes).", "system", "")
	writeJSON(w, 401, map[string]string{"error": "Invalid admin password"})
}

func (m *Manager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token != "" {
		m.Auth.Destroy(token)
	}
	m.Log.Log(logging.LevelInfo, "Admin logged out.", "system", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

func (m *Manager) HandleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := m.Cfg.Get()
	token := extractToken(r)
	authenticated := !cfg.EnableAdminAuth || m.Auth.Validate(token)
	writeJSON(w, 200, map[string]interface{}{
		"enableAdminAuth": cfg.EnableAdminAuth,
		"isAuthenticated": authenticated,
	})
}

// --- Providers ---

func (m *Manager) HandleListProviders(w http.ResponseWriter, _ *http.Request) {
	cfg := m.Cfg.Get()
	writeJSON(w, 200, cfg.Providers)
}

func (m *Manager) HandleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var p domain.Provider
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
		return
	}
	if p.ID == "" || p.Name == "" || p.BaseURL == "" {
		writeJSON(w, 400, map[string]string{"error": "Missing required fields (id, name, baseUrl)"})
		return
	}
	p.Enabled = false
	if err := m.Cfg.Update(func(c *domain.Config) {
		c.Providers = append(c.Providers, p)
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	m.Log.Log(logging.LevelInfo, "Created provider: "+p.Name+" ("+p.ID+")", "system", "")
	writeJSON(w, 201, p)
}

func (m *Manager) HandleGetProvider(w http.ResponseWriter, r *http.Request, id string) {
	cfg := m.Cfg.Get()
	for _, p := range cfg.Providers {
		if p.ID == id {
			writeJSON(w, 200, p)
			return
		}
	}
	writeJSON(w, 404, map[string]string{"error": "Provider not found"})
}

func (m *Manager) HandleUpdateProvider(w http.ResponseWriter, r *http.Request, id string) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
		return
	}
	if err := m.Cfg.Update(func(c *domain.Config) {
		for i := range c.Providers {
			if c.Providers[i].ID == id {
				applyPatch(&c.Providers[i], body)
				c.Providers[i].ID = id // prevent id change
				break
			}
		}
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	cfg := m.Cfg.Get()
	for _, p := range cfg.Providers {
		if p.ID == id {
			m.Log.Log(logging.LevelInfo, "Updated provider: "+p.Name+" ("+p.ID+")", "system", "")
			writeJSON(w, 200, p)
			return
		}
	}
	writeJSON(w, 404, map[string]string{"error": "Provider not found"})
}

func (m *Manager) HandleDeleteProvider(w http.ResponseWriter, _ *http.Request, id string) {
	deleted := false
	if err := m.Cfg.Update(func(c *domain.Config) {
		for i := range c.Providers {
			if c.Providers[i].ID == id {
				c.Providers = append(c.Providers[:i], c.Providers[i+1:]...)
				deleted = true
				return
			}
		}
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !deleted {
		writeJSON(w, 404, map[string]string{"error": "Provider not found"})
		return
	}
	m.Log.Log(logging.LevelInfo, "Deleted provider: "+id, "system", "")
	writeJSON(w, 200, map[string]interface{}{"deleted": true})
}

func (m *Manager) HandleEnableProvider(w http.ResponseWriter, _ *http.Request, id string) {
	found := false
	if err := m.Cfg.Update(func(c *domain.Config) {
		for i := range c.Providers {
			c.Providers[i].Enabled = (c.Providers[i].ID == id)
			if c.Providers[i].ID == id {
				found = true
			}
		}
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, 404, map[string]string{"error": "Provider not found"})
		return
	}
	m.Log.Log(logging.LevelInfo, "Enabled provider: "+id+", others disabled.", "system", "")
	writeJSON(w, 200, map[string]interface{}{"enabled": id})
}

// --- Settings ---

func (m *Manager) HandleGetSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := m.Cfg.Get()
	writeJSON(w, 200, cfg.ToSettings())
}

func (m *Manager) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EnableVirtualKey *bool   `json:"enableVirtualKey"`
		EnableAdminAuth  *bool   `json:"enableAdminAuth"`
		AdminPassword    *string `json:"adminPassword"`
		LogDetail        *string `json:"logDetail"`
		LogBody          *bool   `json:"logBody"`
		MaxLogSizeMB     *int    `json:"maxLogSizeMB"`
		MaxRequestLogs   *int    `json:"maxRequestLogs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
		return
	}
	if err := m.Cfg.Update(func(c *domain.Config) {
		if body.EnableVirtualKey != nil {
			c.EnableVirtualKey = *body.EnableVirtualKey
		}
		if body.EnableAdminAuth != nil {
			if c.EnableAdminAuth != *body.EnableAdminAuth {
				m.Auth.Clear()
			}
			c.EnableAdminAuth = *body.EnableAdminAuth
		}
		if body.AdminPassword != nil && strings.TrimSpace(*body.AdminPassword) != "" {
			c.AdminPassword = strings.TrimSpace(*body.AdminPassword)
			m.Auth.Clear()
			m.Log.Log(logging.LevelInfo, "Admin password updated. All existing sessions cleared.", "system", "")
		}
		if body.LogDetail != nil {
			valid := map[string]bool{"off": true, "basic": true, "error": true, "all": true}
			if valid[*body.LogDetail] {
				c.LogDetail = *body.LogDetail
			}
		}
		if body.LogBody != nil {
			c.LogBody = *body.LogBody
		}
		if body.MaxLogSizeMB != nil && *body.MaxLogSizeMB > 0 {
			c.MaxLogSizeMB = *body.MaxLogSizeMB
		}
		if body.MaxRequestLogs != nil && *body.MaxRequestLogs > 0 {
			c.MaxRequestLogs = *body.MaxRequestLogs
		}
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	cfg := m.Cfg.Get()
	m.Log.Log(logging.LevelInfo, "Settings updated: enableVirtualKey="+btoa(cfg.EnableVirtualKey)+", enableAdminAuth="+btoa(cfg.EnableAdminAuth)+", logDetail="+cfg.LogDetail+", logBody="+btoa(cfg.LogBody)+", maxLogSizeMB="+itoa(cfg.MaxLogSizeMB), "system", "")
	writeJSON(w, 200, cfg.ToSettings())
}

// --- Keys ---

func (m *Manager) HandleListKeys(w http.ResponseWriter, _ *http.Request) {
	cfg := m.Cfg.Get()
	if cfg.Keys == nil {
		writeJSON(w, 200, []domain.VirtualKey{})
		return
	}
	writeJSON(w, 200, cfg.Keys)
}

func (m *Manager) HandleCreateKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string   `json:"name"`
		ProviderIDs []string `json:"providerIds"`
		GroupID     string   `json:"groupId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
		return
	}
	if body.Name == "" {
		writeJSON(w, 400, map[string]string{"error": "Key name is required"})
		return
	}
	b := make([]byte, 24)
	rand.Read(b)
	key := "sk-proxy-" + strings.TrimRight(strings.ReplaceAll(string(b), "/", "a"), "=")
	// Use URL-safe base64
	var b64 [32]byte
	for i := range b64 {
		b64[i] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"[int(b[i%len(b)])%64]
	}
	key = "sk-proxy-" + string(b64[:])
	vk := domain.VirtualKey{
		Key:         key,
		Name:        body.Name,
		GroupID:     body.GroupID,
		ProviderIDs: body.ProviderIDs,
		CreatedAt:   time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if err := m.Cfg.Update(func(c *domain.Config) {
		c.Keys = append(c.Keys, vk)
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	m.Log.Log(logging.LevelInfo, "Created virtual key: "+body.Name+" ("+key[:12]+"...)", "system", "")
	writeJSON(w, 201, vk)
}

func (m *Manager) HandleDeleteKey(w http.ResponseWriter, _ *http.Request, key string) {
	deleted := false
	if err := m.Cfg.Update(func(c *domain.Config) {
		for i := range c.Keys {
			if c.Keys[i].Key == key {
				c.Keys = append(c.Keys[:i], c.Keys[i+1:]...)
				deleted = true
				return
			}
		}
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !deleted {
		writeJSON(w, 404, map[string]string{"error": "Virtual key not found"})
		return
	}
	m.Log.Log(logging.LevelInfo, "Deleted virtual key", "system", "")
	writeJSON(w, 200, map[string]interface{}{"deleted": true})
}

func (m *Manager) HandleUpdateKey(w http.ResponseWriter, r *http.Request, key string) {
	var body struct {
		Name        string   `json:"name"`
		ProviderIDs []string `json:"providerIds"`
		GroupID     string   `json:"groupId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
		return
	}
	if body.Name == "" {
		writeJSON(w, 400, map[string]string{"error": "Key name is required"})
		return
	}
	found := false
	if err := m.Cfg.Update(func(c *domain.Config) {
		for i := range c.Keys {
			if c.Keys[i].Key == key {
				c.Keys[i].Name = body.Name
				c.Keys[i].ProviderIDs = body.ProviderIDs
				c.Keys[i].GroupID = body.GroupID
				found = true
				return
			}
		}
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, 404, map[string]string{"error": "Virtual key not found"})
		return
	}
	m.Log.Log(logging.LevelInfo, "Updated virtual key: "+body.Name, "system", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// --- System Logs ---

func (m *Manager) HandleListLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 15
	}
	sinceID, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	logs, total, err := m.SysStore.Query(q.Get("level"), q.Get("category"), q.Get("requestId"), sinceID, limit)
	if err != nil {
		// 回退到空结果而不是报错
		logs = []logstore.SystemLog{}
	}
	writeJSON(w, 200, map[string]interface{}{
		"logs":  logs,
		"total": total,
	})
}

func (m *Manager) HandleLogsStatus(w http.ResponseWriter, _ *http.Request) {
	count, _ := m.SysStore.Count()
	writeJSON(w, 200, map[string]interface{}{
		"activeFile":  m.Cfg.Get().ActiveLogFile,
		"file1Size":   m.SysStore.Size(), // 单库文件大小
		"file2Size":   0,
		"maxLogSizeMB": m.Cfg.Get().MaxLogSizeMB,
		"totalLogs":    count,
	})
}

func (m *Manager) HandleClearLogs(w http.ResponseWriter, _ *http.Request) {
	_ = m.SysStore.Clear()
	m.Log.Log(logging.LevelInfo, "All system and proxy logs cleared.", "system", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

func (m *Manager) HandleRotateLogs(w http.ResponseWriter, _ *http.Request) {
	next := 2
	if m.Cfg.Get().ActiveLogFile == 2 {
		next = 1
	}
	_ = m.Cfg.Update(func(c *domain.Config) { c.ActiveLogFile = next })
	_ = m.SysStore.ClearFile(next)
	m.Log.Log(logging.LevelInfo, "Manual log rotation triggered. Active log file switched to file "+itoa(next)+".", "system", "")
	writeJSON(w, 200, map[string]interface{}{"success": true, "activeFile": next})
}

// --- Request Logs ---

func (m *Manager) HandleListRequestLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	f := logstore.QueryFilter{
		Key:      q.Get("key"),
		Model:    q.Get("model"),
		Provider: q.Get("provider"),
		Status:   q.Get("status"),
	}
	if fromStr := q.Get("from"); fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			ms := t.UnixMilli()
			f.From = &ms
		}
	}
	if toStr := q.Get("to"); toStr != "" {
		if tt, err := time.Parse(time.RFC3339, toStr); err == nil {
			ms := tt.UnixMilli()
			f.To = &ms
		}
	}
	logs, err := m.ReqStore.Query(f, limit, offset)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	m.resolveLogDetail(logs)
	writeJSON(w, 200, logs)
}

// resolveLogDetail overwrites the persisted has_detail flags with a real-time
// check against the system log store, so stale or buffered values don't drive
// the Detail buttons in the UI.
func (m *Manager) resolveLogDetail(logs []logstore.RequestLog) {
	ids := make([]string, 0, len(logs))
	for _, l := range logs {
		ids = append(ids, l.RequestID)
	}
	if len(ids) == 0 {
		return
	}
	present, err := m.SysStore.HasRequestIDs(ids)
	if err != nil {
		return
	}
	for i := range logs {
		logs[i].HasDetail = present[logs[i].RequestID]
	}
}

func (m *Manager) HandleRequestLogsStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := logstore.QueryFilter{
		Key:      q.Get("key"),
		Model:    q.Get("model"),
		Provider: q.Get("provider"),
		Status:   q.Get("status"),
	}
	if fromStr := q.Get("from"); fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			ms := t.UnixMilli()
			f.From = &ms
		}
	}
	if toStr := q.Get("to"); toStr != "" {
		if tt, err := time.Parse(time.RFC3339, toStr); err == nil {
			ms := tt.UnixMilli()
			f.To = &ms
		}
	}
	count, prompt, completion, cached, total, err := m.ReqStore.Stats(f)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"count":            count,
		"promptTokens":     prompt,
		"completionTokens": completion,
		"cachedTokens":     cached,
		"totalTokens":      total,
	})
}

func (m *Manager) HandleClearRequestLogs(w http.ResponseWriter, _ *http.Request) {
	_ = m.ReqStore.Clear()
	m.Log.Log(logging.LevelInfo, "All request usage logs cleared.", "system", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// --- Helpers ---

func extractToken(r *http.Request) string {
	if t := r.Header.Get("x-admin-token"); t != "" {
		return t
	}
	return auth.ExtractBearer(r.Header.Get("Authorization"))
}

func applyPatch(p *domain.Provider, body map[string]interface{}) {
	if v, ok := body["name"].(string); ok {
		p.Name = v
	}
	if v, ok := body["baseUrl"].(string); ok {
		p.BaseURL = v
	}
	if v, ok := body["apiKey"].(string); ok {
		p.APIKey = v
	}
	if v, ok := body["enabled"].(bool); ok {
		p.Enabled = v
	}
	if v, ok := body["concurrency"].(float64); ok {
		p.Concurrency = int(v)
	}
	if v, ok := body["timeout"].(float64); ok {
		p.Timeout = int(v)
	}
	if v, ok := body["openaiEndpoint"].(string); ok {
		p.OpenAIEndpoint = v
	}
	if v, ok := body["defaultModel"].(string); ok {
		p.DefaultModel = v
	}
	if v, ok := body["models"]; ok {
		if arr, ok := v.([]interface{}); ok {
			models := make([]string, len(arr))
			for i, x := range arr {
				models[i], _ = x.(string)
			}
			p.Models = models
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func itoa(n int) string { return strconv.Itoa(n) }

func btoa(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Package-level init guards
var _ = os.DevNull
var _ = filepath.Separator

// --- Groups ---

type groupResult struct {
	ProviderID  string `json:"providerId"`
	Model       string `json:"model"`
	Available   bool   `json:"available"`
	FailCount   int    `json:"failCount"`
	CooldownMs  int64  `json:"cooldownMs"`
	RemainingMs int64  `json:"remainingMs"`
}

func (m *Manager) HandleListGroups(w http.ResponseWriter, _ *http.Request) {
	cfg := m.Cfg.Get()
	if cfg.Groups == nil {
		cfg.Groups = []domain.ProviderGroup{}
	}
	writeJSON(w, 200, cfg.Groups)
}

func (m *Manager) HandleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var g domain.ProviderGroup
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
		return
	}
	if g.ID == "" || g.Name == "" {
		writeJSON(w, 400, map[string]string{"error": "Missing required fields (id, name)"})
		return
	}
	if g.Entries == nil {
		g.Entries = []domain.GroupEntry{}
	}
	if err := m.Cfg.Update(func(c *domain.Config) {
		for i := range c.Groups {
			if c.Groups[i].ID == g.ID {
				c.Groups[i].Name = g.Name
				c.Groups[i].Entries = g.Entries
				return
			}
		}
		g.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		c.Groups = append(c.Groups, g)
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	m.Log.Log(logging.LevelInfo, "Created group: "+g.Name+" ("+g.ID+")", "system", "")
	writeJSON(w, 201, g)
}

func (m *Manager) HandleUpdateGroup(w http.ResponseWriter, r *http.Request, id string) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
		return
	}
	if err := m.Cfg.Update(func(c *domain.Config) {
		for i := range c.Groups {
			if c.Groups[i].ID == id {
				if v, ok := body["name"].(string); ok {
					c.Groups[i].Name = v
				}
				if v, ok := body["entries"].([]interface{}); ok {
					entries := make([]domain.GroupEntry, 0, len(v))
					for _, e := range v {
						mm, _ := e.(map[string]interface{})
						pe, _ := mm["providerId"].(string)
						var me []string
						if mv, ok := mm["models"].([]interface{}); ok {
							me = make([]string, len(mv))
							for j, m := range mv {
								if s, ok := m.(string); ok {
									me[j] = s
								}
							}
						}
						entries = append(entries, domain.GroupEntry{ProviderID: pe, Models: me})
					}
					c.Groups[i].Entries = entries
				}
				break
			}
		}
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	cfg := m.Cfg.Get()
	for i := range cfg.Groups {
		if cfg.Groups[i].ID == id {
			m.Log.Log(logging.LevelInfo, "Updated group: "+cfg.Groups[i].Name+" ("+id+")", "system", "")
			writeJSON(w, 200, cfg.Groups[i])
			return
		}
	}
	writeJSON(w, 404, map[string]string{"error": "Group not found"})
}

func (m *Manager) HandleDeleteGroup(w http.ResponseWriter, _ *http.Request, id string) {
	deleted := false
	if err := m.Cfg.Update(func(c *domain.Config) {
		for i := range c.Groups {
			if c.Groups[i].ID == id {
				c.Groups = append(c.Groups[:i], c.Groups[i+1:]...)
				deleted = true
				return
			}
		}
	}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !deleted {
		writeJSON(w, 404, map[string]string{"error": "Group not found"})
		return
	}
	m.Log.Log(logging.LevelInfo, "Deleted group: "+id, "system", "")
	writeJSON(w, 200, map[string]interface{}{"deleted": true})
}

func (m *Manager) HandleGroupHealth(w http.ResponseWriter, _ *http.Request, id string) {
	cfg := m.Cfg.Get()
	var grp *domain.ProviderGroup
	for i := range cfg.Groups {
		if cfg.Groups[i].ID == id {
			grp = &cfg.Groups[i]
			break
		}
	}
	if grp == nil {
		writeJSON(w, 404, map[string]string{"error": "Group not found"})
		return
	}
	providerMap := map[string]domain.Provider{}
	for _, p := range cfg.Providers {
		providerMap[p.ID] = p
	}
	results := []groupResult{}
	snap := m.ProxyApp.Health.Snapshot()
	for _, e := range grp.Entries {
		p, ok := providerMap[e.ProviderID]
		if !ok {
			results = append(results, groupResult{ProviderID: e.ProviderID, Available: false})
			continue
		}
		models := e.Models
		if len(models) == 0 {
			models = p.Models
			if len(models) == 0 && p.DefaultModel != "" {
				models = []string{p.DefaultModel}
			}
		}
		for _, model := range models {
			if model == "" {
				continue
			}
			if s, ok := snap[e.ProviderID+"|"+model]; ok {
				results = append(results, groupResult{
					ProviderID:  e.ProviderID,
					Model:       model,
					Available:   false,
					FailCount:   s.FailCount,
					CooldownMs:  s.CooldownMs,
					RemainingMs: s.RemainingMs,
				})
			} else {
				results = append(results, groupResult{ProviderID: e.ProviderID, Model: model, Available: true})
			}
		}
	}
	writeJSON(w, 200, results)
}

// testResult holds per-model one-shot test outcome.
type testResult struct {
	ProviderID string `json:"providerId"`
	Model      string `json:"model"`
	OK         bool   `json:"ok"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

func (m *Manager) HandleGroupTest(w http.ResponseWriter, r *http.Request, id string) {
	cfg := m.Cfg.Get()
	var grp *domain.ProviderGroup
	for i := range cfg.Groups {
		if cfg.Groups[i].ID == id {
			grp = &cfg.Groups[i]
			break
		}
	}
	if grp == nil {
		writeJSON(w, 404, map[string]string{"error": "Group not found"})
		return
	}
	providerMap := map[string]*proxy.Provider{}
	for _, p := range cfg.Providers {
		providerMap[p.ID] = proxy.ProviderFromDomain(&p)
	}
	results := []testResult{}
	for _, e := range grp.Entries {
		p, ok := providerMap[e.ProviderID]
		if !ok {
			results = append(results, testResult{ProviderID: e.ProviderID, OK: false, Error: "provider not found"})
			continue
		}
		models := e.Models
		if len(models) == 0 {
			models = p.Models
			if len(models) == 0 && p.DefaultModel != "" {
				models = []string{p.DefaultModel}
			}
		}
		for _, model := range models {
			if model == "" {
				continue
			}
			start := time.Now()
			bodyBytes, _ := json.Marshal(map[string]interface{}{
				"model":      model,
				"messages":   []map[string]string{{"role": "user", "content": "hi"}},
				"max_tokens": 1,
			})
			targetURL := proxy.ResolveTargetURL(p.BaseURL, p.OpenAIEndpoint, "/v1/chat/completions")
			resp, err := m.ProxyApp.Client.Do(r.Context(), "POST", targetURL,
				http.Header{"Content-Type": {"application/json"}, "Authorization": {"Bearer " + p.APIKey}},
				strings.NewReader(string(bodyBytes)),
				5*time.Second)
			dur := time.Since(start).Milliseconds()
			if err != nil {
				results = append(results, testResult{ProviderID: e.ProviderID, Model: model, OK: false, Error: err.Error(), DurationMS: dur})
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			results = append(results, testResult{
				ProviderID: e.ProviderID, Model: model,
				OK: resp.StatusCode >= 200 && resp.StatusCode < 300,
				Status: resp.StatusCode, DurationMS: dur,
			})
		}
	}
	writeJSON(w, 200, results)
}