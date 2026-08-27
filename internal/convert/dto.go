// OpenAI Chat Completions / Responses 数据结构子集。
//
// Derived from QuantumNous/new-api relaykit/dto (AGPL-3.0, Copyright QuantumNous).
// 仅保留本项目协议转换所需的字段；未保留字段会在跨协议转换时被丢弃，
// 同协议直通路径不受影响（直通不经过本结构体）。

package convert

import (
	"encoding/json"
	"strings"
)

const (
	ContentTypeText       = "text"
	ContentTypeImageURL   = "image_url"
	ContentTypeInputAudio = "input_audio"
	ContentTypeFile       = "file"
	ContentTypeVideoUrl   = "video_url" // 阿里百炼视频识别
)

const CustomType = "custom"

// Usage 覆盖 chat 与 responses 两种 usage 形态（prompt_tokens 与 input_tokens 同结构体共存）。
type Usage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	TotalTokens          int `json:"total_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens,omitempty"`

	PromptTokensDetails    InputTokenDetails  `json:"prompt_tokens_details"`
	CompletionTokenDetails OutputTokenDetails `json:"completion_tokens_details"`
	InputTokens            int                `json:"input_tokens"`
	OutputTokens           int                `json:"output_tokens"`
	InputTokensDetails     *InputTokenDetails `json:"input_tokens_details,omitempty"`
}

type InputTokenDetails struct {
	CachedTokens         int `json:"cached_tokens"`
	CachedCreationTokens int `json:"cached_creation_tokens,omitempty"`
	CacheWriteTokens     int `json:"cache_write_tokens,omitempty"`
	TextTokens           int `json:"text_tokens"`
	AudioTokens          int `json:"audio_tokens"`
	ImageTokens          int `json:"image_tokens"`
}

type OutputTokenDetails struct {
	TextTokens      int `json:"text_tokens"`
	AudioTokens     int `json:"audio_tokens"`
	ImageTokens     int `json:"image_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
}

type ResponseFormat struct {
	Type       string          `json:"type,omitempty"`
	JsonSchema json.RawMessage `json:"json_schema,omitempty"`
}

type ToolCallRequest struct {
	ID       string          `json:"id,omitempty"`
	Type     string          `json:"type"`
	Function FunctionRequest `json:"function,omitempty"`
	Custom   json.RawMessage `json:"custom,omitempty"`
}

type FunctionRequest struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name"`
	Parameters  any    `json:"parameters,omitempty"`
	Arguments   string `json:"arguments,omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
	// IncludeObfuscation is only for /v1/responses stream payload.
	IncludeObfuscation bool `json:"include_obfuscation,omitempty"`
}

// GeneralOpenAIRequest 是 /v1/chat/completions 请求体。
type GeneralOpenAIRequest struct {
	Model               string            `json:"model,omitempty"`
	N                   *int              `json:"n,omitempty"`
	Messages            []Message         `json:"messages,omitempty"`
	Stream              *bool             `json:"stream,omitempty"`
	StreamOptions       *StreamOptions    `json:"stream_options,omitempty"`
	MaxTokens           *uint             `json:"max_tokens,omitempty"`
	MaxCompletionTokens *uint             `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string            `json:"reasoning_effort,omitempty"`
	Temperature         *float64          `json:"temperature,omitempty"`
	TopP                *float64          `json:"top_p,omitempty"`
	TopLogProbs         *int              `json:"top_logprobs,omitempty"`
	FrequencyPenalty    *float64          `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64          `json:"presence_penalty,omitempty"`
	ResponseFormat      *ResponseFormat   `json:"response_format,omitempty"`
	ParallelTooCalls    *bool             `json:"parallel_tool_calls,omitempty"`
	Tools               []ToolCallRequest `json:"tools,omitempty"`
	ToolChoice          any               `json:"tool_choice,omitempty"`
	User                json.RawMessage   `json:"user,omitempty"`
	ServiceTier         json.RawMessage   `json:"service_tier,omitempty"`
	Store               json.RawMessage   `json:"store,omitempty"`
	PromptCacheKey      string            `json:"prompt_cache_key,omitempty"`
	// PromptCacheRetention/Metadata/SafetyIdentifier 原样透传
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention,omitempty"`
	Metadata             json.RawMessage `json:"metadata,omitempty"`
	SafetyIdentifier     json.RawMessage `json:"safety_identifier,omitempty"`
	EnableThinking       json.RawMessage `json:"enable_thinking,omitempty"`
	ThinkingBudget       json.RawMessage `json:"thinking_budget,omitempty"`
}

