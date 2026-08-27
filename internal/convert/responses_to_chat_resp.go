// Portions of this file are derived from QuantumNous/new-api
// (https://github.com/QuantumNous/new-api), AGPL-3.0.
// Copyright (C) QuantumNous. Ported and adapted for llmproxy, 2026.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, version 3.

package convert

import (
	"errors"
	"fmt"
	"strings"
)

func ResponsesFinishReasonFromStatus(resp *OpenAIResponsesResponse) (string, bool) {
	if resp == nil {
		return "", false
	}

	status := responseStatusString(resp)
	if status != "incomplete" {
		return "", false
	}

	reason := ""
	if resp.IncompleteDetails != nil {
		reason = strings.TrimSpace(resp.IncompleteDetails.Reason)
	}
	if reason == responsesIncompleteReasonContentFilter {
		return "content_filter", true
	}
	return "length", true
}

func ResponsesResponseToChatCompletionsResponse(resp *OpenAIResponsesResponse, id string) (*OpenAITextResponse, *Usage, error) {
	if resp == nil {
		return nil, nil, errors.New("response is nil")
	}

	text := ExtractOutputTextFromResponses(resp)
	reasoning := ExtractReasoningTextFromResponses(resp)

	usage := UsageFromResponsesUsage(resp.Usage)

	created := resp.CreatedAt

	var toolCalls []ToolCallResponse
	if len(resp.Output) > 0 {
		for _, out := range resp.Output {
			if !isResponsesToolOutputType(out.Type) {
				continue
			}
			name := strings.TrimSpace(out.Name)
			if name == "" {
				continue
			}
			callId := strings.TrimSpace(out.CallId)
			if callId == "" {
				callId = strings.TrimSpace(out.ID)
			}
			toolCalls = append(toolCalls, ToolCallResponse{
				ID:   callId,
				Type: "function",
				Function: FunctionResponse{
					Name:      name,
					Arguments: out.ArgumentsString(),
				},
			})
		}
	}

	finishReason := "stop"
	if mappedReason, ok := ResponsesFinishReasonFromStatus(resp); ok {
		finishReason = mappedReason
	} else if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	msg := Message{
		Role:    "assistant",
		Content: text,
	}
	if reasoning != "" {
		msg.ReasoningContent = &reasoning
	}
	if len(toolCalls) > 0 {
		msg.SetToolCalls(toolCalls)
	}

	out := &OpenAITextResponse{
		Id:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   resp.Model,
		Choices: []OpenAITextResponseChoice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: finishReason,
			},
		},
		Usage: *usage,
	}

	return out, usage, nil
}

func UsageFromResponsesUsage(src *Usage) *Usage {
	usage := &Usage{}
	if src == nil {
		return usage
	}
	if src.InputTokens != 0 {
		usage.PromptTokens = src.InputTokens
		usage.InputTokens = src.InputTokens
	}
	if src.OutputTokens != 0 {
		usage.CompletionTokens = src.OutputTokens
		usage.OutputTokens = src.OutputTokens
	}
	if src.TotalTokens != 0 {
		usage.TotalTokens = src.TotalTokens
	} else {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if src.InputTokensDetails != nil {
		usage.PromptTokensDetails.CachedTokens = src.InputTokensDetails.CachedTokens
		usage.PromptTokensDetails.CachedCreationTokens = src.InputTokensDetails.CachedCreationTokens
		usage.PromptTokensDetails.CacheWriteTokens = src.InputTokensDetails.CacheWriteTokens
		usage.PromptTokensDetails.TextTokens = src.InputTokensDetails.TextTokens
		usage.PromptTokensDetails.ImageTokens = src.InputTokensDetails.ImageTokens
		usage.PromptTokensDetails.AudioTokens = src.InputTokensDetails.AudioTokens
	}
	if src.CompletionTokenDetails.ReasoningTokens != 0 ||
		src.CompletionTokenDetails.TextTokens != 0 ||
		src.CompletionTokenDetails.AudioTokens != 0 ||
		src.CompletionTokenDetails.ImageTokens != 0 {
		usage.CompletionTokenDetails.ReasoningTokens = src.CompletionTokenDetails.ReasoningTokens
		usage.CompletionTokenDetails.TextTokens = src.CompletionTokenDetails.TextTokens
		usage.CompletionTokenDetails.AudioTokens = src.CompletionTokenDetails.AudioTokens
		usage.CompletionTokenDetails.ImageTokens = src.CompletionTokenDetails.ImageTokens
	}
	return usage
}

func ExtractOutputTextFromResponses(resp *OpenAIResponsesResponse) string {
	if resp == nil || len(resp.Output) == 0 {
		return ""
	}

	var sb strings.Builder

	for _, out := range resp.Output {
		if out.Type != "message" {
			continue
		}
		if out.Role != "" && out.Role != "assistant" {
			continue
		}
		for _, c := range out.Content {
			if c.Type == "output_text" && c.Text != "" {
				sb.WriteString(c.Text)
			}
		}
	}
	if sb.Len() > 0 {
		return sb.String()
	}
	for _, out := range resp.Output {
		for _, c := range out.Content {
			if c.Text != "" {
				sb.WriteString(c.Text)
			}
		}
	}
	return sb.String()
}

func ExtractReasoningTextFromResponses(resp *OpenAIResponsesResponse) string {
	if resp == nil || len(resp.Output) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, out := range resp.Output {
		if out.Type != responsesOutputTypeReasoning {
			continue
		}
		for _, c := range out.Content {
			if c.Text != "" {
				sb.WriteString(c.Text)
			}
		}
	}
	return sb.String()
}

func responseStatusString(resp *OpenAIResponsesResponse) string {
	if resp == nil || len(resp.Status) == 0 {
		return ""
	}
	var status string
	_ = unmarshalJSON(resp.Status, &status)
	return strings.TrimSpace(status)
}

func ensureIncompleteResponse(resp *OpenAIResponsesResponse) *OpenAIResponsesResponse {
	if resp == nil {
		resp = &OpenAIResponsesResponse{}
	}
	if len(resp.Status) == 0 {
		resp.Status = []byte(`"incomplete"`)
	}
	return resp
}

func isResponsesToolOutputType(outputType string) bool {
	return outputType == responsesOutputTypeFunctionCall || outputType == responsesOutputTypeCustomToolCall
}

func responseStreamEventItemID(event *ResponsesStreamResponse) string {
	if event == nil {
		return ""
	}
	if event.Item != nil {
		if itemID := strings.TrimSpace(event.Item.ID); itemID != "" {
			return itemID
		}
	}
	return strings.TrimSpace(event.ItemID)
}

func fallbackToolKey(itemID string, callID string, outputIndex *int) string {
	if outputIndex != nil {
		return fmt.Sprintf("output:%d", *outputIndex)
	}
	if strings.TrimSpace(itemID) != "" {
		return "item:" + strings.TrimSpace(itemID)
	}
	if strings.TrimSpace(callID) != "" {
		return "call:" + strings.TrimSpace(callID)
	}
	return ""
}

func fallbackCallID(event *ResponsesStreamResponse) string {
	if event == nil {
		return ""
	}
	if strings.TrimSpace(event.ItemID) != "" {
		return strings.TrimSpace(event.ItemID)
	}
	if event.OutputIndex != nil {
		return fmt.Sprintf("call_output_%d", *event.OutputIndex)
	}
	return ""
}
