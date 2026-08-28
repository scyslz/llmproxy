package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"llmproxy/internal/auth"
	"llmproxy/internal/logging"
)

// HandleModels 处理 /v1/models（绑定语义与 /v1/* 共享）。
func (a *App) HandleModels(w http.ResponseWriter, r *http.Request) {
	cfg := a.Cfg.Get()
	apiKey := auth.ExtractBearer(r.Header.Get("Authorization"))
	reqID := ShortID()

	if cfg.EnableVirtualKey && len(cfg.Keys) > 0 {
		matched := false
		for _, k := range cfg.Keys {
			if k.Key == apiKey {
				matched = true
				break
			}
		}
		if !matched {
			a.Logger.Log(logging.LevelWarn, "Unauthorized /v1/models request. Invalid or missing key.", "proxy", reqID)
			writeJSON(w, 401, map[string]string{"error": "Unauthorized: Invalid virtual key"})
			return
		}
	}

	cands, _, _ := a.selectCandidates(cfg, apiKey, nil)
	models := []string{}
	seen := map[string]bool{}
	for _, p := range cands {
		for _, m := range p.Models {
			if !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		}
	}
	data := make([]map[string]interface{}, len(models))
	for i, m := range models {
		data[i] = map[string]interface{}{
			"id":       m,
			"object":   "model",
			"created":  time.Now().UnixMilli(),
			"owned_by": "proxy",
		}
	}
	writeJSON(w, 200, map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

// HandleDirectChat 处理 Playground 直连测试（POST /api/providers/:id/chat/completions）。
// 此路由不经过候选链 / virtual key 校验，直接转发到指定 provider。
func (a *App) HandleDirectChat(w http.ResponseWriter, r *http.Request, providerID string) {
	cfg := a.Cfg.Get()
	reqID := ShortID()
	logDetail := orDefault(cfg.LogDetail, "basic")

	var p *Provider
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == providerID {
			pp := cfg.Providers[i]
			p = ProviderFromDomain(&pp)
			break
		}
	}
	if p == nil {
		writeJSON(w, 404, map[string]string{"error": "Provider not found"})
		return
	}

	directLog := func(level, msg string) {
		if logDetail != "off" {
			a.Logger.Log(level, msg, "proxy", reqID)
		}
	}
	start := time.Now()
	h := &handlerCtx{
		start:     start,
		origPath:  r.URL.Path,
		method:    r.Method,
		requestID: reqID,
		logDetail: logDetail,
	}
	directLog("info", "[Provider Test] "+p.Name+" chat completions initiated")

	base := strings.TrimRight(p.BaseURL, "/")
	targetURL := ""
	if p.ChatEndpoint != "" {
		ep := p.ChatEndpoint
		if !strings.HasPrefix(ep, "/") {
			ep = "/" + ep
		}
		targetURL = base + ep
	} else {
		hasSuffix := false
		for _, s := range []string{"/v1", "/openai", "/v1beta", "/api", "/v4", "/v2", "/v3"} {
			if strings.HasSuffix(base, s) {
				hasSuffix = true
				break
			}
		}
		if hasSuffix {
			targetURL = base + "/chat/completions"
		} else {
			targetURL = base + "/v1/chat/completions"
		}
	}

	rawBody, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
		return
	}

	model := ""
	var reqMeta struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rawBody, &reqMeta); err == nil {
		model = reqMeta.Model
	}

	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		hdr.Set("Authorization", "Bearer "+p.APIKey)
	}
	if logDetail == "all" {
		a.Logger.Log(logging.LevelInfo, "[Provider Test Request Headers] "+formatHeaders(hdr), "proxy", reqID)
	}

	// 若 provider 声明为 responses 协议，Playground 仍以 chat/completions 形态发送，
	// 此处将请求转换为 responses 并指向 /v1/responses，响应再转回 chat。
	// 未确定协议时，非200 均翻转一次（与 HandleProxy 保持一致）。
	needsConvert := false
	sendURL := targetURL
	sendBody := rawBody
	targetProto, known := p.upstreamProtocol(model)
	probe := !known
	if known && targetProto == protoResponses {
		needsConvert = true
		var m map[string]interface{}
		if json.Unmarshal(rawBody, &m) == nil {
			if conv, cerr := convertRequestBody(protoChat, protoResponses, m); cerr == nil {
				if b, e := json.Marshal(conv); e == nil {
					sendBody = b
				}
			}
		}
		sendURL = buildTargetURL(p, protoResponses)
	}

	resp, err := a.Client.Do(r.Context(), "POST", sendURL, hdr, bytes.NewReader(sendBody), p.Timeout)
	if err != nil {
		directLog("error", "[Provider Test] "+p.Name+" failed: "+err.Error())
		a.logRequest(h, p.Name, model, 502, 0, 0, 0, 0, false, "connection error: "+err.Error())
		writeJSON(w, 502, map[string]string{"error": "Provider test failed: " + err.Error()})
		return
	}

	if needsConvert {
		a.transpileResponse(w, r, h, p, resp, protoChat, protoResponses, model)
		return
	}

	// 未确定协议时，若首次 chat 尝试非200，则翻转到 responses 重试一次
	if probe && resp.StatusCode != 200 {
		// 消费并关闭首次响应
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		var m2 map[string]interface{}
		if json.Unmarshal(rawBody, &m2) == nil {
			if conv2, cerr2 := convertRequestBody(protoChat, protoResponses, m2); cerr2 == nil {
				if b2, e2 := json.Marshal(conv2); e2 == nil {
					flipURL := buildTargetURL(p, protoResponses)
					resp2, err2 := a.Client.Do(r.Context(), "POST", flipURL, hdr, bytes.NewReader(b2), p.Timeout)
					if err2 == nil {
						if resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
							a.persistDiscoveredProtocol(p.ID, model, protoResponses)
							directLog("info", "[Provider Test] protocol probe succeeded by flipping to 'responses' ("+p.Name+"/"+model+") [=> /responses]")
							a.transpileResponse(w, r, h, p, resp2, protoChat, protoResponses, model)
							return
						}
						// 翻转后仍失败，使用翻转后的响应继续透传
						resp = resp2
					} else {
						// 翻转请求本身失败，透传原失败
						writeJSON(w, 502, map[string]string{"error": "Provider test failed: HTTP " + itoa(resp.StatusCode)})
						return
					}
				}
			}
		} else {
			writeJSON(w, 502, map[string]string{"error": "Provider test failed: HTTP " + itoa(resp.StatusCode)})
			return
		}
	}
	defer resp.Body.Close()

	if h.detailActiveFor(resp.StatusCode) {
		if logDetail == "error" {
			a.Logger.Log(logging.LevelInfo, "[Provider Test Request Headers] "+formatHeaders(hdr), "proxy", reqID)
		}
		a.Logger.Log(logging.LevelInfo, "[Provider Test Response Headers] "+formatHeaders(resp.Header), "proxy", reqID)
	}

	for k, vv := range resp.Header {
		lk := strings.ToLower(k)
		if lk == "transfer-encoding" || lk == "content-encoding" || lk == "content-length" {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	stream := isStream(resp)
	var parser UsageParser
	var body bytes.Buffer
	statusOut := resp.StatusCode
	errMsg := ""
	resModel := model
	var prompt, completion, cached int

	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			body.Write(buf[:n])
			if stream {
				parser.Push(string(buf[:n]))
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				errMsg = "client closed connection"
				statusOut = 499
				break
			}
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if r.Context().Err() == context.Canceled {
				errMsg = "client closed connection"
				statusOut = 499
			} else {
				errMsg = "stream error: " + rerr.Error()
				statusOut = 502
			}
			break
		}
	}

	if errMsg == "" {
		if stream {
			if parser.Model != "" {
				resModel = parser.Model
			}
			if parser.Usage != nil {
				prompt = parser.Usage.PromptTokens
				completion = parser.Usage.CompletionTokens
				cached = parser.Usage.CachedTokens
			}
		} else {
			if usage, mm := parseUsageJSON(body.Bytes()); usage != nil {
				resModel = mm
				prompt = usage.PromptTokens
				completion = usage.CompletionTokens
				cached = usage.CachedTokens
			}
		}
	}

	directLog("info", "[Provider Test] "+p.Name+" completed with status "+itoa(statusOut)+" ("+spanMs(start)+")")
	a.logRequest(h, p.Name, resModel, statusOut, prompt, completion, cached, prompt+completion, stream, errMsg)
}

