// Portions of this file are derived from QuantumNous/new-api
// (https://github.com/QuantumNous/new-api), AGPL-3.0.
// Copyright (C) QuantumNous. Ported and adapted for llmproxy, 2026.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, version 3.

package convert

const (
	chatFinishReasonLength        = "length"
	chatFinishReasonContentFilter = "content_filter"

	responsesEventCreated                  = "response.created"
	responsesEventCompleted                = "response.completed"
	responsesEventDone                     = "response.done"
	responsesEventIncomplete               = "response.incomplete"
	responsesEventFailed                   = "response.failed"
	responsesEventError                    = "response.error"
	responsesEventOutputTextDelta          = "response.output_text.delta"
	responsesEventOutputItemAdded          = "response.output_item.added"
	responsesEventOutputItemDone           = "response.output_item.done"
	responsesEventFunctionArgsDelta        = "response.function_call_arguments.delta"
	responsesEventFunctionArgsDone         = "response.function_call_arguments.done"
	responsesEventCustomToolInputDelta     = "response.custom_tool_call_input.delta"
	responsesEventCustomToolInputDone      = "response.custom_tool_call_input.done"
	responsesEventReasoningSummaryDelta    = "response.reasoning_summary_text.delta"
	responsesEventReasoningSummaryDone     = "response.reasoning_summary_text.done"
	responsesEventReasoningTextDelta       = "response.reasoning_text.delta"
	responsesEventReasoningTextDone        = "response.reasoning_text.done"
	responsesOutputTypeFunctionCall        = "function_call"
	responsesOutputTypeCustomToolCall      = "custom_tool_call"
	responsesOutputTypeMessage             = "message"
	responsesOutputTypeReasoning           = "reasoning"
	responsesIncompleteReasonContentFilter = "content_filter"
	responsesIncompleteReasonMaxTokens     = "max_output_tokens"
)
