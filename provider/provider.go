package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"llmproxy/config"
)

// Client Provider 客户端
type Client struct {
	p  *config.Provider
	hc *http.Client
}

// NewClient 创建客户端
func NewClient(p *config.Provider) *Client {
	return &Client{
		p: p,
		hc: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Chat 转发聊天请求（直接用 base_url 作为完整请求路径）
func (c *Client) Chat(ctx context.Context, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.p.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.p.APIKey)

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return resp, nil
}

// ListModels 获取模型列表（如果 provider 支持）
func (c *Client) ListModels() ([]string, error) {
	// 这里可以调用 /v1/models，但当前直接用配置的 models
	return c.p.Models, nil
}

// Ping 检查 provider 是否可用
func (c *Client) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	// 简单发一个空请求测试连通性
	body := []byte(`{"model":"","messages":[]}`)
	resp, err := c.Chat(ctx, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}
