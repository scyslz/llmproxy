// Package convert implements automatic protocol conversion between OpenAI
// Chat Completions (/v1/chat/completions) and OpenAI Responses (/v1/responses).
//
// The conversion core is derived from QuantumNous/new-api
// (https://github.com/QuantumNous/new-api), licensed under AGPL-3.0.
// Copyright (C) QuantumNous. It has been adapted for llmproxy's stdlib-only,
// gin-free architecture. Distributed under AGPL-3.0 accordingly.
package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

func unmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func marshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// getJsonType classifies a raw JSON value: object|array|string|boolean|null|number|unknown.
func getJsonType(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return "unknown"
	}
	switch trimmed[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}

// jsonRawMessageToString returns JSON strings as their decoded value and other JSON values as raw text.
func jsonRawMessageToString(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] != '"' {
		return string(trimmed)
	}
	var value string
	if err := unmarshalJSON(trimmed, &value); err != nil {
		return string(trimmed)
	}
	return value
}

func interface2String(inter interface{}) string {
	switch v := inter.(type) {
	case string:
		return v
	case int:
		return fmt.Sprintf("%d", v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", inter)
	}
}

func string2Int(str string) int {
	num, err := strconv.Atoi(str)
	if err != nil {
		return 0
	}
	return num
}

// fromPtrOr 返回指针指向的值，指针为 nil 时返回默认值。
func fromPtrOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// toPtr 返回指向 v 的指针。
func toPtr[T any](v T) *T {
	return &v
}

// getPointer 与 fromPtrOr 组合：指针非空时取其地址。
func getPointer[T any](v T) *T {
	return &v
}
