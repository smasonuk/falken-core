package builtintools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFailHasConsistentFailureShape(t *testing.T) {
	result := Fail("invalid_arguments", "bad args")
	if result.Success {
		t.Fatalf("Success = true, want false")
	}
	if result.Status != "invalid_arguments" || result.Content != "bad args" || result.Error != "bad args" {
		t.Fatalf("result = %+v, want consistent status/content/error", result)
	}

	var payload map[string]any
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["success"] != false || payload["status"] != "invalid_arguments" || payload["error"] != "bad args" {
		t.Fatalf("payload = %+v, want failure fields", payload)
	}
}

func TestSuccessPayloadEncodeErrorReturnsFailureShape(t *testing.T) {
	result, err := Success("", map[string]any{"bad": func() {}})
	if err != nil {
		t.Fatalf("Success returned error = %v, want encoded failure result", err)
	}
	assertEncodeFailureResult(t, result.Status, result.Content, result.Error, result.Payload)
}

func TestResultFromPayloadEncodeErrorReturnsFailureShape(t *testing.T) {
	result, err := ResultFromPayload(true, "ok", "", map[string]any{"bad": func() {}})
	if err != nil {
		t.Fatalf("ResultFromPayload returned error = %v, want encoded failure result", err)
	}
	assertEncodeFailureResult(t, result.Status, result.Content, result.Error, result.Payload)
}

func TestDecodeArgsRejectsNull(t *testing.T) {
	var target struct{}
	if err := DecodeArgs(nil, &target); err != nil {
		t.Fatalf("DecodeArgs nil: %v", err)
	}
	if err := DecodeArgs(json.RawMessage(`{}`), &target); err != nil {
		t.Fatalf("DecodeArgs object: %v", err)
	}
	if err := DecodeArgs(json.RawMessage(`null`), &target); err == nil || !strings.Contains(err.Error(), "not null") {
		t.Fatalf("DecodeArgs null error = %v, want not-null rejection", err)
	}
}

func assertEncodeFailureResult(t *testing.T, status, content, errorText string, payload []byte) {
	t.Helper()
	if status != "encode_error" {
		t.Fatalf("status = %q, want encode_error", status)
	}
	if !strings.Contains(content, "failed to encode result") || errorText != content {
		t.Fatalf("content/error = %q/%q, want matching encode error", content, errorText)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if decoded["success"] != false || decoded["status"] != "encode_error" || decoded["error"] != content {
		t.Fatalf("payload = %+v, want encode failure fields", decoded)
	}
}
