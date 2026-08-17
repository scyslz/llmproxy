package main

import (
	"log"
	"os"
	"path/filepath"

	"llmproxy/internal/auth"
	"llmproxy/internal/circuit"
	"llmproxy/internal/config"
	"llmproxy/internal/logging"
	"llmproxy/internal/logstore"
	"llmproxy/internal/proxy"
	"llmproxy/internal/server"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get working directory: %v", err)
	}

	configDir := filepath.Join(cwd, "config")
	logsDir := filepath.Join(cwd, "logs")

	// 配置
	cfg, err := config.New(configDir)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// SQLite 日志存储
	sysStore, err := logstore.OpenSystem(filepath.Join(logsDir, "system_logs.db"))
	if err != nil {
		log.Fatalf("Failed to open system log database: %v", err)
	}
	defer sysStore.Close()

	reqStore, err := logstore.OpenRequest(filepath.Join(logsDir, "requests.db"), cfg.Get().MaxRequestLogs)
	if err != nil {
		log.Fatalf("Failed to open request log database: %v", err)
	}
	defer reqStore.Close()

	// 基础设施
	breaker := circuit.NewBreaker()
	client := proxy.NewClient()
	adm := auth.NewAdmin()
	logger := logging.New(cfg, sysStore)

	// 代理引擎
	proxyApp := proxy.NewApp(cfg, breaker, client, logger, reqStore)

	// 启动
	server.Run(cfg, sysStore, reqStore, logger, adm, proxyApp)
}