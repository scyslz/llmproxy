package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"

	"llmproxy/config"
	"llmproxy/handler"
	"llmproxy/key"
)

//go:embed web/*
var embeddedWeb embed.FS

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

	// 注入嵌入的 web 目录（交叉编译后无需 web/ 目录）
	webSub, _ := fs.Sub(embeddedWeb, "web")
	handler.SetWebFS(webSub)

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
