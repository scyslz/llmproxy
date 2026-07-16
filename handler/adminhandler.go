package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"llmproxy/config"
)

// ProvidersAPI 处理 /api/providers 管理 API（RESTful）
// GET  /api/providers          → 列表
// POST /api/providers          → 新建
// GET  /api/providers/:id      → 获取单个
// PUT  /api/providers/:id      → 更新
// DELETE /api/providers/:id    → 删除
// POST /api/providers/:id/enable → 启用（互斥）
func ProvidersAPI(w http.ResponseWriter, r *http.Request) {
	// 去掉前缀 /api/providers
	path := strings.TrimPrefix(r.URL.Path, "/api/providers")
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")

	if path == "" {
		// /api/providers
		switch r.Method {
		case "GET":
			providersList(w, r)
		case "POST":
			providerCreate(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, 405)
		}
		return
	}

	// 解析 :id 和可选子资源
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}

	if sub == "" {
		// /api/providers/:id
		switch r.Method {
		case "GET":
			providerGet(w, r, id)
		case "PUT":
			providerUpdate(w, r, id)
		case "DELETE":
			providerDelete(w, r, id)
		default:
			http.Error(w, `{"error":"method not allowed"}`, 405)
		}
		return
	}

	// /api/providers/:id/enable
	if sub == "enable" && r.Method == "POST" {
		providerEnable(w, r, id)
		return
	}

	http.Error(w, `{"error":"not found"}`, 404)
}

// providersList 列出所有 providers（包括 disabled）
func providersList(w http.ResponseWriter, r *http.Request) {
	list := make([]config.Provider, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		list = append(list, p)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// providerCreate 新建 provider
func providerCreate(w http.ResponseWriter, r *http.Request) {
	var p config.Provider
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, 400)
		return
	}

	providers[p.ID] = &p
	cfg.Providers = append(cfg.Providers, p)
	saveConfig()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(p)
}

// providerGet 获取单个 provider
func providerGet(w http.ResponseWriter, r *http.Request, id string) {
	for _, p := range cfg.Providers {
		if p.ID == id {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(p)
			return
		}
	}
	http.Error(w, `{"error":"provider not found"}`, 404)
}

// providerUpdate 更新 provider（PUT 全量更新）
func providerUpdate(w http.ResponseWriter, r *http.Request, id string) {
	var p config.Provider
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, 400)
		return
	}

	found := false
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == id {
			p.Enabled = cfg.Providers[i].Enabled
			cfg.Providers[i] = p
			providers[id] = &cfg.Providers[i]
			found = true
			break
		}
	}
	if !found {
		http.Error(w, `{"error":"provider not found"}`, 404)
		return
	}

	saveConfig()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

// providerDelete 删除 provider
func providerDelete(w http.ResponseWriter, r *http.Request, id string) {
	found := false
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == id {
			found = true
			cfg.Providers = append(cfg.Providers[:i], cfg.Providers[i+1:]...)
			break
		}
	}
	if !found {
		http.Error(w, `{"error":"provider not found"}`, 404)
		return
	}

	delete(providers, id)
	saveConfig()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"deleted": "true"})
}

// providerEnable 启用 provider（互斥：启用这个，其他自动禁用）
func providerEnable(w http.ResponseWriter, r *http.Request, id string) {
	found := false
	for _, p := range cfg.Providers {
		if p.ID == id {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, `{"error":"provider not found"}`, 404)
		return
	}

	for i := range cfg.Providers {
		if cfg.Providers[i].ID == id {
			cfg.Providers[i].Enabled = true
			providers[id] = &cfg.Providers[i]
		} else {
			cfg.Providers[i].Enabled = false
		
		}
	}

	saveConfig()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"enabled": id})
}

// saveConfig 保存到 config.json
func saveConfig() {
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile("config.json", data, 0644)
}

// IndexHandler 返回 Web UI（SPA 模式，所有路径都返回 index.html）
func IndexHandler(w http.ResponseWriter, r *http.Request) {
	 socureId := r.URL.Path[1:]
	if socureId == "" {
	socureId = "index.html"
	}
	 http.ServeFile(w, r, "web/"+socureId)
}
