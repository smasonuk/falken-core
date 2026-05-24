package agent

import (
	"encoding/json"
	"testing"
)

func testPayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

func testFailedResult(name, status, errText string) ToolResult {
	return ToolResult{
		CallID:  "call-1",
		Name:    name,
		Content: errText,
		Payload: json.RawMessage(`{"success":false,"status":"` + status + `","error":"` + errText + `"}`),
		Error:   errText,
	}
}

func testSuccessfulResult(name string) ToolResult {
	return ToolResult{
		CallID:  "call-ok",
		Name:    name,
		Content: "ok",
		Payload: json.RawMessage(`{"success":true,"status":"ok"}`),
	}
}

func TestRepeatedFailureTrackerCountsConsecutiveIdenticalFailures(t *testing.T) {
	tracker := newRepeatedFailureTracker()
	call := ToolCall{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{"path":"foo.go"}`)}
	result := testFailedResult("read_file", "not_found", "target file does not exist")

	first := tracker.Record(call, result)
	if first.Count != 1 {
		t.Fatalf("first count = %d, want 1", first.Count)
	}

	second := tracker.Record(call, result)
	if second.Count != 2 {
		t.Fatalf("second count = %d, want 2", second.Count)
	}

	third := tracker.Record(call, result)
	if third.Count != 3 {
		t.Fatalf("third count = %d, want 3", third.Count)
	}
}

func TestRepeatedFailureTrackerResetsAfterSuccess(t *testing.T) {
	tracker := newRepeatedFailureTracker()
	call := ToolCall{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{"path":"foo.go"}`)}
	fail := testFailedResult("read_file", "not_found", "target file does not exist")

	first := tracker.Record(call, fail)
	if first.Count != 1 {
		t.Fatalf("first count = %d, want 1", first.Count)
	}

	successCall := ToolCall{ID: "2", Name: "grep", Arguments: json.RawMessage(`{"regex":"x"}`)}
	success := testSuccessfulResult("grep")
	reset := tracker.Record(successCall, success)
	if reset.Count != 0 {
		t.Fatalf("success count = %d, want 0", reset.Count)
	}

	again := tracker.Record(call, fail)
	if again.Count != 1 {
		t.Fatalf("count after success reset = %d, want 1", again.Count)
	}
}

