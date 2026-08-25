package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"llmproxy/internal/auth"
	"llmproxy/internal/circuit"
	"llmproxy/internal/config"
	"llmproxy/internal/domain"
	"llmproxy/internal/lazyhealth"
	"llmproxy/internal/logging"
	"llmproxy/internal/logstore"
)

// App 承载代理转发所需的状态（配置、熔断器、上游客户端、日志、健康状态）。
type App struct {
	Cfg     *config.Manager
	Breaker *circuit.Breaker
	Client  *Client
	Logger  *logging.Logger
	ReqLog  *logstore.RequestStore
	Health  *lazyhealth.Tracker
}

// NewApp 构造代理引擎。
func NewApp(cfg *config.Manager, br *circuit.Breaker, cl *Client,
	log *logging.Logger, req *logstore.RequestStore, health *lazyhealth.Tracker) *App {
	return &App{Cfg: cfg, Breaker: br, Client: cl, Logger: log, ReqLog: req, Health: health}
}

// handlerCtx 携带单次转发所需上下文。
type handlerCtx struct {
	start        time.Time
	origPath     string
	method       string
	requestID    string
	cfg          *domain.Config
	logDetail    string
	logBody      bool
	apiKey       string
	keyName      string
	reqModel     string
		degradedFrom []string
		timedOut     bool
		lastStatus   int
		lastProvider string
		failureLogged bool
	}

func (h *handlerCtx) proxyLog(a *App, level, msg string) {
	if h.logDetail != "off" {
		a.Logger.Log(level, msg, "proxy", h.requestID)
	}
}

// enabledCandidates 全局模式下取 enabled provider 作为候选链。
func enabledCandidates(ps []domain.Provider) []*Provider {
	var out []*Provider
	for _, p := range ps {
		pp := p
		if p.Enabled {
			out = append(out, ProviderFromDomain(&pp))
		}
	}
	return out
}

func ProviderFromDomain(p *domain.Provider) *Provider {
	return &Provider{
		ID:        p.ID,
		Name:      p.Name,
		BaseURL:   p.BaseURL,
		APIKey:    p.APIKey,
		Models:    p.Models,
		OpenAIEndpoint: p.OpenAIEndpoint,
		DefaultModel:   p.DefaultModel,
		Timeout:   time.Duration(p.Timeout) * time.Millisecond,
		Concurrency: p.Concurrency,
	}
}

// selectCandidates 解析 virtual key 并返回候选 provider 链与绑定标记。
// 返回 (候选, 已绑定, 来自Group)。key 的 groupId 优先：展开为组内有序 provider 链；
// 无 groupId 时回退 providerIds；再回退全局 enabled 集合。
func (a *App) selectCandidates(cfg *domain.Config, apiKey string, keyName *string) ([]*Provider, bool, bool) {
	if apiKey != "" {
		for i := range cfg.Keys {
			if cfg.Keys[i].Key == apiKey {
				if keyName != nil {
					*keyName = cfg.Keys[i].Name
				}
				vk := &cfg.Keys[i]
				if vk.GroupID != "" {
				if group := findGroup(cfg, vk.GroupID); group != nil {
					cands := []*Provider{}
					for _, e := range group.Entries {
						for j := range cfg.Providers {
							if cfg.Providers[j].ID == e.ProviderID {
								pp := cfg.Providers[j]
								p := ProviderFromDomain(&pp)
								if len(e.Models) > 0 {
									p.DefaultModel = e.Models[0]
								}
								cands = append(cands, p)
								break
							}
						}
					}
					return cands, true, true
				}
				// groupId references nonexistent group -> fall through to providerIds
			}
				if len(vk.ProviderIDs) > 0 {
					isAll := false
					for _, id := range vk.ProviderIDs {
						if id == "all" || id == "*" {
							isAll = true
							break
						}
					}
					if !isAll {
						cands := []*Provider{}
						for _, id := range vk.ProviderIDs {
							for j := range cfg.Providers {
								if cfg.Providers[j].ID == id {
									pp := cfg.Providers[j]
									cands = append(cands, ProviderFromDomain(&pp))
									break
								}
							}
						}
						return cands, true, false
					}
				}
				return enabledCandidates(cfg.Providers), false, false
			}
		}
	}
	return enabledCandidates(cfg.Providers), false, false
}

