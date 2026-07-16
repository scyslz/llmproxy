package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"llmproxy/config"
	"llmproxy/handler"
	"llmproxy/key"

)

func main() {
	// 加载配置
	data, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}

	cfg := &config.Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		log.Fatalf("解析配置失败: %v", err)
	}

	// 初始化 providers map
	providers := make(map[string]*config.Provider)
	for i := range cfg.Providers {
		if cfg.Providers[i].Enabled {
			providers[cfg.Providers[i].ID] = &cfg.Providers[i]
		}
	}

	// 初始化 key store
	keyStore := key.NewStore(cfg.EnableVirtualKey)
	for i := range cfg.VirtualKeys {
		keyStore.Add(&cfg.VirtualKeys[i])
	}

	// 初始化 handler
	handler.Init(cfg, providers, keyStore)

	// 路由
	http.HandleFunc("/v1/", handler.ForwardHandler) // 通用 handler：POST/PUT 判断 model/stream，GET 直接转发
	http.HandleFunc("/v1/models", handler.ModelsHandler)
	http.HandleFunc("/health", healthHandler)
	// 管理 API
	http.HandleFunc("/api/providers", handler.ProvidersAPI)
	http.HandleFunc("/api/providers/", handler.ProvidersAPI)
	// Web UI
	http.HandleFunc("/", handler.IndexHandler)
	

	log.Printf("llmproxy listening on %s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, nil); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
