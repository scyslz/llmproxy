package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider 是转发目标上游的实例化配置快照（随配置变更重建）。
type Provider struct {
	ID             string
	Name           string
	BaseURL        string
	APIKey         string
	Models         []string
	ChatEndpoint      string
	ResponsesEndpoint string
	DefaultModel   string
	Protocol       string
	ModelProtocols map[string]string
	Timeout        time.Duration
	Concurrency    int
}

// Client 执行对上游的具体 HTTP 请求。
type Client struct {
	httpClient *http.Client
}

// NewClient 构造上游转发客户端。
func NewClient() *Client {
	return &Client{httpClient: &http.Client{Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}}}
}

// Do 将请求发送到上游，返回响应（调用方负责关闭 Body）。
// timeout>0 时为请求施加超时；ctx 取消（客户端断开）则返回 ctx 的错误。
func (c *Client) Do(ctx context.Context, method, url string, headers http.Header, body io.Reader, timeout time.Duration) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header = headers
	if timeout > 0 {
		dctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		req = req.WithContext(dctx)
	}
	return c.httpClient.Do(req)
}

// UsageParser 增量解析 SSE 流中的 model 与 usage 字段。
type UsageParser struct {
	buf   string
	Model string
	Usage *Usage
}

// Usage 是 OpenAI 风格的 token 统计。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

// Push 喂入一段文本，兼容 chat 与 responses 两种 usage 形态及流式嵌套结构。
func (p *UsageParser) Push(text string) {
	p.buf += text
	for {
		idx := strings.IndexByte(p.buf, '\n')
		if idx < 0 {
			return
		}
		line := strings.TrimSpace(p.buf[:idx])
		p.buf = p.buf[idx+1:]
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if data == "" || data == "[DONE]" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(data), &raw); err != nil {
			continue
		}
		if v, ok := raw["model"]; ok {
			var m string
			if json.Unmarshal(v, &m) == nil && m != "" {
				p.Model = m
			}
		}
		var usageRaw json.RawMessage
		if v, ok := raw["usage"]; ok {
			usageRaw = v
		} else if v, ok := raw["response"]; ok {
			var resp struct {
				Model string          `json:"model"`
				Usage json.RawMessage `json:"usage"`
			}
			if json.Unmarshal(v, &resp) == nil {
				if resp.Model != "" && p.Model == "" {
					p.Model = resp.Model
				}
				if len(resp.Usage) > 0 {
					usageRaw = resp.Usage
				}
			}
		}
		if len(usageRaw) == 0 {
			continue
		}
		var u struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			CachedTokens     int `json:"cached_tokens"`
			Details          *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CacheHitTokens int `json:"prompt_cache_hit_tokens"`
			InputTokens    int `json:"input_tokens"`
			OutputTokens   int `json:"output_tokens"`
			InputDetails   *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		}
		if err := json.Unmarshal(usageRaw, &u); err != nil {
			continue
		}
		pt := u.PromptTokens
		ct := u.CompletionTokens
		cached := 0
		switch {
		case u.Details != nil && u.Details.CachedTokens > 0:
			cached = u.Details.CachedTokens
		case u.CacheHitTokens > 0:
			cached = u.CacheHitTokens
		case u.CachedTokens > 0:
			cached = u.CachedTokens
		case u.InputDetails != nil && u.InputDetails.CachedTokens > 0:
			cached = u.InputDetails.CachedTokens
		}
		if pt == 0 && u.InputTokens > 0 {
			pt = u.InputTokens
		}
		if ct == 0 && u.OutputTokens > 0 {
			ct = u.OutputTokens
		}
		if pt != 0 || ct != 0 || cached != 0 {
			p.Usage = &Usage{PromptTokens: pt, CompletionTokens: ct, CachedTokens: cached}
		}
	}
}

// ResolveTargetURL 构建上游目标 URL，行为对齐 Node 版本：
//   - 配置了 openaiEndpoint 时直接使用（拼在 baseUrl 后）
//   - 否则在 baseUrl 后附加 /v1 + 去掉 /v1 前缀的请求子路径
func ResolveTargetURL(baseURL, chatEndpoint, requestPath string) string {
	base := strings.TrimRight(baseURL, "/")
	configured := strings.TrimSpace(chatEndpoint)
	if configured != "" {
		if !strings.HasPrefix(configured, "/") {
			configured = "/" + configured
		}
		return base + configured
	}
	endpointBase := base
	for _, s := range []string{"/v1", "/openai", "/v1beta", "/api", "/v4", "/v2", "/v3"} {
		if strings.HasSuffix(base, s) {
			endpointBase = base
			goto haveBase
		}
	}
	endpointBase = base + "/v1"
haveBase:
	sub := requestPath
	if idx := strings.Index(requestPath, "/v1"); idx >= 0 {
		sub = requestPath[idx+3:]
	}
	if !strings.HasPrefix(sub, "/") {
		sub = "/" + sub
	}
	return endpointBase + sub
}

// ShortID 生成 16 位十六进制请求标识。
func ShortID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