// FetchRemoteModels 拉取 provider 的远程模型列表。
func (a *App) FetchRemoteModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
		return
	}
	if body.BaseURL == "" {
		writeJSON(w, 400, map[string]string{"error": "Base URL is required to fetch models"})
		return
	}

	apiKey := body.APIKey
	if apiKey == "" && body.ID != "" {
		cfg := a.Cfg.Get()
		for _, p := range cfg.Providers {
			if p.ID == body.ID && p.APIKey != "" {
				apiKey = p.APIKey
				break
			}
		}
	}
	if apiKey == "" && (body.ID == "gemini" || strings.Contains(body.BaseURL, "googleapis.com")) {
		cfg := a.Cfg.Get()
		for _, p := range cfg.Providers {
			if p.ID == "gemini" && p.APIKey != "" {
				apiKey = p.APIKey
				break
			}
		}
	}

	cleanURL := strings.TrimRight(body.BaseURL, "/")
	targetURL := ""
	if body.ID == "gemini" || strings.Contains(cleanURL, "googleapis.com") {
		targetURL = "https://generativelanguage.googleapis.com/v1beta/models?key=" + apiKey
	} else {
		for _, s := range []string{"/v1", "/api", "/models", "/openai"} {
			if strings.HasSuffix(cleanURL, s) {
				targetURL = cleanURL + "/models"
				goto fetch
			}
		}
		targetURL = cleanURL + "/v1/models"
	}

