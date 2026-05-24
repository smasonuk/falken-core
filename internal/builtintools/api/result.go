package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/smasonuk/falken-core/internal/agent"
)

// Success builds a successful result, marshalling payload to JSON.
func Success(content string, payload any) (agent.ToolExecutionResult, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Fail("encode_error", "failed to encode result: "+err.Error()), nil
	}
	return agent.ToolExecutionResult{
		Success: true,
		Status:  "ok",
		Content: content,
		Payload: data,
	}, nil
}

// ResultFromPayload builds a result where success is determined by the caller.
func ResultFromPayload(success bool, status, content string, payload any) (agent.ToolExecutionResult, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Fail("encode_error", "failed to encode result: "+err.Error()), nil
	}
	errorText := ""
	if !success {
		errorText = payloadErrorString(data, content)
	}
	return agent.ToolExecutionResult{
		Success: success,
		Status:  status,
		Content: content,
		Payload: data,
		Error:   errorText,
	}, nil
}

// Fail builds a failed result with a simple error message.
// Exported so the falken package can use it for dispatch-level errors.
func Fail(status, message string) agent.ToolExecutionResult {
	payload, _ := json.Marshal(map[string]any{
		"success": false,
		"status":  status,
		"error":   message,
	})
	return agent.ToolExecutionResult{
		Success: false,
		Status:  status,
		Content: message,
		Payload: payload,
		Error:   message,
	}
}

// DecodeArgs unmarshals raw JSON tool arguments into target.
// An empty or nil args slice is treated as an empty object.
func DecodeArgs(args json.RawMessage, target any) error {
	trimmed := strings.TrimSpace(string(args))
	switch trimmed {
	case "":
		args = json.RawMessage(`{}`)
	case "null":
		return errors.New("arguments must be a JSON object, not null")
	}
	return DecodeStrictJSON(args, target)
}

// DecodeStrictJSON unmarshals one JSON value into target while rejecting unknown fields.
func DecodeStrictJSON(args json.RawMessage, target any) error {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()

	if err := dec.Decode(target); err != nil {
		return err
	}

	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("arguments must contain exactly one JSON value")
	}

	return nil
}

func payloadErrorString(data []byte, fallback string) string {
	var decoded map[string]any
	if json.Unmarshal(data, &decoded) != nil {
		return fallback
	}
	if v, ok := decoded["error"].(string); ok && v != "" {
		return v
	}
	return fallback
}
