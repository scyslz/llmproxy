package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"llmproxy/internal/convert"
	"llmproxy/internal/domain"
	"llmproxy/internal/logging"
)

const (
	protoChat      = "chat"
	protoResponses = "responses"
)

// inboundProtocol 根据请求路径判断客户端使用的协议。
func inboundProtocol(path string) string {
	if strings.HasSuffix(path, "/v1/responses") || strings.HasSuffix(path, "/responses") {
		return protoResponses
	}
	return protoChat
}

// upstreamProtocol 解析 provider 对指定模型期望的协议；known=false 表示未知（需探测）。
func (p *Provider) upstreamProtocol(model string) (proto string, known bool) {
	if p.ModelProtocols != nil {
		if mp, ok := p.ModelProtocols[model]; ok && mp != "" {
			return mp, true
		}
	}
	if p.Protocol != "" {
		return p.Protocol, true
	}
	return "", false
}

func targetSubPath(proto string) string {
	if proto == protoResponses {
		return "/responses"
	}
	return "/chat/completions"
}

// buildTargetURL 根据协议构造上游 URL（等价于 ResolveTargetURL 但使用目标协议子路径）。
func buildTargetURL(p *Provider, proto string) string {
	base := strings.TrimRight(p.BaseURL, "/")
	var configured string
	if proto == protoResponses {
		configured = strings.TrimSpace(p.ResponsesEndpoint)
	} else {
		configured = strings.TrimSpace(p.ChatEndpoint)
	}
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
	return endpointBase + targetSubPath(proto)
}

