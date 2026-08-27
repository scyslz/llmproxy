// Portions of this file are derived from QuantumNous/new-api
// (https://github.com/QuantumNous/new-api), AGPL-3.0.
// Copyright (C) QuantumNous. Ported and adapted for llmproxy, 2026.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, version 3.

package convert

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func normalizeChatImageURLToString(v any) any {
	switch vv := v.(type) {
	case string:
		return vv
	case map[string]any:
		if url := interface2String(vv["url"]); url != "" {
			return url
		}
		return v
	case MessageImageUrl:
		if vv.Url != "" {
			return vv.Url
		}
		return v
	case *MessageImageUrl:
		if vv != nil && vv.Url != "" {
			return vv.Url
		}
		return v
	default:
		return v
	}
}

func convertChatResponseFormatToResponsesText(reqFormat *ResponseFormat) json.RawMessage {
	if reqFormat == nil || strings.TrimSpace(reqFormat.Type) == "" {
		return nil
	}

	format := map[string]any{
		"type": reqFormat.Type,
	}

	if reqFormat.Type == "json_schema" && len(reqFormat.JsonSchema) > 0 {
		var chatSchema map[string]any
		if err := unmarshalJSON(reqFormat.JsonSchema, &chatSchema); err == nil {
			for key, value := range chatSchema {
				if key == "type" {
					continue
				}
				format[key] = value
			}

			if nested, ok := format["json_schema"].(map[string]any); ok {
				for key, value := range nested {
					if _, exists := format[key]; !exists {
						format[key] = value
					}
				}
				delete(format, "json_schema")
			}
		} else {
			format["json_schema"] = reqFormat.JsonSchema
		}
	}

	textRaw, _ := marshalJSON(map[string]any{
		"format": format,
	})
	return textRaw
}