func findGroup(cfg *domain.Config, id string) *domain.ProviderGroup {
	for i := range cfg.Groups {
		if cfg.Groups[i].ID == id {
			return &cfg.Groups[i]
		}
	}
	return nil
}

// sem 返回 provider 维度的全局信号量（registry 跨配置变更保留）。
func (a *App) Sem(providerID string, concurrency int) *Semaphore {
	return globalSemaphore(providerID, concurrency)
}

// HandleProxy 处理 /v1/* 的通用转发（候选链降级 + 熔断 + 流式回传）。
func (a *App) HandleProxy(w http.ResponseWriter, r *http.Request) {
	h := &handlerCtx{
		start:     time.Now(),
		origPath:  r.URL.Path,
		method:    r.Method,
		requestID: ShortID(),
		cfg:       a.Cfg.Get(),
		apiKey:    auth.ExtractBearer(r.Header.Get("Authorization")),
	}
	h.logDetail = orDefault(h.cfg.LogDetail, "basic")
	h.logBody = h.cfg.LogBody
	h.proxyLog(a, logging.LevelInfo, "[API Proxy] "+h.method+" "+h.origPath+" initiated")

	// 解析请求体
	var reqBody map[string]interface{}
	if h.method == "POST" || h.method == "PUT" {
		data, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "Invalid request body"})
			return
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &reqBody); err != nil {
				writeJSON(w, 400, map[string]string{"error": "Invalid JSON body"})
				return
			}
		}
	} else {
		_, _ = io.Copy(io.Discard, r.Body)
	}

	// virtual key 校验
	if h.cfg.EnableVirtualKey {
		matched := false
		if h.apiKey != "" {
			for _, k := range h.cfg.Keys {
				if k.Key == h.apiKey {
					matched = true
					break
				}
			}
		}
		if !matched {
			if len(h.cfg.Keys) == 0 {
				h.proxyLog(a, logging.LevelWarn, "Virtual key mode enabled but no keys configured. Proceeding with default routing.")
			} else {
				a.Logger.Log(logging.LevelError, "Virtual key validation failed for request to "+h.origPath, "proxy", h.requestID)
				writeJSON(w, 401, map[string]string{"error": "Unauthorized: Invalid or missing virtual key"})
				return
			}
		}
	}

	cands, bound, fromGroup := a.selectCandidates(h.cfg, h.apiKey, &h.keyName)
	if !bound && len(cands) == 0 {
		msg := "No active provider enabled or allowed for request to " + h.origPath
		h.proxyLog(a, logging.LevelError, msg)
		a.logRequest(h, "", h.reqModel, 503, 0, 0, 0, 0, false, msg)
		writeJSON(w, 503, map[string]string{"error": "Service Unavailable: No provider enabled/authorized"})
		return
	}

	if model, ok := reqBody["model"].(string); ok {
		h.reqModel = model
	}
	origModel := h.reqModel

	for _, p := range cands {
		// Group 路由: 懒探测冷却判断（provider+model 粒度）
		if fromGroup && h.reqModel != "" {
			if a.Health.InCooldown(p.ID, h.reqModel) {
				h.degradedFrom = append(h.degradedFrom, p.Name+" (cooldown/"+h.reqModel+")")
				h.proxyLog(a, logging.LevelWarn, "[API Proxy] Skipping "+p.Name+"/"+h.reqModel+" (in cooldown)")
				continue
			}
		}
		// circuit breaker（兼容：始终检查，与 group 冷却互不干扰）
		if a.Breaker.InCooldown(p.ID) {
			h.degradedFrom = append(h.degradedFrom, p.Name+" (circuit-breaker)")
			h.proxyLog(a, logging.LevelWarn, "[API Proxy Degrade] Skipping "+p.Name+" (circuit breaker), trying next provider")
			continue
		}

		// 模型校验与替换（仅 POST/PUT 且有 model），每轮基于用户原始请求模型重新判断
		candBody := reqBody
		candModel := origModel
		if (h.method == "POST" || h.method == "PUT") && candModel != "" {
			if !containsStr(p.Models, candModel) && len(p.Models) > 0 {
				fallback := orDefault(p.DefaultModel, p.Models[0])
				if h.logDetail == "all" {
					h.proxyLog(a, logging.LevelWarn, "[API Proxy] Model '"+candModel+"' not supported by provider '"+p.Name+"'. Substituting with fallback '"+fallback+"'.")
				}
				cb := make(map[string]interface{}, len(candBody)+1)
				for k, v := range candBody {
					cb[k] = v
				}
				cb["model"] = fallback
				candBody = cb
				candModel = fallback
			}
		}
		h.reqModel = candModel

		targetURL := ResolveTargetURL(p.BaseURL, p.OpenAIEndpoint, h.origPath)
		sub := h.origPath
		if idx := strings.Index(h.origPath, "/v1"); idx >= 0 {
			sub = h.origPath[idx+3:]
		}
		if !strings.HasPrefix(sub, "/") {
			sub = "/" + sub
		}
		pathRewritten := p.OpenAIEndpoint != "" && p.OpenAIEndpoint != sub
		h.proxyLog(a, logging.LevelInfo, "[API Proxy Forward] "+h.method+" "+h.origPath+" -> "+p.Name+
			" ("+orDefault(candModel, "default")+")"+
			ifAny(h.keyName != "", " [Key: "+h.keyName+"]", "")+
			ifAny(pathRewritten, " => "+p.OpenAIEndpoint, ""))
		if h.logDetail == "all" {
			h.proxyLog(a, logging.LevelInfo, "[API Proxy Request URL] "+h.method+" "+targetURL)
			if h.logBody && (h.method == "POST" || h.method == "PUT") && candBody != nil {
				if b, err := json.Marshal(candBody); err == nil {
					h.proxyLog(a, logging.LevelInfo, "[API Proxy Request Body] "+string(b))
				}
			}
		}

		// 并发控制（覆盖整个转发+回传周期）
		sem := a.Sem(p.ID, p.Concurrency)

		res, cancelFn, aborted, reason, status, timeoutErr := a.forwardOnce(h, p, candBody, targetURL, r.Context(), sem)
		if aborted {
			h.proxyLog(a, logging.LevelWarn, "[API Proxy Aborted] Client closed connection ("+spanMs(h.start)+")")
			a.logRequest(h, p.Name, h.reqModel, 499, 0, 0, 0, 0, false, "client closed connection")
			return
		}
		if res != nil {
			a.Breaker.RecordSuccess(p.ID)
			if fromGroup {
				a.Health.RecordSuccess(p.ID, candModel)
			}
			a.streamResponse(w, r, h, p, res, isStream(res))
			cancelFn()
			return
		}
		a.Breaker.RecordFailure(p.ID)
		if fromGroup {
			a.Health.RecordFailure(p.ID, h.reqModel)
		}
		if timeoutErr {
			h.timedOut = true
		}
		h.degradedFrom = append(h.degradedFrom, p.Name+" ("+reason+")")
		h.lastStatus = status
		h.lastProvider = p.Name
		h.failureLogged = true
		a.logRequest(h, p.Name, candModel, status, 0, 0, 0, 0, false, reason)
		h.proxyLog(a, logging.LevelWarn, "[API Proxy Degrade] "+p.Name+" failed: "+reason+", trying next provider")
	}

	// 全部失败
	last := "all providers failed"
	if len(h.degradedFrom) > 0 {
		last = h.degradedFrom[len(h.degradedFrom)-1]
	}
	status := 502
	errMsg := "Provider gateway error: " + last
	if h.timedOut {
		status = 504
		errMsg = "Upstream timeout: " + last
	} else if h.lastStatus >= 400 {
		status = h.lastStatus
		errMsg = "Provider gateway error: " + last
	}
	h.proxyLog(a, levelFor(h.timedOut), "[API Proxy "+
		ifAny(h.timedOut, "Timeout", "Error")+"] All providers failed: "+last+" ("+spanMs(h.start)+")")
	if !h.failureLogged {
		a.logRequest(h, h.lastProvider, h.reqModel, status, 0, 0, 0, 0, false, last)
	}
	writeJSON(w, status, map[string]string{"error": errMsg})
}

