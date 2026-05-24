package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunnerFailureJSONFingerprintIgnoresWhitespace(t *testing.T) {
	a := jsonFingerprint(json.RawMessage(`{"path":"foo.go","range":{"start":1,"end":2}}`))
	b := jsonFingerprint(json.RawMessage(`{
		"path": "foo.go",
		"range": {
			"start": 1,
			"end": 2
		}
	}`))

	if a != b {
		t.Fatalf("fingerprints differ for whitespace-equivalent JSON: %q vs %q", a, b)
	}
}

func TestRunnerFailureTrackerRecordIncrementsSameFailure(t *testing.T) {
	tracker := newRepeatedFailureTracker()
	call := ToolCall{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"missing.go"}`)}
	result := runnerFailureResult("read_file", "not_found", "file not found")

	first := tracker.Record(call, result)
	second := tracker.Record(call, result)

	if first.Count != 1 || second.Count != 2 {
		t.Fatalf("counts = %d then %d, want 1 then 2", first.Count, second.Count)
	}
}

func TestRunnerFailureTrackerRecordResetsAfterSuccess(t *testing.T) {
	tracker := newRepeatedFailureTracker()
	call := ToolCall{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"missing.go"}`)}
	failure := runnerFailureResult("read_file", "not_found", "file not found")
	success := ToolResult{
		CallID:  "call-2",
		Name:    "read_file",
		Payload: json.RawMessage(`{"success":true,"status":"ok"}`),
	}

	if got := tracker.Record(call, failure); got.Count != 1 {
		t.Fatalf("first failure count = %d, want 1", got.Count)
	}
	if got := tracker.Record(call, success); got.Count != 0 {
		t.Fatalf("success count = %d, want 0", got.Count)
	}
	if got := tracker.Record(call, failure); got.Count != 1 {
		t.Fatalf("failure after reset count = %d, want 1", got.Count)
	}
}

func TestRunnerFailureWarningUpdatesResultFields(t *testing.T) {
	result := withRepeatedFailureWarning(runnerFailureResult("read_file", "not_found", "file not found"))

	if !strings.Contains(result.Content, "failed repeatedly") {
		t.Fatalf("content = %q, want repeated failure warning", result.Content)
	}
	if !strings.Contains(result.Error, "failed repeatedly") {
		t.Fatalf("error = %q, want repeated failure warning", result.Error)
	}

	var payload map[string]any
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["repeated_failure_warning"] == "" {
		t.Fatalf("payload = %+v, want repeated_failure_warning", payload)
	}
}

func runnerFailureResult(name, status, errorText string) ToolResult {
	return ToolResult{
		CallID:  "call-1",
		Name:    name,
		Content: errorText,
		Payload: json.RawMessage(`{"success":false,"status":"` + status + `","error":"` + errorText + `"}`),
		Error:   errorText,
	}
}