func ChatCompletionsRequestToResponsesRequest(req *GeneralOpenAIRequest) (*OpenAIResponsesRequest, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if req.Model == "" {
		return nil, errors.New("model is required")
	}
	if fromPtrOr(req.N, 1) > 1 {
		return nil, fmt.Errorf("n>1 is not supported in responses compatibility mode")
	}

	var instructionsParts []string
	inputItems := make([]map[string]any, 0, len(req.Messages))

	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			continue
		}

		if role == "tool" || role == "function" {
			callID := strings.TrimSpace(msg.ToolCallId)

			var output any
			if msg.Content == nil {
				output = ""
			} else if msg.IsStringContent() {
				output = msg.StringContent()
			} else {
				if b, err := marshalJSON(msg.Content); err == nil {
					output = string(b)
				} else {
					output = fmt.Sprintf("%v", msg.Content)
				}
			}

			if callID == "" {
				inputItems = append(inputItems, map[string]any{
					"role":    "user",
					"content": fmt.Sprintf("[tool_output_missing_call_id] %v", output),
				})
				continue
			}

			inputItems = append(inputItems, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  output,
			})
			continue
		}

		if role == "system" || role == "developer" {
			if msg.Content == nil {
				continue
			}
			if msg.IsStringContent() {
				if s := strings.TrimSpace(msg.StringContent()); s != "" {
					instructionsParts = append(instructionsParts, s)
				}
				continue
			}
			parts := msg.ParseContent()
			var sb strings.Builder
			for _, part := range parts {
				if part.Type == ContentTypeText && strings.TrimSpace(part.Text) != "" {
					if sb.Len() > 0 {
						sb.WriteString("\n")
					}
					sb.WriteString(part.Text)
				}
			}
			if s := strings.TrimSpace(sb.String()); s != "" {
				instructionsParts = append(instructionsParts, s)
			}
			continue
		}

		item := map[string]any{
			"role": role,
		}

		if msg.Content == nil {
			item["content"] = ""
			inputItems = append(inputItems, item)

			if role == "assistant" {
				for _, tc := range msg.ParseToolCalls() {
					if strings.TrimSpace(tc.ID) == "" {
						continue
					}
					if tc.Type != "" && tc.Type != "function" {
						continue
					}
					name := strings.TrimSpace(tc.Function.Name)
					if name == "" {
						continue
					}
					inputItems = append(inputItems, map[string]any{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      name,
						"arguments": tc.Function.Arguments,
					})
				}
			}
			continue
		}

		if msg.IsStringContent() {
			item["content"] = msg.StringContent()
			inputItems = append(inputItems, item)

			if role == "assistant" {
				for _, tc := range msg.ParseToolCalls() {
					if strings.TrimSpace(tc.ID) == "" {
						continue
					}
					if tc.Type != "" && tc.Type != "function" {
						continue
					}
					name := strings.TrimSpace(tc.Function.Name)
					if name == "" {
						continue
					}
					inputItems = append(inputItems, map[string]any{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      name,
						"arguments": tc.Function.Arguments,
					})
				}
			}
			continue
		}

		parts := msg.ParseContent()
		contentParts := make([]map[string]any, 0, len(parts))
		for _, part := range parts {
			switch part.Type {
			case ContentTypeText:
				textType := "input_text"
				if role == "assistant" {
					textType = "output_text"
				}
				contentParts = append(contentParts, map[string]any{
					"type": textType,
					"text": part.Text,
				})
			case ContentTypeImageURL:
				contentParts = append(contentParts, map[string]any{
					"type":      "input_image",
					"image_url": normalizeChatImageURLToString(part.ImageUrl),
				})
			case ContentTypeInputAudio:
				contentParts = append(contentParts, map[string]any{
					"type":        "input_audio",
					"input_audio": part.InputAudio,
				})
			case ContentTypeFile:
				contentParts = append(contentParts, map[string]any{
					"type": "input_file",
					"file": part.File,
				})
			case ContentTypeVideoUrl:
				contentParts = append(contentParts, map[string]any{
					"type":      "input_video",
					"video_url": part.VideoUrl,
				})
			default:
				contentParts = append(contentParts, map[string]any{
					"type": part.Type,
				})
			}
		}
		item["content"] = contentParts
		inputItems = append(inputItems, item)

		if role == "assistant" {
			for _, tc := range msg.ParseToolCalls() {
				if strings.TrimSpace(tc.ID) == "" {
					continue
				}
				if tc.Type != "" && tc.Type != "function" {
					continue
				}
				name := strings.TrimSpace(tc.Function.Name)
				if name == "" {
					continue
				}
				inputItems = append(inputItems, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      name,
					"arguments": tc.Function.Arguments,
				})
			}
		}
	}

	inputRaw, err := marshalJSON(inputItems)
	if err != nil {
		return nil, err
	}

	var instructionsRaw json.RawMessage
	if len(instructionsParts) > 0 {
		instructions := strings.Join(instructionsParts, "\n\n")
		instructionsRaw, _ = marshalJSON(instructions)
	}

	var toolsRaw json.RawMessage
	if req.Tools != nil {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			switch tool.Type {
			case "function":
				tools = append(tools, map[string]any{
					"type":        "function",
					"name":        tool.Function.Name,
					"description": tool.Function.Description,
					"parameters":  tool.Function.Parameters,
				})
			default:
				var m map[string]any
				if b, err := marshalJSON(tool); err == nil {
					_ = unmarshalJSON(b, &m)
				}
				if len(m) == 0 {
					m = map[string]any{"type": tool.Type}
				}
				tools = append(tools, m)
			}
		}
		toolsRaw, _ = marshalJSON(tools)
	}

	var toolChoiceRaw json.RawMessage
	if req.ToolChoice != nil {
		switch v := req.ToolChoice.(type) {
		case string:
			toolChoiceRaw, _ = marshalJSON(v)
		default:
			var m map[string]any
			if b, err := marshalJSON(v); err == nil {
				_ = unmarshalJSON(b, &m)
			}
			if m == nil {
				toolChoiceRaw, _ = marshalJSON(v)
			} else if t, _ := m["type"].(string); t == "function" {
				if name, ok := m["name"].(string); ok && name != "" {
					toolChoiceRaw, _ = marshalJSON(map[string]any{
						"type": "function",
						"name": name,
					})
				} else if fn, ok := m["function"].(map[string]any); ok {
					if name, ok := fn["name"].(string); ok && name != "" {
						toolChoiceRaw, _ = marshalJSON(map[string]any{
							"type": "function",
							"name": name,
						})
					} else {
						toolChoiceRaw, _ = marshalJSON(v)
					}
				} else {
					toolChoiceRaw, _ = marshalJSON(v)
				}
			} else {
				toolChoiceRaw, _ = marshalJSON(v)
			}
		}
	}

	var parallelToolCallsRaw json.RawMessage
	if req.ParallelTooCalls != nil {
		parallelToolCallsRaw, _ = marshalJSON(*req.ParallelTooCalls)
	}

	textRaw := convertChatResponseFormatToResponsesText(req.ResponseFormat)

	maxOutputTokens := fromPtrOr(req.MaxTokens, uint(0))
	maxCompletionTokens := fromPtrOr(req.MaxCompletionTokens, uint(0))
	if maxCompletionTokens > maxOutputTokens {
		maxOutputTokens = maxCompletionTokens
	}

	var topP *float64
	if req.TopP != nil {
		topP = getPointer(*req.TopP)
	}

	var frequencyPenaltyRaw, presencePenaltyRaw json.RawMessage
	if req.FrequencyPenalty != nil {
		frequencyPenaltyRaw, _ = marshalJSON(req.FrequencyPenalty)
	}
	if req.PresencePenalty != nil {
		presencePenaltyRaw, _ = marshalJSON(req.PresencePenalty)
	}

	var promptCacheKeyRaw json.RawMessage
	if req.PromptCacheKey != "" {
		promptCacheKeyRaw, err = marshalJSON(req.PromptCacheKey)
		if err != nil {
			return nil, fmt.Errorf("marshal prompt_cache_key: %w", err)
		}
	}

	out := &OpenAIResponsesRequest{
		Model:             req.Model,
		Input:             inputRaw,
		Instructions:      instructionsRaw,
		Stream:            req.Stream,
		Temperature:       req.Temperature,
		Text:              textRaw,
		ToolChoice:        toolChoiceRaw,
		Tools:             toolsRaw,
		TopP:              topP,
		FrequencyPenalty:  frequencyPenaltyRaw,
		PresencePenalty:   presencePenaltyRaw,
		User:              req.User,
		ParallelToolCalls: parallelToolCallsRaw,
		Store:             req.Store,
		Metadata:          req.Metadata,
		PromptCacheKey:    promptCacheKeyRaw,
		EnableThinking:    req.EnableThinking,
		ThinkingBudget:    req.ThinkingBudget,
	}
	if req.MaxTokens != nil || req.MaxCompletionTokens != nil {
		out.MaxOutputTokens = toPtr(maxOutputTokens)
	}

	if req.ReasoningEffort != "" {
		out.Reasoning = &Reasoning{
			Effort:  req.ReasoningEffort,
			Summary: "detailed",
		}
	}

	return out, nil
}