func TestRepeatedFailureTrackerResetsOnDifferentFailure(t *testing.T) {
	tracker := newRepeatedFailureTracker()

	callA := ToolCall{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{"path":"foo.go"}`)}
	failA := testFailedResult("read_file", "not_found", "foo missing")

	callB := ToolCall{ID: "2", Name: "read_file", Arguments: json.RawMessage(`{"path":"bar.go"}`)}
	failB := testFailedResult("read_file", "not_found", "bar missing")

	first := tracker.Record(callA, failA)
	if first.Count != 1 {
		t.Fatalf("first count = %d, want 1", first.Count)
	}

	second := tracker.Record(callB, failB)
	if second.Count != 1 {
		t.Fatalf("different failure count = %d, want 1", second.Count)
	}

	third := tracker.Record(callA, failA)
	if third.Count != 1 {
		t.Fatalf("original failure after different failure count = %d, want 1", third.Count)
	}
}

func TestRepeatedFailureTrackerUsesArgumentsForNonCommandTools(t *testing.T) {
	tracker := newRepeatedFailureTracker()

	result := testFailedResult("read_file", "not_found", "target file does not exist")

	callFoo := ToolCall{
		ID:        "1",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"foo.go"}`),
	}
	callBar := ToolCall{
		ID:        "2",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"bar.go"}`),
	}

	first := tracker.Record(callFoo, result)
	if first.Count != 1 {
		t.Fatalf("first count = %d, want 1", first.Count)
	}

	second := tracker.Record(callBar, result)
	if second.Count != 1 {
		t.Fatalf("different args count = %d, want 1", second.Count)
	}

	third := tracker.Record(callBar, result)
	if third.Count != 2 {
		t.Fatalf("same args repeated count = %d, want 2", third.Count)
	}
}

func TestJSONFingerprintCanonicalizesObjectKeyOrder(t *testing.T) {
	a := jsonFingerprint(json.RawMessage(`{"path":"foo.go","start_line":1}`))
	b := jsonFingerprint(json.RawMessage(`{"start_line":1,"path":"foo.go"}`))

	if a != b {
		t.Fatalf("fingerprints differ for equivalent JSON: %q vs %q", a, b)
	}
}

func TestExecuteCommandRepeatedFailureDifferentOutputDoesNotRepeat(t *testing.T) {
	tracker := newRepeatedFailureTracker()

	call := ToolCall{
		ID:        "1",
		Name:      "execute_command",
		Arguments: json.RawMessage(`{"command":"go test ./..."}`),
	}

	foo := ToolResult{
		CallID:  "1",
		Name:    "execute_command",
		Content: "Command failed",
		Payload: testPayload(t, map[string]any{
			"success":         false,
			"status":          "exited_non_zero",
			"error":           "exit status 1",
			"command":         "go test ./...",
			"exit_code":       1,
			"combined_output": "FAIL: TestFoo\nexit status 1\n",
		}),
		Error: "exit status 1",
	}

	bar := ToolResult{
		CallID:  "2",
		Name:    "execute_command",
		Content: "Command failed",
		Payload: testPayload(t, map[string]any{
			"success":         false,
			"status":          "exited_non_zero",
			"error":           "exit status 1",
			"command":         "go test ./...",
			"exit_code":       1,
			"combined_output": "FAIL: TestBar\nexit status 1\n",
		}),
		Error: "exit status 1",
	}

	first := tracker.Record(call, foo)
	if first.Count != 1 {
		t.Fatalf("first count = %d, want 1", first.Count)
	}

	second := tracker.Record(call, bar)
	if second.Count != 1 {
		t.Fatalf("different command output count = %d, want 1", second.Count)
	}
}

func TestExecuteCommandRepeatedFailureSameOutputRepeats(t *testing.T) {
	tracker := newRepeatedFailureTracker()

	call := ToolCall{
		ID:        "1",
		Name:      "execute_command",
		Arguments: json.RawMessage(`{"command":"go test ./..."}`),
	}

	result := ToolResult{
		CallID:  "1",
		Name:    "execute_command",
		Content: "Command failed",
		Payload: testPayload(t, map[string]any{
			"success":         false,
			"status":          "exited_non_zero",
			"error":           "exit status 1",
			"command":         "go test ./...",
			"exit_code":       1,
			"combined_output": "FAIL: TestFoo\nexit status 1\n",
		}),
		Error: "exit status 1",
	}

	first := tracker.Record(call, result)
	if first.Count != 1 {
		t.Fatalf("first count = %d, want 1", first.Count)
	}

	second := tracker.Record(call, result)
	if second.Count != 2 {
		t.Fatalf("same command output count = %d, want 2", second.Count)
	}
}

func TestCommandOutputFingerprintNormalizesDurations(t *testing.T) {
	a := commandOutputFingerprint("FAIL github.com/example/pkg 0.123s\n")
	b := commandOutputFingerprint("FAIL github.com/example/pkg 0.456s\n")

	if a != b {
		t.Fatalf("duration-normalized fingerprints differ: %q vs %q", a, b)
	}
}

func TestRepeatedFailureTrackerUsesPayloadFailureWhenErrorEmpty(t *testing.T) {
	tracker := newRepeatedFailureTracker()

	call := ToolCall{
		ID:        "1",
		Name:      "provider_tool",
		Arguments: json.RawMessage(`{"input":"x"}`),
	}

	result := ToolResult{
		CallID:  "1",
		Name:    "provider_tool",
		Content: "old string was not found",
		Payload: testPayload(t, map[string]any{
			"success": false,
			"status":  "no_match",
			"error":   "old string was not found",
		}),
		Error: "",
	}

	first := tracker.Record(call, result)
	if first.Count != 1 {
		t.Fatalf("first count = %d, want 1", first.Count)
	}

	second := tracker.Record(call, result)
	if second.Count != 2 {
		t.Fatalf("second count = %d, want 2", second.Count)
	}

	if second.Error != "old string was not found" {
		t.Fatalf("second error = %q, want payload error", second.Error)
	}
}

func TestRepeatedFailureTrackerDoesNotTreatSuccessPayloadAsFailure(t *testing.T) {
	tracker := newRepeatedFailureTracker()

	call := ToolCall{
		ID:        "1",
		Name:      "provider_tool",
		Arguments: json.RawMessage(`{}`),
	}

	result := ToolResult{
		CallID:  "1",
		Name:    "provider_tool",
		Content: "ok",
		Payload: testPayload(t, map[string]any{
			"success": true,
			"status":  "ok",
		}),
		Error: "",
	}

	repeated := tracker.Record(call, result)
	if repeated.Count != 0 {
		t.Fatalf("success payload count = %d, want 0", repeated.Count)
	}
}