fetch:
	reqID := ShortID()
	a.Logger.Log(logging.LevelInfo, "Fetching models from remote upstream: "+targetURL, "proxy", reqID)

	hdr := http.Header{}
	hdr.Set("Accept", "application/json")
	if apiKey != "" && !strings.Contains(targetURL, "key=") {
		hdr.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := a.Client.Do(r.Context(), "GET", targetURL, hdr, nil, 30*time.Second)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	// 如果第一次失败，尝试 fallback
	if resp.StatusCode != 200 {
		fallbackURL := ""
		if strings.HasSuffix(targetURL, "/v1/models") {
			fallbackURL = cleanURL + "/models"
		} else if !strings.HasSuffix(targetURL, "/models") && !strings.Contains(targetURL, "googleapis.com") {
			fallbackURL = cleanURL + "/v1/models"
		}
		if fallbackURL != "" {
			a.Logger.Log(logging.LevelInfo, "Fallback fetching models from: "+fallbackURL, "proxy", reqID)
			resp.Body.Close()
			resp, err = a.Client.Do(r.Context(), "GET", fallbackURL, hdr, nil, 30*time.Second)
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			defer resp.Body.Close()
		}
	}

	if resp.StatusCode != 200 {
		errText, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		writeJSON(w, 500, map[string]string{"error": "Upstream API error: " + itoa(resp.StatusCode) + " - " + string(errText)})
		return
	}

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to parse upstream response"})
		return
	}
	models := extractModels(raw)
	a.Logger.Log(logging.LevelInfo, "Successfully fetched "+itoa(len(models))+" models from "+targetURL, "proxy", reqID)
	writeJSON(w, 200, map[string]interface{}{
		"models": models,
		"count":  len(models),
		"url":    targetURL,
	})
}

func extractModels(raw json.RawMessage) []string {
	var data struct {
		Data   []interface{} `json:"data"`
		Models []interface{} `json:"models"`
	}
	if err := json.Unmarshal(raw, &data); err == nil {
		if len(data.Data) > 0 {
			return extractIDs(data.Data)
		}
		if len(data.Models) > 0 {
			return extractNames(data.Models)
		}
	}
	var arr []interface{}
	if err := json.Unmarshal(raw, &arr); err == nil {
		return extractIDs(arr)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, v := range obj {
			var sub []interface{}
			if err := json.Unmarshal(v, &sub); err == nil {
				if ids := extractIDs(sub); len(ids) > 0 {
					return ids
				}
			}
		}
	}
	return nil
}

func extractIDs(items []interface{}) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		id := ""
		switch v := item.(type) {
		case string:
			id = v
		case map[string]interface{}:
			if s, ok := v["id"].(string); ok {
				id = s
			} else if s, ok := v["name"].(string); ok {
				id = s
			}
		}
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func extractNames(items []interface{}) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		name := ""
		switch v := item.(type) {
		case string:
			name = v
		case map[string]interface{}:
			if s, ok := v["name"].(string); ok {
				name = strings.TrimPrefix(s, "models/")
			} else if s, ok := v["id"].(string); ok {
				name = s
			}
		}
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}