type Message struct {
	Role             string          `json:"role"`
	Content          any             `json:"content"`
	Name             *string         `json:"name,omitempty"`
	Prefix           *bool           `json:"prefix,omitempty"`
	ReasoningContent *string         `json:"reasoning_content,omitempty"`
	Reasoning        *string         `json:"reasoning,omitempty"`
	ToolCalls        json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallId       string          `json:"tool_call_id,omitempty"`
	parsedContent    []MediaContent
}

type MediaContent struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ImageUrl   any             `json:"image_url,omitempty"`
	InputAudio any             `json:"input_audio,omitempty"`
	File       any             `json:"file,omitempty"`
	VideoUrl   any             `json:"video_url,omitempty"`
	// OpenRouter Params
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

func (m *MediaContent) GetImageMedia() *MessageImageUrl {
	if m.ImageUrl != nil {
		if itemMap, ok := m.ImageUrl.(map[string]any); ok {
			return &MessageImageUrl{
				Url:      interface2String(itemMap["url"]),
				Detail:   interface2String(itemMap["detail"]),
				MimeType: interface2String(itemMap["mime_type"]),
			}
		}
	}
	return nil
}

type MessageImageUrl struct {
	Url      string `json:"url"`
	Detail   string `json:"detail,omitempty"`
	MimeType string
}

type MessageInputAudio struct {
	Data   string `json:"data"` //base64
	Format string `json:"format"`
}

type MessageFile struct {
	FileName string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
	FileId   string `json:"file_id,omitempty"`
}

type MessageVideoUrl struct {
	Url string `json:"url"`
}

func (m *Message) GetReasoningContent() string {
	if m.ReasoningContent == nil && m.Reasoning == nil {
		return ""
	}
	if m.ReasoningContent != nil {
		return *m.ReasoningContent
	}
	return *m.Reasoning
}

func (m *Message) ParseToolCalls() []ToolCallRequest {
	if m.ToolCalls == nil {
		return nil
	}
	var toolCalls []ToolCallRequest
	if err := unmarshalJSON(m.ToolCalls, &toolCalls); err == nil {
		return toolCalls
	}
	return toolCalls
}

func (m *Message) SetToolCalls(toolCalls any) {
	toolCallsJSON, _ := marshalJSON(toolCalls)
	m.ToolCalls = toolCallsJSON
}

func (m *Message) StringContent() string {
	switch v := m.Content.(type) {
	case string:
		return v
	case []any:
		var contentStr string
		for _, contentItem := range v {
			contentMap, ok := contentItem.(map[string]any)
			if !ok {
				continue
			}
			if contentMap["type"] == ContentTypeText {
				if subStr, ok := contentMap["text"].(string); ok {
					contentStr += subStr
				}
			}
		}
		return contentStr
	}
	return ""
}

func (m *Message) SetNullContent() {
	m.Content = nil
	m.parsedContent = nil
}

func (m *Message) SetStringContent(content string) {
	m.Content = content
	m.parsedContent = nil
}

func (m *Message) IsStringContent() bool {
	_, ok := m.Content.(string)
	return ok
}

