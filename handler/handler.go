package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"llmproxy/config"
	"llmproxy/key"
)

var (
	cfg       *config.Config
	providers map[string]*config.Provider
	keyStore  *key.Store
)

// Init 初始化
func Init(c *config.Config, ps map[string]*config.Provider, ks *key.Store) {
	cfg = c
	providers = ps
	keyStore = ks
}

func ForwardHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("[ForwardHandler] %s %s", r.Method, r.URL.Path)

	// 验证虚拟密钥（全局启用时才进行验证）
	if cfg.EnableVirtualKey && keyStore != nil {
		apiKey := extractAPIKey(r)
		if apiKey != "" {
			providerIDs, err := keyStore.Validate(apiKey)
			if err != nil {
				log.Printf("[ForwardHandler] 虚拟密钥验证失败: %v", err)
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 401)
				return
			}
			log.Printf("[ForwardHandler] 虚拟密钥验证成功，可用 providers: %v", providerIDs)
			// TODO: 根据虚拟密钥的 providerIDs 选择 provider
		}
	}

	// GET：直接转发，不解析 body
	if r.Method == "GET" {
		forwardGet(w, r)
		return
	}
	// POST/PUT：解析 body，判断 model/stream
	if r.Method != "POST" && r.Method != "PUT" {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[ForwardHandler] 读取 body 失败: %v", err)
		http.Error(w, `{"error":"read body failed"}`, 400)
		return
	}

	// 解析请求，只提取 model/stream，保留其他字段原样
	// body 为空或不是 JSON 时跳过，直接转发原始 body
	var raw map[string]json.RawMessage
	if len(body) > 0 {
		if err := json.Unmarshal(body, &raw); err != nil {
			log.Printf("[ForwardHandler] body 不是有效 JSON，直接转发原始 body")
			raw = nil
		}
	}

	// 选第一个启用的 provider
	var providerCfg *config.Provider
	for _, pp := range providers {
		if pp.Enabled {
			providerCfg = pp
			break
		}
	}
	if providerCfg == nil {
		log.Printf("[ForwardHandler] 没有启用的 provider")
		http.Error(w, `{"error":"no provider enabled"}`, 503)
		return
	}

	// 替换 model（如果需要）
	body = resolveModel(raw, body, providerCfg)

	// 转发路径：截断 /v1，拼到 provider base_url
	path := r.URL.Path
	if idx := strings.Index(path, "/v1"); idx >= 0 {
		path = path[idx+3:]
	}
	targetURL := providerCfg.BaseURL + path

	// 从 raw 里取 model 用于日志
	reqModel := ""
	if raw != nil {
		if v, ok := raw["model"]; ok {
			json.Unmarshal(v, &reqModel)
		}
	}
	log.Printf("[ForwardHandler] %s %s -> %s (provider=%s, model=%s) body: \n %s ", r.Method, r.URL.Path, targetURL, providerCfg.Name, reqModel, string(body))

	// 重新构造请求到 targetURL
	ctx := r.Context()
	httpReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("[ForwardHandler] 创建请求失败: %v", err)
		http.Error(w, `{"error":"create request failed"}`, 500)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+providerCfg.APIKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		log.Printf("[ForwardHandler] 转发失败: %v", err)
		http.Error(w, `{"error":"provider error: `+err.Error()+`"}`, 502)
		return
	}
	defer resp.Body.Close()

	log.Printf("[ForwardHandler] 响应: %s %s -> %d", r.Method, r.URL.Path, resp.StatusCode)

	// 处理响应（从 raw 里取 stream）
	reqStream := false
	if raw != nil {
		if v, ok := raw["stream"]; ok {
			json.Unmarshal(v, &reqStream)
		}
	}

	if reqStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, `{"error":"streaming not supported"}`, 500)
			return
		}
		buf := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				flusher.Flush()
			}
			if err != nil {
				break
			}
		}
	} else {
		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, resp.Body)
	}
}

// resolveModel 如果需要，替换请求里的 model 字段
func resolveModel(raw map[string]json.RawMessage, body []byte, providerCfg *config.Provider) []byte {
	if raw == nil {
		return body
	}
	var reqModel string
	if v, ok := raw["model"]; ok {
		json.Unmarshal(v, &reqModel)
	}

	modelOK := false
	for _, m := range providerCfg.Models {
		if m == reqModel {
			modelOK = true
			break
		}
	}
	if !modelOK && len(providerCfg.Models) > 0 {
		raw["model"] = json.RawMessage(`"` + providerCfg.Models[0] + `"`)
		body, _ = json.Marshal(raw)
	}
	return body
}

// forwardGet 处理 GET 请求，直接转发，不解析 body
func forwardGet(w http.ResponseWriter, r *http.Request) {
	var providerCfg *config.Provider
	for _, pp := range providers {
		if pp.Enabled {
			providerCfg = pp
			break
		}
	}
	if providerCfg == nil {
		log.Printf("[forwardGet] 没有启用的 provider")
		http.Error(w, `{"error":"no provider enabled"}`, 503)
		return
	}

	path := r.URL.Path
	if idx := strings.Index(path, "/v1"); idx >= 0 {
		path = path[idx+3:]
	}
	targetURL := providerCfg.BaseURL + path

	log.Printf("[forwardGet] GET %s -> %s (provider=%s)", r.URL.Path, targetURL, providerCfg.Name)

	ctx := r.Context()
	httpReq, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		log.Printf("[forwardGet] 创建请求失败: %v", err)
		http.Error(w, `{"error":"create request failed"}`, 500)
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+providerCfg.APIKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		log.Printf("[forwardGet] 转发失败: %v", err)
		http.Error(w, `{"error":"provider error: `+err.Error()+`"}`, 502)
		return
	}
	defer resp.Body.Close()

	log.Printf("[forwardGet] 响应: GET %s -> %d", r.URL.Path, resp.StatusCode)

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

// ModelsHandler 处理 /v1/models
func ModelsHandler(w http.ResponseWriter, r *http.Request) {
	var models []string
	for _, p := range providers {
		if p.Enabled {
			models = append(models, p.Models...)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

// extractAPIKey 从请求中提取 API Key
func extractAPIKey(r *http.Request) string {
	// 从 Authorization header 中提取
	auth := r.Header.Get("Authorization")
	if auth != "" {
		// 格式: "Bearer <api_key>"
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
		return auth
	}

	// 可以从其他位置提取，比如查询参数等
	return ""
}

