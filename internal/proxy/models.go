package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
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
	ctxLens := map[string]int{}
	seen := map[string]bool{}
	for _, p := range cands {
		for _, m := range p.Models {
			if !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
			if n := p.ModelContextLengths[m]; n > 0 && ctxLens[m] == 0 {
				ctxLens[m] = n
			}
		}
	}
	data := make([]map[string]interface{}, len(models))
	for i, m := range models {
		item := map[string]interface{}{
			"id":       m,
			"object":   "model",
			"created":  time.Now().UnixMilli(),
			"owned_by": "proxy",
		}
		if n := ctxLens[m]; n > 0 {
			item["context_length"] = n
		}
		data[i] = item
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

	resp, err := a.Client.Do(r.Context(), "POST", targetURL, hdr, bytes.NewReader(rawBody), p.Timeout)
	if err != nil {
		directLog("error", "[Provider Test] "+p.Name+" failed: "+err.Error())
		a.logRequest(h, p.Name, model, 502, 0, 0, 0, 0, false, "connection error: "+err.Error())
		writeJSON(w, 502, map[string]string{"error": "Provider test failed: " + err.Error()})
		return
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

// HandleDirectResponses 处理 Playground 直连测试的 responses 格式
// （POST /api/providers/:id/responses）。不经候选链 / virtual key 校验，
// 将请求体按 responses 协议原样转发到 provider 的 responses 端点并回传结果；
// provider 不支持 /responses 时也直接请求，不做 chat 兜底转换。
func (a *App) HandleDirectResponses(w http.ResponseWriter, r *http.Request, providerID string) {
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
	directLog("info", "[Provider Test] "+p.Name+" responses initiated")

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

	sendURL := buildTargetURL(p, protoResponses)

	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		hdr.Set("Authorization", "Bearer "+p.APIKey)
	}
	if logDetail == "all" {
		a.Logger.Log(logging.LevelInfo, "[Provider Test Request Headers] "+formatHeaders(hdr), "proxy", reqID)
	}

	resp, err := a.Client.Do(r.Context(), "POST", sendURL, hdr, bytes.NewReader(rawBody), p.Timeout)
	if err != nil {
		directLog("error", "[Provider Test] "+p.Name+" failed: "+err.Error())
		a.logRequest(h, p.Name, model, 502, 0, 0, 0, 0, false, "connection error: "+err.Error())
		writeJSON(w, 502, map[string]string{"error": "Provider test failed: " + err.Error()})
		return
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
		} else if body.Len() > 0 {
			if usage, mm := parseUsageJSON(body.Bytes()); usage != nil {
				if mm != "" {
					resModel = mm
				}
				prompt = usage.PromptTokens
				completion = usage.CompletionTokens
				cached = usage.CachedTokens
			} else {
				var fallback struct {
					Model string `json:"model"`
				}
				if json.Unmarshal(body.Bytes(), &fallback) == nil && fallback.Model != "" {
					resModel = fallback.Model
				}
			}
		}
	}

	directLog("info", "[Provider Test] "+p.Name+" responses completed with status "+itoa(statusOut)+" ("+spanMs(start)+")")
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

	infos, err := extractModelsFromReader(resp.Body)
	if err != nil && len(infos) == 0 {
		writeJSON(w, 500, map[string]string{"error": "Failed to parse upstream response: " + err.Error()})
		return
	}
	models := make([]string, 0, len(infos))
	contextLengths := map[string]int{}
	for _, info := range infos {
		models = append(models, info.ID)
		if info.ContextLength > 0 {
			contextLengths[info.ID] = info.ContextLength
		}
	}
	a.Logger.Log(logging.LevelInfo, "Successfully fetched "+itoa(len(models))+" models from "+targetURL, "proxy", reqID)
	out := map[string]interface{}{
		"models": models,
		"count":  len(models),
		"url":    targetURL,
	}
	if len(contextLengths) > 0 {
		out["contextLengths"] = contextLengths
	}
	writeJSON(w, 200, out)
}

type remoteModelInfo struct {
	ID            string
	ContextLength int
}