func (m *Message) ParseContent() []MediaContent {
	if m.Content == nil {
		return nil
	}
	if len(m.parsedContent) > 0 {
		return m.parsedContent
	}

	content, ok := m.Content.(string)
	if ok {
		contentList := []MediaContent{{
			Type: ContentTypeText,
			Text: content,
		}}
		m.parsedContent = contentList
		return contentList
	}

	arrayContent, ok := m.Content.([]any)
	if !ok {
		return nil
	}

	var contentList []MediaContent
	for _, contentItemAny := range arrayContent {
		mediaItem, ok := contentItemAny.(MediaContent)
		if ok {
			contentList = append(contentList, mediaItem)
			continue
		}

		contentItem, ok := contentItemAny.(map[string]any)
		if !ok {
			continue
		}
		contentType, ok := contentItem["type"].(string)
		if !ok {
			continue
		}

		switch contentType {
		case ContentTypeText:
			if text, ok := contentItem["text"].(string); ok {
				contentList = append(contentList, MediaContent{
					Type: ContentTypeText,
					Text: text,
				})
			}

		case ContentTypeImageURL:
			imageUrl := contentItem["image_url"]
			temp := &MessageImageUrl{
				Detail: "high",
			}
			switch v := imageUrl.(type) {
			case string:
				temp.Url = v
			case map[string]interface{}:
				url, ok1 := v["url"].(string)
				detail, ok2 := v["detail"].(string)
				if ok2 {
					temp.Detail = detail
				}
				if ok1 {
					temp.Url = url
				}
			}
			contentList = append(contentList, MediaContent{
				Type:     ContentTypeImageURL,
				ImageUrl: temp,
			})

		case ContentTypeInputAudio:
			if audioData, ok := contentItem["input_audio"].(map[string]interface{}); ok {
				data, ok1 := audioData["data"].(string)
				format, ok2 := audioData["format"].(string)
				if ok1 && ok2 {
					contentList = append(contentList, MediaContent{
						Type: ContentTypeInputAudio,
						InputAudio: &MessageInputAudio{
							Data:   data,
							Format: format,
						},
					})
				}
			}
		case ContentTypeFile:
			if fileData, ok := contentItem["file"].(map[string]interface{}); ok {
				fileId, ok3 := fileData["file_id"].(string)
				if ok3 {
					contentList = append(contentList, MediaContent{
						Type: ContentTypeFile,
						File: &MessageFile{
							FileId: fileId,
						},
					})
				} else {
					fileName, ok1 := fileData["filename"].(string)
					fileDataStr, ok2 := fileData["file_data"].(string)
					if ok1 && ok2 {
						contentList = append(contentList, MediaContent{
							Type: ContentTypeFile,
							File: &MessageFile{
								FileName: fileName,
								FileData: fileDataStr,
							},
						})
					}
				}
			}
		case ContentTypeVideoUrl:
			if videoUrl, ok := contentItem["video_url"].(string); ok {
				contentList = append(contentList, MediaContent{
					Type: ContentTypeVideoUrl,
					VideoUrl: &MessageVideoUrl{
						Url: videoUrl,
					},
				})
			}
		}
	}

	if len(contentList) > 0 {
		m.parsedContent = contentList
	}
	return contentList
}

// https://platform.openai.com/docs/api-reference/responses/create
type OpenAIResponsesRequest struct {
	Model   string          `json:"model"`
	Input   json.RawMessage `json:"input,omitempty"`
	Include json.RawMessage `json:"include,omitempty"`
	// Conversation/ContextManagement/Prompt 用于 responses→chat 的不支持字段校验。
	Conversation      json.RawMessage `json:"conversation,omitempty"`
	ContextManagement json.RawMessage `json:"context_management,omitempty"`
	Instructions      json.RawMessage `json:"instructions,omitempty"`
	MaxOutputTokens   *uint           `json:"max_output_tokens,omitempty"`
	TopLogProbs       *int            `json:"top_logprobs,omitempty"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
	ParallelToolCalls json.RawMessage `json:"parallel_tool_calls,omitempty"`
	// FrequencyPenalty/PresencePenalty 是官方 Responses API 不存在但部分兼容上游接受的字段，原样转发。
	FrequencyPenalty json.RawMessage `json:"frequency_penalty,omitempty"`
	PresencePenalty  json.RawMessage `json:"presence_penalty,omitempty"`

	PreviousResponseID string     `json:"previous_response_id,omitempty"`
	Reasoning          *Reasoning `json:"reasoning,omitempty"`
	ServiceTier        string     `json:"service_tier,omitempty"`

	Store                json.RawMessage `json:"store,omitempty"`
	PromptCacheKey       json.RawMessage `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention,omitempty"`
	SafetyIdentifier     json.RawMessage `json:"safety_identifier,omitempty"`

	Stream        *bool          `json:"stream,omitempty"`
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	Text          json.RawMessage `json:"text,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	Tools         json.RawMessage `json:"tools,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	Truncation    json.RawMessage `json:"truncation,omitempty"`
	User          json.RawMessage `json:"user,omitempty"`
	Prompt        json.RawMessage `json:"prompt,omitempty"`
	// qwen 扩展
	EnableThinking json.RawMessage `json:"enable_thinking,omitempty"`
	ThinkingBudget json.RawMessage `json:"thinking_budget,omitempty"`
}

type Reasoning struct {
	Effort  string          `json:"effort,omitempty"`
	Summary string          `json:"summary,omitempty"`
	Mode    json.RawMessage `json:"mode,omitempty"`
	Context json.RawMessage `json:"context,omitempty"`
}

type Input struct {
	Type    string          `json:"type,omitempty"`
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

type MediaInput struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	FileUrl  string `json:"file_url,omitempty"`
	ImageUrl string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"` // 仅 input_image 有效
}