// convertRequestBody 将请求体在 inbound 协议与目标协议间转换。
func convertRequestBody(inbound, target string, body map[string]interface{}) (map[string]interface{}, error) {
	if inbound == target {
		return body, nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	switch {
	case inbound == protoChat && target == protoResponses:
		var req convert.GeneralOpenAIRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		out, err := convert.ChatCompletionsRequestToResponsesRequest(&req)
		if err != nil {
			return nil, err
		}
		return marshalMap(out)
	case inbound == protoResponses && target == protoChat:
		var req convert.OpenAIResponsesRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		out, err := convert.ResponsesRequestToChatCompletionsRequest(&req)
		if err != nil {
			return nil, err
		}
		return marshalMap(out)
	}
	return body, nil
}

func marshalMap(v interface{}) (map[string]interface{}, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// convertUsageToProxy 将 convert.Usage 映射到代理日志用的 usage 形态。
func convertUsageToProxy(u *convert.Usage) (prompt, completion, cached int) {
	if u == nil {
		return 0, 0, 0
	}
	prompt = u.PromptTokens
	if prompt == 0 {
		prompt = u.InputTokens
	}
	completion = u.CompletionTokens
	if completion == 0 {
		completion = u.OutputTokens
	}
	cached = u.PromptTokensDetails.CachedTokens
	return prompt, completion, cached
}

// transpileResponse 在 inbound 协议与目标协议不一致时，将上游响应转写为客户端
// 期望的协议后回传，并统计 usage 写请求日志。
func (a *App) transpileResponse(w http.ResponseWriter, r *http.Request, h *handlerCtx, p *Provider, res *http.Response, inbound, target, model string) {
	for k, vv := range res.Header {
		lk := strings.ToLower(k)
		if lk == "transfer-encoding" || lk == "content-encoding" || lk == "content-length" {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)

	stream := isStream(res)
	if h.logDetail != "off" {
		h.proxyLog(a, logging.LevelInfo, "[API Proxy Complete] Status "+itoa(res.StatusCode)+" ("+spanMs(h.start)+") -> "+p.Name+" [converted "+inbound+"->"+target+"]")
	}

	var prompt, completion, cached int
	resModel := model
	errMsg := ""

	if !stream {
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		out, usage, _, err := convertResponseBody(inbound, target, body)
		if err != nil {
			errMsg = "conversion error: " + err.Error()
			a.logRequest(h, p.Name, resModel, 502, 0, 0, 0, 0, false, errMsg)
			return
		}
		prompt, completion, cached = convertUsageToProxy(usage)
		_, _ = w.Write(out)
		a.logRequest(h, p.Name, resModel, res.StatusCode, prompt, completion, cached, prompt+completion, false, "")
		return
	}

	// 流式：逐事件转写
	var state interface{}
	if inbound == protoChat && target == protoResponses {
		state = convert.NewChatToResponsesStreamState("", model)
	} else {
		state = convert.NewResponsesToChatStreamState(model, true)
	}

	reader := bufio.NewReader(res.Body)
	var buf bytes.Buffer
	for {
		line, err := reader.ReadString('\n')
		buf.WriteString(line)
		if err != nil && line == "" {
			break
		}
		if !strings.HasSuffix(buf.String(), "\n\n") && err == nil {
			continue
		}
		eventBlock := buf.String()
		buf.Reset()
		dataJSON := extractSSEData(eventBlock)
		if dataJSON == "" {
			if err != nil {
				break
			}
			continue
		}
		chunks := transpileSSEEvent(state, target, dataJSON)
		for _, c := range chunks {
			if _, werr := w.Write([]byte(c)); werr != nil {
				errMsg = "client closed connection"
				break
			}
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
		if err != nil {
			break
		}
	}
	res.Body.Close()

	var usage *convert.Usage
	switch s := state.(type) {
	case *convert.ChatToResponsesStreamState:
		for _, ev := range convert.FinalizeChatCompletionsStreamToResponses(s) {
			if b, e := json.Marshal(ev); e == nil {
				_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			}
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		usage = s.Usage
	case *convert.ResponsesToChatStreamState:
		for _, ch := range convert.FinalizeResponsesToChatStream(s) {
			if b, e := json.Marshal(ch); e == nil {
				_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			}
		}
		usage = s.Usage
	}
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	prompt, completion, cached = convertUsageToProxy(usage)
	a.logRequest(h, p.Name, resModel, res.StatusCode, prompt, completion, cached, prompt+completion, true, errMsg)
}

// convertResponseBody 转写非流式响应体。
func convertResponseBody(inbound, target string, raw []byte) (out []byte, usage *convert.Usage, resModel string, err error) {
	switch {
	case inbound == protoChat && target == protoResponses:
		var resp convert.OpenAITextResponse
		if e := json.Unmarshal(raw, &resp); e != nil {
			return nil, nil, "", e
		}
		conv, u, e := convert.ChatCompletionsResponseToResponsesResponse(&resp, resp.Id)
		if e != nil {
			return nil, nil, "", e
		}
		b, _ := json.Marshal(conv)
		return b, u, conv.Model, nil
	case inbound == protoResponses && target == protoChat:
		var resp convert.OpenAIResponsesResponse
		if e := json.Unmarshal(raw, &resp); e != nil {
			return nil, nil, "", e
		}
		conv, u, e := convert.ResponsesResponseToChatCompletionsResponse(&resp, resp.ID)
		if e != nil {
			return nil, nil, "", e
		}
		b, _ := json.Marshal(conv)
		return b, u, conv.Model, nil
	}
	return raw, nil, "", nil
}

// transpileSSEEvent 将单个上游 SSE data 帧转写为目标协议的一段 SSE 文本（含分隔）。
func transpileSSEEvent(state interface{}, target, dataJSON string) []string {
	var out []string
	if target == protoResponses {
		var event convert.ChatCompletionsStreamResponse
		if err := json.Unmarshal([]byte(dataJSON), &event); err != nil {
			return nil
		}
		events, _ := convert.ChatCompletionsStreamChunkToResponsesEvents(&event, state.(*convert.ChatToResponsesStreamState))
		for _, ev := range events {
			if b, e := json.Marshal(ev.Payload); e == nil {
				out = append(out, "event: "+ev.Type+"\ndata: "+string(b)+"\n\n")
			}
		}
	} else {
		var event convert.ResponsesStreamResponse
		if err := json.Unmarshal([]byte(dataJSON), &event); err != nil {
			return nil
		}
		chunks, _ := convert.ResponsesStreamEventToChatChunks(&event, state.(*convert.ResponsesToChatStreamState))
		for _, ch := range chunks {
			if b, e := json.Marshal(ch); e == nil {
				out = append(out, "data: "+string(b)+"\n\n")
			}
		}
	}
	return out
}

// extractSSEData 从一个 SSE 事件块中提取 data: 负载 JSON（忽略 event:/注释行）。
func extractSSEData(block string) string {
	lines := strings.Split(block, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	return ""
}

// persistDiscoveredProtocol 将探测成功的 (provider, model) -> protocol 写入配置。
func (a *App) persistDiscoveredProtocol(providerID, model, proto string) {
	_ = a.Cfg.Update(func(c *domain.Config) {
		for i := range c.Providers {
			if c.Providers[i].ID == providerID {
				if c.Providers[i].ModelProtocols == nil {
					c.Providers[i].ModelProtocols = map[string]string{}
				}
				c.Providers[i].ModelProtocols[model] = proto
				return
			}
		}
	})
}
