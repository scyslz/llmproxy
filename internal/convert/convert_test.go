// Derived from QuantumNous/new-api relayconvert tests (AGPL-3.0, Copyright QuantumNous).
// Ported and adapted for llmproxy, 2026.

package convert

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestChatRequestToResponsesAndBack(t *testing.T) {
	req := &GeneralOpenAIRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		},
		Tools: []ToolCallRequest{
			{
				Type:     "function",
				Function: FunctionRequest{Name: "get_weather", Description: "get weather", Parameters: map[string]any{"type": "object"}},
			},
		},
		Temperature: toPtr(0.5),
	}

	responsesReq, err := ChatCompletionsRequestToResponsesRequest(req)
	if err != nil {
		t.Fatalf("to responses: %v", err)
	}
	if responsesReq.Model != "gpt-4o" {
		t.Errorf("model mismatch: %s", responsesReq.Model)
	}
	// system message should land in instructions
	if !strings.Contains(string(responsesReq.Instructions), "You are helpful.") {
		t.Errorf("instructions missing system text: %s", responsesReq.Instructions)
	}
	// tools preserved
	if len(responsesReq.Tools) == 0 {
		t.Errorf("tools not preserved")
	}

	back, err := ResponsesRequestToChatCompletionsRequest(responsesReq)
	if err != nil {
		t.Fatalf("back to chat: %v", err)
	}
	if back.Model != "gpt-4o" {
		t.Errorf("round-trip model mismatch: %s", back.Model)
	}
	if len(back.Messages) < 2 {
		t.Errorf("round-trip lost messages: %d", len(back.Messages))
	}
	if len(back.Tools) != 1 || back.Tools[0].Function.Name != "get_weather" {
		t.Errorf("round-trip tools broken: %+v", back.Tools)
	}
}

func TestChatResponseToResponsesAndBack(t *testing.T) {
	chatResp := &OpenAITextResponse{
		Id:      "chatcmpl-1",
		Model:   "gpt-4o",
		Object:  "chat.completion",
		Created: 1700000000,
		Choices: []OpenAITextResponseChoice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: "The answer is 42"},
				FinishReason: "stop",
			},
		},
		Usage: Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	responsesResp, usage, err := ChatCompletionsResponseToResponsesResponse(chatResp, "resp-1")
	if err != nil {
		t.Fatalf("to responses: %v", err)
	}
	if usage == nil || usage.PromptTokens != 10 || usage.OutputTokens != 5 {
		t.Errorf("usage mapping wrong: %+v", usage)
	}
	if len(responsesResp.Output) != 1 || responsesResp.Output[0].Type != responsesOutputTypeMessage {
		t.Fatalf("output type wrong: %+v", responsesResp.Output)
	}
	text := ExtractOutputTextFromResponses(responsesResp)
	if text != "The answer is 42" {
		t.Errorf("extracted text wrong: %q", text)
	}

	back, _, err := ResponsesResponseToChatCompletionsResponse(responsesResp, "chatcmpl-2")
	if err != nil {
		t.Fatalf("back to chat: %v", err)
	}
	if back.Choices[0].Message.StringContent() != "The answer is 42" {
		t.Errorf("round-trip text wrong: %q", back.Choices[0].Message.StringContent())
	}
	if back.Usage.TotalTokens != 15 {
		t.Errorf("round-trip usage wrong: %+v", back.Usage)
	}
}

func TestChatStreamToResponses(t *testing.T) {
	state := NewChatToResponsesStreamState("s1", "gpt-4o")
	var events []ChatToResponsesStreamEvent

	chunk := &ChatCompletionsStreamResponse{
		Id:      "s1",
		Model:   "gpt-4o",
		Created: 1700000000,
		Choices: []ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: ChatCompletionsStreamResponseChoiceDelta{Role: "assistant", Content: toPtr("Hel")}},
		},
	}
	ev, err := ChatCompletionsStreamChunkToResponsesEvents(chunk, state)
	if err != nil {
		t.Fatalf("chunk1: %v", err)
	}
	events = append(events, ev...)

	chunk2 := &ChatCompletionsStreamResponse{
		Id:    "s1",
		Model: "gpt-4o",
		Choices: []ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: ChatCompletionsStreamResponseChoiceDelta{Content: toPtr("lo")}},
		},
	}
	ev, _ = ChatCompletionsStreamChunkToResponsesEvents(chunk2, state)
	events = append(events, ev...)

	events = append(events, FinalizeChatCompletionsStreamToResponses(state)...)

	if len(events) == 0 {
		t.Fatal("no events produced")
	}
	// the buffered text must contain the full content
	if !strings.Contains(state.UsageText(), "Hello") {
		t.Errorf("stream text missing: %q", state.UsageText())
	}
}

func TestResponsesStreamToChat(t *testing.T) {
	state := NewResponsesToChatStreamState("r1", true)
	var chunks []ChatCompletionsStreamResponse

	created := &ResponsesStreamResponse{
		Type:     responsesEventCreated,
		Response: &OpenAIResponsesResponse{ID: "r1", Model: "gpt-4o", CreatedAt: 1700000000, Usage: &Usage{PromptTokens: 3}},
	}
	c, err := ResponsesStreamEventToChatChunks(created, state)
	if err != nil {
		t.Fatalf("created: %v", err)
	}
	chunks = append(chunks, c...)

	delta := &ResponsesStreamResponse{
		Type: responsesEventOutputTextDelta,
		Delta: "Hi",
	}
	c, _ = ResponsesStreamEventToChatChunks(delta, state)
	chunks = append(chunks, c...)

	completed := &ResponsesStreamResponse{
		Type:     responsesEventCompleted,
		Response: &OpenAIResponsesResponse{ID: "r1", Model: "gpt-4o", CreatedAt: 1700000000, Usage: &Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}},
	}
	c, _ = ResponsesStreamEventToChatChunks(completed, state)
	chunks = append(chunks, c...)
	chunks = append(chunks, FinalizeResponsesToChatStream(state)...)

	var sb strings.Builder
	var usage *Usage
	for _, ch := range chunks {
		if ch.Usage != nil {
			usage = ch.Usage
		}
		for _, choice := range ch.Choices {
			if choice.Delta.Content != nil {
				sb.WriteString(*choice.Delta.Content)
			}
		}
	}
	if sb.String() != "Hi" {
		t.Errorf("stream text wrong: %q", sb.String())
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Errorf("stream usage wrong: %+v", usage)
	}
}

func TestResponsesRequestRejectsStatefulFields(t *testing.T) {
	req := &OpenAIResponsesRequest{
		Model:              "gpt-4o",
		PreviousResponseID: "resp-prev",
		Input:              mustJSON(t, "hi"),
	}
	if err := ValidateRequestChatUnsupportedFields(req); err == nil {
		t.Errorf("expected error for previous_response_id")
	}
}