// ParseInput 将 Responses API `input` 字段归一化为 MediaInput 列表。
func (r *OpenAIResponsesRequest) ParseInput() []MediaInput {
	if r.Input == nil {
		return nil
	}

	var mediaInputs []MediaInput

	if getJsonType(r.Input) == "string" {
		var str string
		_ = unmarshalJSON(r.Input, &str)
		mediaInputs = append(mediaInputs, MediaInput{Type: "input_text", Text: str})
		return mediaInputs
	}

	if getJsonType(r.Input) == "array" {
		var inputs []Input
		_ = unmarshalJSON(r.Input, &inputs)
		for _, input := range inputs {
			switch getJsonType(input.Content) {
			case "string":
				var str string
				_ = unmarshalJSON(input.Content, &str)
				mediaInputs = append(mediaInputs, MediaInput{Type: "input_text", Text: str})
			case "array":
				var array []any
				_ = unmarshalJSON(input.Content, &array)
				for _, itemAny := range array {
					item, ok := itemAny.(map[string]any)
					if !ok {
						continue
					}
					typeVal, ok := item["type"].(string)
					if !ok {
						continue
					}
					switch typeVal {
					case "input_text":
						text, _ := item["text"].(string)
						mediaInputs = append(mediaInputs, MediaInput{Type: "input_text", Text: text})
					case "input_image":
						var imageUrl string
						switch v := item["image_url"].(type) {
						case string:
							imageUrl = v
						case map[string]any:
							if url, ok := v["url"].(string); ok {
								imageUrl = url
							}
						}
						mediaInputs = append(mediaInputs, MediaInput{Type: "input_image", ImageUrl: imageUrl})
					case "input_file":
						var fileUrl string
						switch v := item["file_url"].(type) {
						case string:
							fileUrl = v
						case map[string]any:
							if url, ok := v["url"].(string); ok {
								fileUrl = url
							}
						}
						mediaInputs = append(mediaInputs, MediaInput{Type: "input_file", FileUrl: fileUrl})
					}
				}
			}
		}
	}

	return mediaInputs
}

type OpenAITextResponseChoice struct {
	Index        int    `json:"index"`
	Message      Message `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type OpenAITextResponse struct {
	Id      string                     `json:"id"`
	Model   string                     `json:"model"`
	Object  string                     `json:"object"`
	Created any                        `json:"created"`
	Choices []OpenAITextResponseChoice `json:"choices"`
	Error   any                        `json:"error,omitempty"`
	Usage   Usage                      `json:"usage"`
}

type ChatCompletionsStreamResponseChoice struct {
	Delta        ChatCompletionsStreamResponseChoiceDelta `json:"delta,omitempty"`
	Logprobs     *any                                     `json:"logprobs"`
	FinishReason *string                                  `json:"finish_reason"`
	Index        int                                      `json:"index"`
}

type ChatCompletionsStreamResponseChoiceDelta struct {
	Content          *string            `json:"content,omitempty"`
	ReasoningContent *string            `json:"reasoning_content,omitempty"`
	Reasoning        *string            `json:"reasoning,omitempty"`
	Role             string             `json:"role,omitempty"`
	ToolCalls        []ToolCallResponse `json:"tool_calls,omitempty"`
}

func (c *ChatCompletionsStreamResponseChoiceDelta) SetContentString(s string) {
	c.Content = &s
}

func (c *ChatCompletionsStreamResponseChoiceDelta) GetContentString() string {
	if c.Content == nil {
		return ""
	}
	return *c.Content
}

func (c *ChatCompletionsStreamResponseChoiceDelta) GetReasoningContent() string {
	if c.ReasoningContent == nil && c.Reasoning == nil {
		return ""
	}
	if c.ReasoningContent != nil {
		return *c.ReasoningContent
	}
	return *c.Reasoning
}

type ToolCallResponse struct {
	// Index is not nil only in chat completion chunk object
	Index    *int             `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     any              `json:"type"`
	Function FunctionResponse `json:"function"`
}

func (c *ToolCallResponse) SetIndex(i int) {
	c.Index = &i
}

type FunctionResponse struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name,omitempty"`
	Parameters  any    `json:"parameters,omitempty"` // request
	Arguments   string `json:"arguments"`            // response
}

type ChatCompletionsStreamResponse struct {
	Id                string                                `json:"id"`
	Object            string                                `json:"object"`
	Created           int64                                 `json:"created"`
	Model             string                                `json:"model"`
	SystemFingerprint *string                               `json:"system_fingerprint"`
	Choices           []ChatCompletionsStreamResponseChoice `json:"choices"`
	Usage             *Usage                                `json:"usage"`
}

