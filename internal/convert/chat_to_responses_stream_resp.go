// Portions of this file are derived from QuantumNous/new-api
// (https://github.com/QuantumNous/new-api), AGPL-3.0.
// Copyright (C) QuantumNous. Ported and adapted for llmproxy, 2026.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, version 3.

package convert

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type ChatToResponsesStreamEvent struct {
	Type    string
	Payload ResponsesStreamResponse
}

type ChatToResponsesStreamState struct {
	ID      string
	Model   string
	Created int64
	Usage   *Usage

	status            string
	incompleteDetails *IncompleteDetails
	sentCreated       bool
	textOutputIndex   int
	textStarted       bool
	textDone          bool
	reasoningIndex    int
	reasoningStarted  bool
	reasoningDone     bool
	finalized         bool
	nextOutputIndex   int
	toolsByIndex      map[int]*chatToResponsesStreamTool
	outputOrder       []chatToResponsesOutputRef
	text              strings.Builder
	reasoning         strings.Builder
}

type chatToResponsesStreamTool struct {
	ChatIndex   int
	OutputIndex int
	ID          string
	Name        string
	Arguments   strings.Builder
	Done        bool
}

type chatToResponsesOutputRef struct {
	Kind      string
	ToolIndex int
}

func NewChatToResponsesStreamState(id string, model string) *ChatToResponsesStreamState {
	return &ChatToResponsesStreamState{
		ID:              id,
		Model:           model,
		Created:         time.Now().Unix(),
		Usage:           &Usage{},
		status:          "completed",
		textOutputIndex: -1,
		reasoningIndex:  -1,
		toolsByIndex:    make(map[int]*chatToResponsesStreamTool),
	}
}

func ChatCompletionsStreamChunkToResponsesEvents(chunk *ChatCompletionsStreamResponse, state *ChatToResponsesStreamState) ([]ChatToResponsesStreamEvent, error) {
	if chunk == nil || state == nil {
		return nil, nil
	}
	if state.ID == "" {
		state.ID = chunk.Id
	}
	if state.Model == "" {
		state.Model = chunk.Model
	}
	if state.Created == 0 {
		state.Created = chunk.Created
	}
	if chunk.Usage != nil {
		state.Usage = UsageFromChatUsage(chunk.Usage)
	}

	events := make([]ChatToResponsesStreamEvent, 0)
	if !state.sentCreated {
		state.sentCreated = true
		events = append(events, responsesStreamEvent(responsesEventCreated, ResponsesStreamResponse{
			Type:     responsesEventCreated,
			Response: state.createdResponse(),
		}))
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.GetReasoningContent() != "" {
			events = append(events, state.appendReasoningDelta(choice.Delta.GetReasoningContent())...)
		}
		if choice.Delta.GetContentString() != "" {
			events = append(events, state.appendTextDelta(choice.Delta.GetContentString())...)
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			toolEvents, err := state.appendToolCallDelta(toolCall)
			if err != nil {
				return nil, err
			}
			events = append(events, toolEvents...)
		}
		if choice.FinishReason != nil && strings.TrimSpace(*choice.FinishReason) != "" {
			state.applyFinishReason(*choice.FinishReason)
			events = append(events, state.doneDeltaEvents()...)
		}
	}
	return events, nil
}

func FinalizeChatCompletionsStreamToResponses(state *ChatToResponsesStreamState) []ChatToResponsesStreamEvent {
	if state == nil || state.finalized {
		return nil
	}
	events := state.doneDeltaEvents()
	state.finalized = true
	resp := state.finalResponse()
	eventType := responsesEventCompleted
	if state.status == "incomplete" {
		eventType = responsesEventIncomplete
	}
	events = append(events, responsesStreamEvent(eventType, ResponsesStreamResponse{
		Type:     eventType,
		Response: resp,
	}))
	return events
}

func (s *ChatToResponsesStreamState) UsageText() string {
	if s == nil {
		return ""
	}
	return s.text.String()
}

func (s *ChatToResponsesStreamState) appendTextDelta(delta string) []ChatToResponsesStreamEvent {
	events := make([]ChatToResponsesStreamEvent, 0, 2)
	if !s.textStarted {
		s.textStarted = true
		s.textOutputIndex = s.nextIndex("message", -1)
		events = append(events, responsesStreamEvent(responsesEventOutputItemAdded, ResponsesStreamResponse{
			Type:        responsesEventOutputItemAdded,
			OutputIndex: intPtr(s.textOutputIndex),
			Item: &ResponsesOutput{
				Type:    responsesOutputTypeMessage,
				ID:      s.messageID(),
				Status:  "in_progress",
				Role:    "assistant",
				Content: []ResponsesOutputContent{},
			},
		}))
	}
	s.text.WriteString(delta)
	events = append(events, responsesStreamEvent(responsesEventOutputTextDelta, ResponsesStreamResponse{
		Type:         responsesEventOutputTextDelta,
		OutputIndex:  intPtr(s.textOutputIndex),
		ContentIndex: intPtr(0),
		Delta:        delta,
		ItemID:       s.messageID(),
	}))
	return events
}