// forwardOnce 对单个 provider 发起请求。res 非空表示成功；cancelFn 需在
// resp.Body 读取完毕后调用（推迟取消，避免流式响应被提前中断）。
func (a *App) forwardOnce(h *handlerCtx, p *Provider, candBody map[string]interface{}, targetURL string, clientCtx context.Context, sem *Semaphore) (res *http.Response, cancelFn context.CancelFunc, aborted bool, reason string, status int, timeoutErr bool) {
	if sem != nil {
		sem.Acquire()
		defer sem.Release()
	}

	ctx, cancel := context.WithCancel(clientCtx)

	var bodyReader io.Reader
	if (h.method == "POST" || h.method == "PUT") && candBody != nil {
		if buf, err := json.Marshal(candBody); err == nil {
			bodyReader = bytes.NewReader(buf)
		}
	}
	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		hdr.Set("Authorization", "Bearer "+p.APIKey)
	}
	if h.logDetail == "all" {
		h.proxyLog(a, logging.LevelInfo, "[API Proxy Request Headers] "+formatHeaders(hdr))
	}

	// 超时仅作用于响应头阶段；头部到达后取消计时器，流式阶段不设限。
	headersDone := make(chan struct{})
	timeoutErrState := false
	if p.Timeout > 0 {
		t := time.AfterFunc(p.Timeout, func() {
			select {
			case <-headersDone:
			default:
				timeoutErrState = true
				cancel()
			}
		})
		defer t.Stop()
	}

	resp, err := a.Client.Do(ctx, h.method, targetURL, hdr, bodyReader, 0)
	close(headersDone)
	if err != nil {
		cancel()
		if clientCtx.Err() == context.Canceled {
			return nil, nil, true, "", 0, false
		}
		if timeoutErrState {
			return nil, nil, false, "timeout after " + p.Timeout.String(), 504, true
		}
		return nil, nil, false, "connection error: " + err.Error(), 502, false
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if h.detailActiveFor(resp.StatusCode) {
			if h.logDetail == "error" {
				h.proxyLog(a, logging.LevelInfo, "[API Proxy Request Headers] "+formatHeaders(hdr))
			}
			h.proxyLog(a, logging.LevelInfo, "[API Proxy Response Headers] "+formatHeaders(resp.Header))
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		cancel()
		return nil, nil, false, "HTTP "+itoa(resp.StatusCode), resp.StatusCode, false
	}
	return resp, cancel, false, "", resp.StatusCode, false
}