type OpenAIResponsesResponse struct {
	ID                 string             `json:"id"`
	Object             string             `json:"object"`
	CreatedAt          int                `json:"created_at"`
	Status             json.RawMessage    `json:"status"`
	Error              any                `json:"error,omitempty"`
	IncompleteDetails  *IncompleteDetails `json:"incomplete_details,omitempty"`
	Instructions       json.RawMessage    `json:"instructions"`
	MaxOutputTokens    int                `json:"max_output_tokens"`
	Model              string             `json:"model"`
	Output             []ResponsesOutput  `json:"output"`
	ParallelToolCalls  bool               `json:"parallel_tool_calls"`
	PreviousResponseID json.RawMessage    `json:"previous_response_id"`
	Reasoning          *Reasoning         `json:"reasoning"`
	Store              bool               `json:"store"`
	Temperature        float64            `json:"temperature"`
	ToolChoice         json.RawMessage    `json:"tool_choice"`
	Tools              []map[string]any   `json:"tools"`
	TopP               float64            `json:"top_p"`
	Truncation         json.RawMessage    `json:"truncation"`
	Usage              *Usage             `json:"usage"`
	User               json.RawMessage    `json:"user"`
	Metadata           json.RawMessage    `json:"metadata"`
}

type IncompleteDetails struct {
	Reason string `json:"reason"`
}

type ResponsesOutput struct {
	Type      string                   `json:"type"`
	ID        string                   `json:"id"`
	Status    string                   `json:"status"`
	Role      string                   `json:"role"`
	Content   []ResponsesOutputContent `json:"content"`
	Quality   string                   `json:"quality"`
	Size      string                   `json:"size"`
	Result    string                   `json:"result,omitempty"`
	CallId    string                   `json:"call_id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments json.RawMessage          `json:"arguments,omitempty"`
}

// ArgumentsString 返回 Chat Completions 期望的字符串形态 function arguments。
func (r *ResponsesOutput) ArgumentsString() string {
	if r == nil {
		return ""
	}
	return jsonRawMessageToString(r.Arguments)
}

type ResponsesOutputContent struct {
	Type        string        `json:"type"`
	Text        string        `json:"text"`
	Annotations []interface{} `json:"annotations"`
}

type ResponsesReasoningSummaryPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ResponsesStreamResponse 对应 /v1/responses 流式响应事件。
type ResponsesStreamResponse struct {
	Type     string                   `json:"type"`
	Response *OpenAIResponsesResponse `json:"response,omitempty"`
	Delta    string                   `json:"delta,omitempty"`
	Item     *ResponsesOutput         `json:"item,omitempty"`
	// - response.function_call_arguments.delta
	// - response.function_call_arguments.done
	OutputIndex  *int                           `json:"output_index,omitempty"`
	ContentIndex *int                           `json:"content_index,omitempty"`
	SummaryIndex *int                           `json:"summary_index,omitempty"`
	ItemID       string                         `json:"item_id,omitempty"`
	Part         *ResponsesReasoningSummaryPart `json:"part,omitempty"`
}

// Error 是上游返回的错误对象的最小子集。
type Error struct {
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
	Param   string `json:"param,omitempty"`
	Code    any    `json:"code,omitempty"`
}

// ExtractUpstreamError 尝试从任意 JSON 解出的 error 字段提取错误信息。
func ExtractUpstreamError(errorField any) *Error {
	if errorField == nil {
		return nil
	}
	switch err := errorField.(type) {
	case map[string]interface{}:
		openaiErr := &Error{}
		if errType, ok := err["type"].(string); ok {
			openaiErr.Type = errType
		}
		if errMsg, ok := err["message"].(string); ok {
			openaiErr.Message = errMsg
		}
		if errParam, ok := err["param"].(string); ok {
			openaiErr.Param = errParam
		}
		if errCode, ok := err["code"]; ok {
			openaiErr.Code = errCode
		}
		if openaiErr.Message != "" || openaiErr.Type != "" {
			return openaiErr
		}
		return nil
	case string:
		return &Error{Type: "error", Message: err}
	default:
		return nil
	}
}

func isQwenThinkingBudgetModel(modelName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	return strings.HasPrefix(normalized, "qwen") ||
		strings.Contains(normalized, "/qwen") ||
		strings.HasPrefix(normalized, "qwq") ||
		strings.Contains(normalized, "/qwq")
}