func (s *ChatToResponsesStreamState) appendReasoningDelta(delta string) []ChatToResponsesStreamEvent {
	events := make([]ChatToResponsesStreamEvent, 0, 2)
	if !s.reasoningStarted {
		s.reasoningStarted = true
		s.reasoningIndex = s.nextIndex("reasoning", -1)
		events = append(events, responsesStreamEvent(responsesEventOutputItemAdded, ResponsesStreamResponse{
			Type:        responsesEventOutputItemAdded,
			OutputIndex: intPtr(s.reasoningIndex),
			Item: &ResponsesOutput{
				Type:    responsesOutputTypeReasoning,
				ID:      s.reasoningID(),
				Status:  "in_progress",
				Content: []ResponsesOutputContent{},
			},
		}))
	}
	s.reasoning.WriteString(delta)
	events = append(events, responsesStreamEvent(responsesEventReasoningSummaryDelta, ResponsesStreamResponse{
		Type:         responsesEventReasoningSummaryDelta,
		OutputIndex:  intPtr(s.reasoningIndex),
		SummaryIndex: intPtr(0),
		Delta:        delta,
		ItemID:       s.reasoningID(),
	}))
	return events
}

func (s *ChatToResponsesStreamState) appendToolCallDelta(toolCall ToolCallResponse) ([]ChatToResponsesStreamEvent, error) {
	chatIndex := 0
	if toolCall.Index != nil {
		chatIndex = *toolCall.Index
	}
	tool := s.toolsByIndex[chatIndex]
	events := make([]ChatToResponsesStreamEvent, 0, 2)
	if tool == nil {
		tool = &chatToResponsesStreamTool{
			ChatIndex:   chatIndex,
			OutputIndex: s.nextIndex("tool", chatIndex),
			ID:          strings.TrimSpace(toolCall.ID),
			Name:        strings.TrimSpace(toolCall.Function.Name),
		}
		if tool.ID == "" {
			tool.ID = fmt.Sprintf("%s_call_%d", s.ID, chatIndex)
		}
		s.toolsByIndex[chatIndex] = tool
		events = append(events, responsesStreamEvent(responsesEventOutputItemAdded, ResponsesStreamResponse{
			Type:        responsesEventOutputItemAdded,
			OutputIndex: intPtr(tool.OutputIndex),
			ItemID:      tool.ID,
			Item: &ResponsesOutput{
				Type:      responsesOutputTypeFunctionCall,
				ID:        tool.ID,
				Status:    "in_progress",
				CallId:    tool.ID,
				Name:      tool.Name,
				Arguments: []byte(`""`),
			},
		}))
	}
	if strings.TrimSpace(toolCall.ID) != "" {
		tool.ID = strings.TrimSpace(toolCall.ID)
	}
	if strings.TrimSpace(toolCall.Function.Name) != "" {
		tool.Name = strings.TrimSpace(toolCall.Function.Name)
	}
	if toolCall.Function.Arguments != "" {
		tool.Arguments.WriteString(toolCall.Function.Arguments)
		events = append(events, responsesStreamEvent(responsesEventFunctionArgsDelta, ResponsesStreamResponse{
			Type:        responsesEventFunctionArgsDelta,
			OutputIndex: intPtr(tool.OutputIndex),
			ItemID:      tool.ID,
			Delta:       toolCall.Function.Arguments,
		}))
	}
	return events, nil
}

func (s *ChatToResponsesStreamState) doneDeltaEvents() []ChatToResponsesStreamEvent {
	events := make([]ChatToResponsesStreamEvent, 0)
	status := s.outputStatus()
	if s.textStarted && !s.textDone {
		s.textDone = true
		events = append(events, responsesStreamEvent("response.output_text.done", ResponsesStreamResponse{
			Type:         "response.output_text.done",
			OutputIndex:  intPtr(s.textOutputIndex),
			ContentIndex: intPtr(0),
			ItemID:       s.messageID(),
		}))
		events = append(events, responsesStreamEvent(responsesEventOutputItemDone, ResponsesStreamResponse{
			Type:        responsesEventOutputItemDone,
			OutputIndex: intPtr(s.textOutputIndex),
			Item:        s.messageOutput(status),
		}))
	}
	if s.reasoningStarted && !s.reasoningDone {
		s.reasoningDone = true
		events = append(events, responsesStreamEvent(responsesEventReasoningSummaryDone, ResponsesStreamResponse{
			Type:         responsesEventReasoningSummaryDone,
			OutputIndex:  intPtr(s.reasoningIndex),
			SummaryIndex: intPtr(0),
			ItemID:       s.reasoningID(),
			Part: &ResponsesReasoningSummaryPart{
				Type: "summary_text",
				Text: s.reasoning.String(),
			},
		}))
		events = append(events, responsesStreamEvent(responsesEventOutputItemDone, ResponsesStreamResponse{
			Type:        responsesEventOutputItemDone,
			OutputIndex: intPtr(s.reasoningIndex),
			Item:        s.reasoningOutput(status),
		}))
	}
	for _, tool := range s.sortedTools() {
		if tool.Done {
			continue
		}
		tool.Done = true
		events = append(events, responsesStreamEvent(responsesEventFunctionArgsDone, ResponsesStreamResponse{
			Type:        responsesEventFunctionArgsDone,
			OutputIndex: intPtr(tool.OutputIndex),
			ItemID:      tool.ID,
		}))
		events = append(events, responsesStreamEvent(responsesEventOutputItemDone, ResponsesStreamResponse{
			Type:        responsesEventOutputItemDone,
			OutputIndex: intPtr(tool.OutputIndex),
			Item:        s.toolOutput(tool, status),
		}))
	}
	return events
}