// streamResponse 将成功上游响应回传给客户端，同时统计 usage 并写请求日志。
func (a *App) streamResponse(w http.ResponseWriter, r *http.Request, h *handlerCtx, p *Provider, resp *http.Response, stream bool) {
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

	detailActive := h.detailActiveFor(resp.StatusCode)
	if detailActive {
		h.proxyLog(a, logging.LevelInfo, "[API Proxy Response Headers] "+formatHeaders(resp.Header))
	}

	var parser UsageParser
	var body bytes.Buffer
	resModel := h.reqModel
	var prompt, completion, cached int
	var statusOut = resp.StatusCode
	errMsg := ""

	if h.logBody && detailActive {
		h.proxyLog(a, logging.LevelInfo, "[API Proxy Complete] Status "+itoa(resp.StatusCode)+" ("+spanMs(h.start)+") -> "+p.Name)
	} else if h.logDetail != "off" {
		h.proxyLog(a, logging.LevelInfo, "[API Proxy Complete] Status "+itoa(resp.StatusCode)+" ("+spanMs(h.start)+") -> "+p.Name)
	}

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
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
		if err == io.EOF {
			break
		}
		if err != nil {
			if r.Context().Err() == context.Canceled {
				errMsg = "client closed connection"
				statusOut = 499
			} else {
				errMsg = "stream error: " + err.Error()
				statusOut = 502
			}
			break
		}
	}
	resp.Body.Close()

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
			var m string
			if usage, mm := parseUsageJSON(body.Bytes()); usage != nil {
				m = mm
				prompt = usage.PromptTokens
				completion = usage.CompletionTokens
				cached = usage.CachedTokens
			}
			if m != "" {
				resModel = m
			}
		}
	}

	if h.logBody && detailActive && body.Len() > 0 {
		h.proxyLog(a, logging.LevelInfo, "[API Proxy Response Body] "+body.String())
	}

	switch {
	case errMsg != "" && statusOut == 499:
		h.proxyLog(a, logging.LevelWarn, "[API Proxy Aborted] Client closed connection ("+spanMs(h.start)+")")
	case errMsg != "" && statusOut == 504:
		h.proxyLog(a, logging.LevelWarn, "[API Proxy Timeout] Upstream timeout ("+spanMs(h.start)+")")
	case errMsg != "":
		h.proxyLog(a, logging.LevelError, "[API Proxy Error] Forwarding failed: "+errMsg+" ("+spanMs(h.start)+")")
	}

	a.logRequest(h, p.Name, resModel, statusOut, prompt, completion, cached, prompt+completion, stream, errMsg)
}

