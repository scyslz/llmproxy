package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"llmproxy/internal/auth"
	"llmproxy/internal/config"
	"llmproxy/internal/handlers"
	"llmproxy/internal/logging"
	"llmproxy/internal/logstore"
	"llmproxy/internal/proxy"
)

//go:embed all:webui/dist
var webFS embed.FS

// Run 启动 HTTP 服务。
func Run(cfg *config.Manager, sysStore *logstore.SystemStore, reqStore *logstore.RequestStore,
	logger *logging.Logger, adm *auth.Admin, proxyApp *proxy.App) {

	hand := handlers.NewManager(cfg, sysStore, reqStore, logger, adm, proxyApp)

	// 中间件链：CORS + admin 认证
	mux := http.NewServeMux()
	handler := adminAuthMiddleware(corsMiddleware(mux), adm, cfg, logger)

	// --- Admin ---
	mux.HandleFunc("/api/admin/login", hand.HandleLogin)
	mux.HandleFunc("/api/admin/logout", hand.HandleLogout)
	mux.HandleFunc("/api/admin/status", hand.HandleStatus)

	// --- Providers ---
	mux.HandleFunc("/api/providers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			hand.HandleListProviders(w, r)
		case "POST":
			hand.HandleCreateProvider(w, r)
		default:
			http.Error(w, "Method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/providers/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/providers/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		id := parts[0]
		if len(parts) >= 2 {
			switch parts[1] {
			case "enable":
				hand.HandleEnableProvider(w, r, id)
				return
			case "chat":
				proxyApp.HandleDirectChat(w, r, id)
				return
			case "responses":
				proxyApp.HandleDirectResponses(w, r, id)
				return
			}
		}
		// 处理 /api/providers/fetch-remote-models
		if id == "fetch-remote-models" {
			proxyApp.FetchRemoteModels(w, r)
			return
		}
		switch r.Method {
		case "GET":
			hand.HandleGetProvider(w, r, id)
		case "PUT":
			hand.HandleUpdateProvider(w, r, id)
		case "DELETE":
			hand.HandleDeleteProvider(w, r, id)
		default:
			http.Error(w, "Method not allowed", 405)
		}
	})

	// --- Settings ---
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			hand.HandleGetSettings(w, r)
		case "PUT":
			hand.HandleUpdateSettings(w, r)
		default:
			http.Error(w, "Method not allowed", 405)
		}
	})

	// --- Keys ---
	mux.HandleFunc("/api/keys", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			hand.HandleListKeys(w, r)
		case "POST":
			hand.HandleCreateKey(w, r)
		default:
			http.Error(w, "Method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/keys/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/api/keys/")
		if key == "" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "DELETE":
			hand.HandleDeleteKey(w, r, key)
		case "PUT":
			hand.HandleUpdateKey(w, r, key)
		default:
			http.Error(w, "Method not allowed", 405)
		}
	})

	// --- Groups ---
	mux.HandleFunc("/api/groups", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			hand.HandleListGroups(w, r)
		case "POST":
			hand.HandleCreateGroup(w, r)
		default:
			http.Error(w, "Method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/groups/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/groups/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		id := parts[0]
		if len(parts) >= 2 {
			switch parts[1] {
			case "health":
				hand.HandleGroupHealth(w, r, id)
				return
			case "test":
				hand.HandleGroupTest(w, r, id)
				return
			}
		}
		switch r.Method {
		case "GET":
			http.NotFound(w, r)
		case "PUT":
			hand.HandleUpdateGroup(w, r, id)
		case "DELETE":
			hand.HandleDeleteGroup(w, r, id)
		default:
			http.Error(w, "Method not allowed", 405)
		}
	})

	// --- Logs ---
	// 注意：/api/logs/* 路由注册顺序靠前的优先匹配
	mux.HandleFunc("/api/logs/status", hand.HandleLogsStatus)
	mux.HandleFunc("/api/logs/clear", hand.HandleClearLogs)
	mux.HandleFunc("/api/logs/rotate", hand.HandleRotateLogs)
	mux.HandleFunc("/api/logs", hand.HandleListLogs)

	// --- Request Logs ---
	mux.HandleFunc("/api/request-logs/stats", hand.HandleRequestLogsStats)
	mux.HandleFunc("/api/request-logs/clear", hand.HandleClearRequestLogs)
	mux.HandleFunc("/api/request-logs", hand.HandleListRequestLogs)

	// --- Proxy core ---
	mux.HandleFunc("/v1/models", proxyApp.HandleModels)
	mux.HandleFunc("/v1/", proxyApp.HandleProxy)
	mux.HandleFunc("/chat/completions", proxyApp.HandleProxy)
	mux.HandleFunc("/responses", proxyApp.HandleProxy)
	mux.HandleFunc("/v1/responses", proxyApp.HandleProxy)
	mux.HandleFunc("/v1/chat/completions", proxyApp.HandleProxy)

	// --- Static files (SPA) ---
	staticFS, err := fs.Sub(webFS, "webui/dist")
	if err != nil {
		log.Fatalf("Failed to open embedded webui: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") || strings.HasPrefix(r.URL.Path, "/v1") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		f, err := staticFS.Open(path)
		if err != nil {
			r.URL.Path = "/"
		} else {
			f.Close()
		}
		fileServer.ServeHTTP(w, r)
	})

	cfgVal := cfg.Get()
	listen := cfgVal.Listen
	if listen == "" {
		listen = ":3000"
	}
	logger.Log(logging.LevelInfo, "LLM Proxy System listening on http://0.0.0.0"+listen, "system", "")
	if err := http.ListenAndServe(listen, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token")
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func adminAuthMiddleware(next http.Handler, adm *auth.Admin, cfg *config.Manager, logger *logging.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		for _, exempt := range adminExempt {
			if r.URL.Path == exempt {
				next.ServeHTTP(w, r)
				return
			}
		}
		serverCfg := cfg.Get()
		if !serverCfg.EnableAdminAuth {
			next.ServeHTTP(w, r)
			return
		}
		token := ""
		if t := r.Header.Get("x-admin-token"); t != "" {
			token = t
		} else {
			token = auth.ExtractBearer(r.Header.Get("Authorization"))
		}
		if token == "" || !adm.Validate(token) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":        "Unauthorized: Admin authentication required for management API",
				"requireLogin": true,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

var adminExempt = []string{"/api/admin/login", "/api/admin/logout", "/api/admin/status"}