func (s *ChatToResponsesStreamState) applyFinishReason(finishReason string) {
	if status, details := ResponsesStatusFromChatFinishReason(finishReason); status != "" {
		s.status = status
		s.incompleteDetails = details
	}
}

func (s *ChatToResponsesStreamState) finalResponse() *OpenAIResponsesResponse {
	output := make([]ResponsesOutput, 0, len(s.outputOrder))
	status := s.outputStatus()
	for _, ref := range s.outputOrder {
		switch ref.Kind {
		case "message":
			output = append(output, *s.messageOutput(status))
		case "reasoning":
			output = append(output, *s.reasoningOutput(status))
		case "tool":
			if tool := s.toolsByIndex[ref.ToolIndex]; tool != nil {
				output = append(output, *s.toolOutput(tool, status))
			}
		}
	}
	return &OpenAIResponsesResponse{
		ID:                s.ID,
		Object:            "response",
		CreatedAt:         int(s.Created),
		Status:            []byte(fmt.Sprintf("%q", s.status)),
		IncompleteDetails: s.incompleteDetails,
		Model:             s.Model,
		Output:            output,
		Usage:             s.Usage,
	}
}

func (s *ChatToResponsesStreamState) createdResponse() *OpenAIResponsesResponse {
	return &OpenAIResponsesResponse{
		ID:        s.ID,
		Object:    "response",
		CreatedAt: int(s.Created),
		Status:    []byte(`"in_progress"`),
		Model:     s.Model,
		Output:    []ResponsesOutput{},
	}
}

func (s *ChatToResponsesStreamState) nextIndex(kind string, toolIndex int) int {
	index := s.nextOutputIndex
	s.nextOutputIndex++
	s.outputOrder = append(s.outputOrder, chatToResponsesOutputRef{Kind: kind, ToolIndex: toolIndex})
	return index
}

func (s *ChatToResponsesStreamState) sortedTools() []*chatToResponsesStreamTool {
	indexes := make([]int, 0, len(s.toolsByIndex))
	for index := range s.toolsByIndex {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	tools := make([]*chatToResponsesStreamTool, 0, len(indexes))
	for _, index := range indexes {
		tools = append(tools, s.toolsByIndex[index])
	}
	return tools
}

func (s *ChatToResponsesStreamState) outputStatus() string {
	if s.status == "incomplete" {
		return "incomplete"
	}
	return "completed"
}

func (s *ChatToResponsesStreamState) messageID() string {
	return fmt.Sprintf("%s_msg_0", s.ID)
}

func (s *ChatToResponsesStreamState) reasoningID() string {
	return fmt.Sprintf("%s_reasoning_0", s.ID)
}

func (s *ChatToResponsesStreamState) messageOutput(status string) *ResponsesOutput {
	return &ResponsesOutput{
		Type:   responsesOutputTypeMessage,
		ID:     s.messageID(),
		Status: status,
		Role:   "assistant",
		Content: []ResponsesOutputContent{
			{
				Type:        "output_text",
				Text:        s.text.String(),
				Annotations: []interface{}{},
			},
		},
	}
}

func (s *ChatToResponsesStreamState) reasoningOutput(status string) *ResponsesOutput {
	return &ResponsesOutput{
		Type:   responsesOutputTypeReasoning,
		ID:     s.reasoningID(),
		Status: status,
		Content: []ResponsesOutputContent{
			{
				Type: "summary_text",
				Text: s.reasoning.String(),
			},
		},
	}
}

func (s *ChatToResponsesStreamState) toolOutput(tool *chatToResponsesStreamTool, status string) *ResponsesOutput {
	return &ResponsesOutput{
		Type:      responsesOutputTypeFunctionCall,
		ID:        tool.ID,
		Status:    status,
		CallId:    tool.ID,
		Name:      tool.Name,
		Arguments: chatArgumentsRawMessage(tool.Arguments.String()),
	}
}
