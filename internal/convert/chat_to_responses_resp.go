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
	"time"
)

func ChatCompletionsResponseToResponsesResponse(resp *OpenAITextResponse, id string) (*OpenAIResponsesResponse, *Usage, error) {
	if resp == nil {
		return nil, nil, errors.New("response is nil")
	}

	usage := UsageFromChatUsage(&resp.Usage)
	out := &OpenAIResponsesResponse{
		ID:        id,
		Object:    "response",
		CreatedAt: chatCreatedAt(resp.Created),
		Status:    []byte(`"completed"`),
		Model:     resp.Model,
		Output:    make([]ResponsesOutput, 0),
		Usage:     usage,
	}

	if len(resp.Choices) == 0 {
		return out, usage, nil
	}

	choice := resp.Choices[0]
	if status, details := ResponsesStatusFromChatFinishReason(choice.FinishReason); status != "" {
		out.Status = []byte(fmt.Sprintf("%q", status))
		out.IncompleteDetails = details
	}

	if text := choice.Message.StringContent(); text != "" {
		out.Output = append(out.Output, ResponsesOutput{
			Type:   responsesOutputTypeMessage,
			ID:     fmt.Sprintf("%s_msg_0", id),
			Status: responseOutputStatus(out),
			Role:   "assistant",
			Content: []ResponsesOutputContent{
				{
					Type:        "output_text",
					Text:        text,
					Annotations: []interface{}{},
				},
			},
		})
	}
	if reasoning := choice.Message.GetReasoningContent(); reasoning != "" {
		out.Output = append(out.Output, ResponsesOutput{
			Type:   responsesOutputTypeReasoning,
			ID:     fmt.Sprintf("%s_reasoning_0", id),
			Status: responseOutputStatus(out),
			Content: []ResponsesOutputContent{
				{
					Type: "summary_text",
					Text: reasoning,
				},
			},
		})
	}

	for i, toolCall := range choice.Message.ParseToolCalls() {
		toolOutput, err := chatToolCallToResponsesOutput(toolCall, id, i, responseOutputStatus(out))
		if err != nil {
			return nil, nil, err
		}
		out.Output = append(out.Output, toolOutput)
	}

	return out, usage, nil
}

func ResponsesStatusFromChatFinishReason(finishReason string) (string, *IncompleteDetails) {
	switch strings.TrimSpace(finishReason) {
	case chatFinishReasonLength:
		return "incomplete", &IncompleteDetails{Reason: responsesIncompleteReasonMaxTokens}
	case chatFinishReasonContentFilter:
		return "incomplete", &IncompleteDetails{Reason: responsesIncompleteReasonContentFilter}
	default:
		return "completed", nil
	}
}

func UsageFromChatUsage(src *Usage) *Usage {
	usage := &Usage{}
	if src == nil {
		return usage
	}
	if src.PromptTokens != 0 {
		usage.PromptTokens = src.PromptTokens
		usage.InputTokens = src.PromptTokens
	}
	if src.CompletionTokens != 0 {
		usage.CompletionTokens = src.CompletionTokens
		usage.OutputTokens = src.CompletionTokens
	}
	if src.TotalTokens != 0 {
		usage.TotalTokens = src.TotalTokens
	} else {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if src.PromptTokensDetails.CachedTokens != 0 ||
		src.PromptTokensDetails.ImageTokens != 0 ||
		src.PromptTokensDetails.AudioTokens != 0 ||
		src.PromptTokensDetails.CachedCreationTokens != 0 ||
		src.PromptTokensDetails.CacheWriteTokens != 0 ||
		src.PromptTokensDetails.TextTokens != 0 {
		details := src.PromptTokensDetails
		usage.InputTokensDetails = &details
	}
	if src.CompletionTokenDetails.ReasoningTokens != 0 ||
		src.CompletionTokenDetails.TextTokens != 0 ||
		src.CompletionTokenDetails.AudioTokens != 0 ||
		src.CompletionTokenDetails.ImageTokens != 0 {
		usage.CompletionTokenDetails = src.CompletionTokenDetails
	}
	return usage
}

func responseOutputStatus(resp *OpenAIResponsesResponse) string {
	if resp == nil || responseStatusString(resp) != "incomplete" {
		return "completed"
	}
	return "incomplete"
}

func chatToolCallToResponsesOutput(toolCall ToolCallRequest, responseID string, index int, status string) (ResponsesOutput, error) {
	callID := strings.TrimSpace(toolCall.ID)
	if callID == "" {
		callID = fmt.Sprintf("%s_call_%d", responseID, index)
	}
	if toolCall.Type == "" || toolCall.Type == "function" {
		return ResponsesOutput{
			Type:      responsesOutputTypeFunctionCall,
			ID:        callID,
			Status:    status,
			CallId:    callID,
			Name:      toolCall.Function.Name,
			Arguments: chatArgumentsRawMessage(toolCall.Function.Arguments),
		}, nil
	}
	return ResponsesOutput{
		Type:      toolCall.Type,
		ID:        callID,
		Status:    status,
		CallId:    callID,
		Arguments: toolCall.Custom,
	}, nil
}

func chatArgumentsRawMessage(arguments string) []byte {
	raw, err := marshalJSON(arguments)
	if err != nil {
		return []byte(`""`)
	}
	return raw
}

func chatCreatedAt(created any) int {
	switch v := created.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case string:
		if parsed := string2Int(v); parsed != 0 {
			return parsed
		}
	}
	return int(time.Now().Unix())
}

func responsesStreamEvent(eventType string, payload ResponsesStreamResponse) ChatToResponsesStreamEvent {
	payload.Type = eventType
	return ChatToResponsesStreamEvent{
		Type:    eventType,
		Payload: payload,
	}
}

func intPtr(v int) *int {
	return &v
}
