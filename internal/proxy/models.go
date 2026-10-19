package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"llmproxy/internal/auth"
	"llmproxy/internal/domain"
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
	maxOuts := map[string]int{}
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
			if n := p.ModelMaxOutputs[m]; n > 0 && maxOuts[m] == 0 {
				maxOuts[m] = n
			}
		}
	}
	now := time.Now().UnixMilli()
	data := make([]map[string]interface{}, 0, len(models)+1)
	// auto 是代理的模型别名：请求方未指定具体模型时，由每个 provider 的
	// defaultModel（缺省为 models[0]）兜底。这里把它暴露给客户端，并按
	// 首选候选的兜底模型补齐上下文/输出限制。
	if !seen["auto"] {
		autoCtx, autoOut := autoModelLimits(cands)
		data = append(data, modelItem("auto", autoCtx, autoOut, now))
	}
	for _, m := range models {
		data = append(data, modelItem(m, ctxLens[m], maxOuts[m], now))
	}
	writeJSON(w, 200, map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

// autoModelLimits 返回 auto 别名对应模型的上下文/输出限制。
// 请求中的 auto 会先落到首个候选 provider 的 defaultModel（或 models[0]）；
// 若该 provider 未配置限制，继续向后寻找，避免客户端拿到空限制。
func autoModelLimits(cands []*Provider) (int, int) {
	for _, p := range cands {
		target := p.DefaultModel
		if target == "" && len(p.Models) > 0 {
			target = p.Models[0]
		}
		if target == "" {
			continue
		}
		ctxLen := p.ModelContextLengths[target]
		outLen := p.ModelMaxOutputs[target]
		if ctxLen > 0 || outLen > 0 {
			return ctxLen, outLen
		}
	}
	return 0, 0
}

func modelItem(id string, ctxLen, outLen int, created int64) map[string]interface{} {
	item := map[string]interface{}{
		"id":       id,
		"object":   "model",
		"created":  created,
		"owned_by": "proxy",
	}
	// opencode V2 要求 limit 必须同时包含 context 和 output，
	// 缺任一项时按比例补齐，保证两个字段要么同时出现，要么同时省略。
	if ctxLen > 0 && outLen <= 0 {
		outLen = defaultMaxOutput(ctxLen)
	}
	if outLen > 0 && ctxLen <= 0 {
		ctxLen = outLen * 16
	}
	if ctxLen > 0 {
		item["limit"] = map[string]int{"context": ctxLen, "output": outLen}
		item["context_length"] = ctxLen
		item["max_output_tokens"] = outLen
	}
	return item
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
		userAgent: r.Header.Get("User-Agent"),
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
	if h.userAgent != "" {
		hdr.Set("User-Agent", h.userAgent)
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
		userAgent: r.Header.Get("User-Agent"),
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
	if h.userAgent != "" {
		hdr.Set("User-Agent", h.userAgent)
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

// modelDBURL 是在线模型库地址，用于按模型名检索最大上下文长度。
// 声明为变量以便测试注入 mock 服务。
var modelDBURL = "https://models.dev/api.json"

const (
	modelDBMaxBody  = 64 << 20
	modelDBCacheTTL = 6 * time.Hour
)

var (
	modelDBCacheMu sync.RWMutex
	modelDBCache   modelLimitIndex
	modelDBCacheAt time.Time
)

// modelDateSuffix 匹配模型名末尾的日期版本后缀（如 -20241022 / -2024-10-22）。
var modelDateSuffix = regexp.MustCompile(`[-@](\d{4}-?\d{2}-?\d{2})$`)

type modelLimit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

type modelDBEntry struct {
	Name  string     `json:"name"`
	Limit modelLimit `json:"limit"`
}

type modelDBProvider struct {
	Models map[string]modelDBEntry `json:"models"`
}

// llmLookup 描述用于询问大模型的临时上游配置。
type llmLookup struct {
	ProviderID string `json:"providerId"`
	BaseURL    string `json:"baseUrl"`
	APIKey     string `json:"apiKey"`
	Model      string `json:"model"`
}

// defaultContextLength 是检索失败或无结果时使用的默认上下文长度（200K tokens）。
const defaultContextLength = 200000

// defaultMaxOutput 单次最大输出无数据时取上下文的 1/16，限制在 [4K, 64K]。
func defaultMaxOutput(ctx int) int {
	out := ctx / 16
	if out > 65536 {
		out = 65536
	}
	if out < 4096 {
		out = 4096
	}
	return out
}

// LookupModelContexts 根据模型名在线检索最大上下文长度。
// 先查公共模型库；useLLM 为 true 时，对未命中的模型再询问大模型补全；
// 仍无结果的模型回退到默认值 defaultContextLength。
// 仅返回命中项；命中数量可能少于请求数量。
func (a *App) LookupModelContexts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Models   []string   `json:"models"`
		UseLLM   bool       `json:"useLLM"`
		UseDef   *bool      `json:"useDefault"`
		Fallback int        `json:"fallback"`
		LLM      *llmLookup `json:"llm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
		return
	}
	models := dedupeNonEmpty(body.Models)
	if len(models) == 0 {
		writeJSON(w, 400, map[string]string{"error": "models is required"})
		return
	}
	fallback := defaultContextLength
	if body.Fallback > 0 {
		fallback = body.Fallback
	}
	useDefault := true
	if body.UseDef != nil {
		useDefault = *body.UseDef
	}

	out := map[string]int{}
	outputs := map[string]int{}
	dbErr := ""
	if idx, err := a.modelContextIndex(r.Context()); err != nil {
		dbErr = err.Error()
	} else {
		for _, m := range models {
			lim := lookupModelLimit(idx, m)
			if lim.Context > 0 {
				out[m] = lim.Context
			}
			if lim.Output > 0 {
				outputs[m] = lim.Output
			}
		}
	}

	llmErr := ""
	if body.UseLLM {
		missing := []string{}
		for _, m := range models {
			if out[m] == 0 {
				missing = append(missing, m)
			}
		}
		if len(missing) > 0 {
			lp := a.resolveLLMProvider(body.LLM)
			if lp == nil {
				llmErr = "no provider available for LLM lookup"
			} else if res, err := a.callLLMContextLengths(r.Context(), lp, missing); err != nil {
				llmErr = err.Error()
			} else {
				for m, lim := range res {
					if out[m] == 0 && lim.Context > 0 {
						out[m] = lim.Context
					}
					if outputs[m] == 0 && lim.Output > 0 {
						outputs[m] = lim.Output
					}
				}
			}
		}
	}

	fallbacks := []string{}
	if useDefault {
		for _, m := range models {
			if out[m] == 0 {
				out[m] = fallback
				fallbacks = append(fallbacks, m)
			}
			if outputs[m] == 0 {
				outputs[m] = defaultMaxOutput(out[m])
			}
		}
	}

	if len(out) == 0 && dbErr != "" && !body.UseLLM && !useDefault {
		writeJSON(w, 502, map[string]string{"error": "Failed to fetch model database: " + dbErr})
		return
	}

	resp := map[string]interface{}{
		"contextLengths": out,
		"maxOutputs":     outputs,
		"matched":        len(out) - len(fallbacks),
		"requested":      len(models),
		"fallbacks":      fallbacks,
	}
	if dbErr != "" {
		resp["dbError"] = dbErr
	}
	if llmErr != "" {
		resp["llmError"] = llmErr
	}
	writeJSON(w, 200, resp)
}

// resolveLLMProvider 解析用于询问大模型的上游：优先使用请求内联配置，
// 其次按 providerId 查配置，最后回退到第一个可用的 enabled provider。
func (a *App) resolveLLMProvider(req *llmLookup) *llmLookup {
	cfg := a.Cfg.Get()
	pick := func(p *domain.Provider, model string) *llmLookup {
		if p.BaseURL == "" || p.APIKey == "" {
			return nil
		}
		m := p.DefaultModel
		if model != "" {
			m = model
		} else if m == "" && len(p.Models) > 0 {
			m = p.Models[0]
		}
		if m == "" {
			return nil
		}
		return &llmLookup{ProviderID: p.ID, BaseURL: p.BaseURL, APIKey: p.APIKey, Model: m}
	}
	if req != nil {
		if req.ProviderID != "" {
			for i := range cfg.Providers {
				if cfg.Providers[i].ID == req.ProviderID {
					if lp := pick(&cfg.Providers[i], req.Model); lp != nil {
						return lp
					}
				}
			}
		}
		if req.BaseURL != "" && req.APIKey != "" && req.Model != "" {
			return req
		}
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].Enabled {
			if lp := pick(&cfg.Providers[i], ""); lp != nil {
				return lp
			}
		}
	}
	return nil
}

// callLLMContextLengths 询问大模型给定模型的上下文窗口与最大输出。
func (a *App) callLLMContextLengths(ctx context.Context, lp *llmLookup, models []string) (map[string]modelLimit, error) {
	var sb strings.Builder
	sb.WriteString("You are a precise large-language-model metadata database.\n")
	sb.WriteString("For each model identifier below, return its maximum context window (input token limit) and its maximum output tokens per response as integers.\n")
	sb.WriteString("Respond with ONLY a minified JSON object mapping the exact model identifier to an object like {\"context\":128000,\"output\":16384}. ")
	sb.WriteString("Omit any model you are not confident about. Do not add explanations or code fences.\n\nModels:\n")
	for _, m := range models {
		sb.WriteString("- " + m + "\n")
	}

	payload := map[string]interface{}{
		"model":       lp.Model,
		"temperature": 0,
		"messages":    []map[string]string{{"role": "user", "content": sb.String()}},
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	hdr.Set("Authorization", "Bearer "+lp.APIKey)
	targetURL := ResolveTargetURL(lp.BaseURL, "", "/v1/chat/completions")

	resp, err := a.Client.Do(ctx, "POST", targetURL, hdr, bytes.NewReader(bodyBytes), 60*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(errText)))
	}

	var rr struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rr); err != nil {
		return nil, err
	}
	if len(rr.Choices) == 0 {
		return nil, fmt.Errorf("empty response from model")
	}
	return parseContextLengthsJSON(rr.Choices[0].Message.Content), nil
}

// parseContextLengthsJSON 从模型输出中提取 {model: {context, output}} 映射，
// 兼容值为纯数字（仅上下文）的旧格式。
func parseContextLengthsJSON(content string) map[string]modelLimit {
	out := map[string]modelLimit{}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return out
	}
	raw := map[string]interface{}{}
	if err := json.Unmarshal([]byte(content[start:end+1]), &raw); err != nil {
		return out
	}
	for k, v := range raw {
		key := strings.TrimSpace(k)
		switch val := v.(type) {
		case map[string]interface{}:
			var lim modelLimit
			if n, ok := anyToInt(val["context"]); ok {
				lim.Context = n
			}
			if n, ok := anyToInt(val["output"]); ok {
				lim.Output = n
			}
			if lim.Context > 0 || lim.Output > 0 {
				out[key] = lim
			}
		default:
			if n, ok := anyToInt(val); ok && n > 0 {
				out[key] = modelLimit{Context: n}
			}
		}
	}
	return out
}

func anyToInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case string:
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, n)
		if digits == "" {
			return 0, false
		}
		i, err := strconv.Atoi(digits)
		if err != nil {
			return 0, false
		}
		return i, true
	}
	return 0, false
}

// modelLimitIndex 按规范化模型名索引 limit（context + output）。
type modelLimitIndex map[string]modelLimit

// modelContextIndex 拉取并缓存在线模型库，构建 "规范化模型名 -> limit" 索引。
func (a *App) modelContextIndex(ctx context.Context) (modelLimitIndex, error) {
	modelDBCacheMu.RLock()
	cached := modelDBCache
	fresh := cached != nil && time.Since(modelDBCacheAt) < modelDBCacheTTL
	modelDBCacheMu.RUnlock()
	if fresh {
		return cached, nil
	}

	hdr := http.Header{}
	hdr.Set("Accept", "application/json")
	resp, err := a.Client.Do(ctx, "GET", modelDBURL, hdr, nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var db map[string]modelDBProvider
	if err := json.NewDecoder(io.LimitReader(resp.Body, modelDBMaxBody)).Decode(&db); err != nil {
		return nil, err
	}
	idx := buildModelLimitIndex(db)
	if len(idx) == 0 {
		return nil, fmt.Errorf("empty model database")
	}

	modelDBCacheMu.Lock()
	modelDBCache = idx
	modelDBCacheAt = time.Now()
	modelDBCacheMu.Unlock()
	return idx, nil
}

// resetModelDBCache 仅用于测试清理全局缓存。
func resetModelDBCache() {
	modelDBCacheMu.Lock()
	modelDBCache = nil
	modelDBCacheAt = time.Time{}
	modelDBCacheMu.Unlock()
}

func buildModelLimitIndex(db map[string]modelDBProvider) modelLimitIndex {
	idx := modelLimitIndex{}
	set := func(key string, lim modelLimit) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		old := idx[key]
		if lim.Context > old.Context {
			old.Context = lim.Context
		}
		if lim.Output > old.Output {
			old.Output = lim.Output
		}
		idx[key] = old
	}
	for _, prov := range db {
		for id, entry := range prov.Models {
			lim := entry.Limit
			if lim.Context <= 0 && lim.Output <= 0 {
				continue
			}
			lower := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "models/"))
			set(lower, lim)
			base := modelBasename(lower)
			set(base, lim)
			set(stripModelVariant(base), lim)
		}
	}
	return idx
}

// lookupModelLimit 逐级放宽匹配：完整名 -> 去掉 provider 前缀 -> 去掉版本/日期后缀。
func lookupModelLimit(idx modelLimitIndex, model string) modelLimit {
	lower := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
	if lower == "" {
		return modelLimit{}
	}
	if lim := idx[lower]; lim.Context > 0 || lim.Output > 0 {
		return lim
	}
	base := modelBasename(lower)
	if lim := idx[base]; lim.Context > 0 || lim.Output > 0 {
		return lim
	}
	if lim := idx[stripModelVariant(base)]; lim.Context > 0 || lim.Output > 0 {
		return lim
	}
	return modelLimit{}
}

// lookupModelContext 保留兼容：仅取上下文长度。
func lookupModelContext(idx modelLimitIndex, model string) int {
	return lookupModelLimit(idx, model).Context
}

func modelBasename(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func stripModelVariant(s string) string {
	s = strings.TrimSuffix(s, "-latest")
	s = strings.TrimSuffix(s, ":latest")
	s = modelDateSuffix.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func dedupeNonEmpty(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
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