const maxModelsBody = 32 << 20

func extractModelsFromReader(r io.Reader) ([]remoteModelInfo, error) {
	dec := json.NewDecoder(io.LimitReader(r, maxModelsBody))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			return decodeModelArray(dec, false)
		}
		if t == '{' {
			return decodeModelObject(dec)
		}
	}
	return nil, nil
}

func decodeModelObject(dec *json.Decoder) ([]remoteModelInfo, error) {
	var dataIDs, modelsIDs, otherIDs []remoteModelInfo
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		d, isDelim := tok.(json.Delim)
		if isDelim && d == '[' {
			preferName := key == "models"
			ids, err := decodeModelArray(dec, preferName)
			if err != nil {
				return nil, err
			}
			switch key {
			case "data":
				dataIDs = ids
			case "models":
				modelsIDs = ids
			default:
				if len(otherIDs) == 0 && len(ids) > 0 {
					otherIDs = ids
				}
			}
			continue
		}
		if isDelim {
			if err := skipDelim(dec, d); err != nil {
				return nil, err
			}
		}
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return nil, err
	}
	if len(dataIDs) > 0 {
		return dataIDs, nil
	}
	if len(modelsIDs) > 0 {
		return modelsIDs, nil
	}
	return otherIDs, nil
}

func decodeModelArray(dec *json.Decoder, preferName bool) ([]remoteModelInfo, error) {
	out := []remoteModelInfo{}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return out, err
		}
		switch v := tok.(type) {
		case string:
			addModelInfo(&out, seen, remoteModelInfo{ID: v})
		case json.Delim:
			if v == '{' {
				id, name, ctxLen, err := decodeIDNameObject(dec)
				if err != nil {
					return out, err
				}
				chosen := ""
				if preferName {
					if name != "" {
						chosen = strings.TrimPrefix(name, "models/")
					} else {
						chosen = id
					}
				} else if id != "" {
					chosen = id
				} else {
					chosen = name
				}
				addModelInfo(&out, seen, remoteModelInfo{ID: chosen, ContextLength: ctxLen})
			} else if err := skipDelim(dec, v); err != nil {
				return out, err
			}
		}
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return out, err
	}
	return out, nil
}

func decodeIDNameObject(dec *json.Decoder) (id, name string, ctxLen int, err error) {
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return "", "", 0, err
		}
		key, _ := keyTok.(string)
		tok, err := dec.Token()
		if err != nil {
			return "", "", 0, err
		}
		if d, ok := tok.(json.Delim); ok {
			if err := skipDelim(dec, d); err != nil {
				return "", "", 0, err
			}
			continue
		}
		switch key {
		case "id":
			if s, ok := tok.(string); ok {
				id = s
			}
		case "name":
			if s, ok := tok.(string); ok {
				name = s
			}
		case "context_length", "contextLength", "max_context_length", "max_input_tokens", "inputTokenLimit":
			if n, ok := jsonNumber(tok); ok && n > ctxLen {
				ctxLen = n
			}
		}
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return id, name, ctxLen, err
	}
	return id, name, ctxLen, nil
}

func skipDelim(dec *json.Decoder, open json.Delim) error {
	for dec.More() {
		if open == '{' {
			if _, err := dec.Token(); err != nil {
				return err
			}
		}
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if err := skipDelim(dec, d); err != nil {
				return err
			}
		}
	}
	_, err := dec.Token()
	return err
}

func addModelInfo(out *[]remoteModelInfo, seen map[string]bool, info remoteModelInfo) {
	if info.ID == "" || seen[info.ID] {
		return
	}
	seen[info.ID] = true
	*out = append(*out, info)
}

func jsonNumber(tok json.Token) (int, bool) {
	switch n := tok.(type) {
	case float64:
		if n > 0 {
			return int(n), true
		}
	case json.Number:
		v, err := n.Int64()
		if err == nil && v > 0 {
			return int(v), true
		}
	case string:
		v, err := strconv.Atoi(n)
		if err == nil && v > 0 {
			return v, true
		}
	}
	return 0, false
}