// parseUsageJSON 从非流式响应 JSON 提取 usage 与 model。
func parseUsageJSON(data []byte) (*Usage, string) {
	var obj struct {
		Model string `json:"model"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			CachedTokens     int `json:"cached_tokens"`
			Details          *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CacheHitTokens int `json:"prompt_cache_hit_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, ""
	}
	if obj.Usage == nil {
		return nil, obj.Model
	}
	u := &Usage{PromptTokens: obj.Usage.PromptTokens, CompletionTokens: obj.Usage.CompletionTokens}
	switch {
	case obj.Usage.Details != nil && obj.Usage.Details.CachedTokens > 0:
		u.CachedTokens = obj.Usage.Details.CachedTokens
	case obj.Usage.CacheHitTokens > 0:
		u.CachedTokens = obj.Usage.CacheHitTokens
	case obj.Usage.CachedTokens > 0:
		u.CachedTokens = obj.Usage.CachedTokens
	}
	return u, obj.Model
}

func isStream(resp *http.Response) bool {
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}

// logRequest 写入一条请求日志（持久化 + 有界裁剪）。
func (a *App) logRequest(h *handlerCtx, provider, model string, status int,
	prompt, completion, cached, total int, stream bool, errMsg string) {
	if a.ReqLog == nil {
		return
	}
	rl := &logstore.RequestLog{
		ID:             ShortID(),
		Timestamp:      time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		KeyName:        h.keyName,
		KeyID:          logging.MaskKey(h.apiKey),
		Model:          model,
		Provider:       provider,
		Path:           h.origPath,
		Method:         h.method,
		PromptTokens:   prompt,
		CompletionToks: completion,
		CachedTokens:   cached,
		TotalTokens:    total,
		Status:         status,
		DurationMS:     int(time.Since(h.start).Milliseconds()),
		Stream:         stream,
		Error:          errMsg,
		RequestID:      h.requestID,
		HasDetail:      a.Logger.HasRelatedLogs(h.requestID),
	}
	_ = a.ReqLog.Insert(rl)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func ifAny(cond bool, ifTrue, ifFalse string) string {
	if cond {
		return ifTrue
	}
	return ifFalse
}

// detailActiveFor 判断当前日志级别是否需要为给定状态码输出请求/响应头细节。
// error 级别仅在状态码 >=400（上游报错）时输出，all 级别始终输出。
func (h *handlerCtx) detailActiveFor(status int) bool {
	return h.logDetail == "all" || (h.logDetail == "error" && status >= 400)
}

// formatHeaders 将 Header 拍平为 "K: V" 列表，跳过 set-cookie 并对
// Authorization 做脱敏，避免把上游密钥写入日志。
func formatHeaders(hdr http.Header) string {
	parts := []string{}
	for k, vv := range hdr {
		lk := strings.ToLower(k)
		if lk == "set-cookie" {
			continue
		}
		for _, v := range vv {
			if lk == "authorization" {
				parts = append(parts, k+": "+maskAuthHeader(v))
			} else {
				parts = append(parts, k+": "+v)
			}
		}
	}
	return strings.Join(parts, " | ")
}

// maskAuthHeader 对 Bearer 令牌做脱敏，保留前缀。
func maskAuthHeader(v string) string {
	if strings.HasPrefix(v, "Bearer ") {
		return "Bearer " + logging.MaskKey(strings.TrimPrefix(v, "Bearer "))
	}
	return logging.MaskKey(v)
}

func levelFor(timeout bool) string {
	if timeout {
		return logging.LevelWarn
	}
	return logging.LevelError
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func spanMs(t time.Time) string { return fmt.Sprintf("%dms", time.Since(t).Milliseconds()) }

