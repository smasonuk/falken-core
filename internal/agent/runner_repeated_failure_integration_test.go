package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/store"
)

type sequenceLLM struct {
	responses []CompletionResponse
	index     int
}

func (l *sequenceLLM) Complete(ctx context.Context, request CompletionRequest) (CompletionResponse, error) {
	if l.index >= len(l.responses) {
		return CompletionResponse{AssistantText: "done", FinishReason: FinishReasonStop}, nil
	}
	response := l.responses[l.index]
	l.index++
	return response, nil
}

type staticActiveTools struct {
	entries []tools.Entry
}

func (p staticActiveTools) ActiveTools(context.Context) ([]tools.Entry, error) {
	return tools.CloneEntries(p.entries), nil
}

type sequenceToolExecutor struct {
	results []ToolExecutionResult
	index   int
}

func (e *sequenceToolExecutor) ExecuteTool(ctx context.Context, request ToolExecutionRequest) (ToolExecutionResult, error) {
	if e.index >= len(e.results) {
		return ToolExecutionResult{
			Success: true,
			Status:  "ok",
			Content: "ok",
			Payload: json.RawMessage(`{"success":true,"status":"ok"}`),
		}, nil
	}
	result := e.results[e.index]
	e.index++
	return result, nil
}

func testRunnerForRepeatedFailureIntegration(t *testing.T, llm LLM, executor ToolExecutor, entries []tools.Entry) *Runner {
	t.Helper()

	workspace := filepath.Join(t.TempDir(), "workspace")
	stateRoot := filepath.Join(t.TempDir(), "state")
	layout, err := state.ResolveLayout(workspace, stateRoot)
	if err != nil {
		t.Fatalf("resolve layout: %v", err)
	}
	if err := state.EnsureLayoutState(layout); err != nil {
		t.Fatalf("ensure layout: %v", err)
	}

	history := NewHistoryManager(
		store.NewHistoryStore(layout),
		store.NewMemoryStore(layout),
	)

	runner, err := NewRunner(RunnerConfig{
		LLM:              llm,
		History:          history,
		Tools:            staticActiveTools{entries: entries},
		Executor:         executor,
		BaseSystemPrompt: "test",
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	return runner
}

func testToolEntry(name string) tools.Entry {
	return tools.Entry{
		Name:        name,
		Description: "Test tool",
		InputSchema: []byte(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
}

func TestRunnerEmitsOneToolResultEventForRepeatedFailureWarning(t *testing.T) {
	warning := "This tool has failed repeatedly with the same error. Do not retry the same call unchanged."

	llm := &sequenceLLM{
		responses: []CompletionResponse{
			{
				ToolCalls: []ToolCall{{
					ID:        "call-1",
					Name:      "failing_tool",
					Arguments: json.RawMessage(`{"x":1}`),
				}},
				FinishReason: FinishReasonToolCalls,
			},
			{
				ToolCalls: []ToolCall{{
					ID:        "call-2",
					Name:      "failing_tool",
					Arguments: json.RawMessage(`{"x":1}`),
				}},
				FinishReason: FinishReasonToolCalls,
			},
			{
				AssistantText: "done",
				FinishReason:  FinishReasonStop,
			},
		},
	}

	fail := ToolExecutionResult{
		Success: false,
		Status:  "failed",
		Content: "same failure",
		Payload: json.RawMessage(`{"success":false,"status":"failed","error":"same failure"}`),
		Error:   "same failure",
	}

	executor := &sequenceToolExecutor{
		results: []ToolExecutionResult{fail, fail},
	}

	var toolResults []ToolResult
	events := func(event Event) {
		if event.Type == EventToolResult && event.ToolResult != nil {
			toolResults = append(toolResults, *event.ToolResult)
		}
	}

	runner := testRunnerForRepeatedFailureIntegration(
		t,
		llm,
		executor,
		[]tools.Entry{testToolEntry("failing_tool")},
	)

	result, err := runner.Run(context.Background(), RunRequest{Prompt: "test", Events: events})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Completed {
		t.Fatalf("run completed = false, error = %s", result.Error)
	}

	if len(toolResults) != 2 {
		t.Fatalf("tool result events = %d, want 2", len(toolResults))
	}

	if !strings.Contains(toolResults[1].Content, warning) {
		t.Fatalf("second tool result content missing warning: %q", toolResults[1].Content)
	}
}

func TestRunnerAbortsOnThirdConsecutiveIdenticalFailure(t *testing.T) {
	llm := &sequenceLLM{
		responses: []CompletionResponse{
			{ToolCalls: []ToolCall{{ID: "call-1", Name: "failing_tool", Arguments: json.RawMessage(`{"x":1}`)}}, FinishReason: FinishReasonToolCalls},
			{ToolCalls: []ToolCall{{ID: "call-2", Name: "failing_tool", Arguments: json.RawMessage(`{"x":1}`)}}, FinishReason: FinishReasonToolCalls},
			{ToolCalls: []ToolCall{{ID: "call-3", Name: "failing_tool", Arguments: json.RawMessage(`{"x":1}`)}}, FinishReason: FinishReasonToolCalls},
		},
	}

	fail := ToolExecutionResult{
		Success: false,
		Status:  "failed",
		Content: "same failure",
		Payload: json.RawMessage(`{"success":false,"status":"failed","error":"same failure"}`),
		Error:   "same failure",
	}

	executor := &sequenceToolExecutor{
		results: []ToolExecutionResult{fail, fail, fail},
	}

	runner := testRunnerForRepeatedFailureIntegration(
		t,
		llm,
		executor,
		[]tools.Entry{testToolEntry("failing_tool")},
	)

	result, err := runner.Run(context.Background(), RunRequest{Prompt: "test"})
	if err == nil {
		t.Fatal("run error is nil, want ErrRepeatedToolFailure")
	}
	if !errors.Is(err, ErrRepeatedToolFailure) {
		t.Fatalf("run error = %v, want ErrRepeatedToolFailure", err)
	}
	if result.Completed {
		t.Fatal("run completed = true, want false")
	}
}

func TestRunnerResetsRepeatedFailureAfterSuccessfulToolResult(t *testing.T) {
	warning := "This tool has failed repeatedly with the same error. Do not retry the same call unchanged."

	llm := &sequenceLLM{
		responses: []CompletionResponse{
			{ToolCalls: []ToolCall{{ID: "call-1", Name: "failing_tool", Arguments: json.RawMessage(`{"x":1}`)}}, FinishReason: FinishReasonToolCalls},
			{ToolCalls: []ToolCall{{ID: "call-2", Name: "successful_tool", Arguments: json.RawMessage(`{}`)}}, FinishReason: FinishReasonToolCalls},
			{ToolCalls: []ToolCall{{ID: "call-3", Name: "failing_tool", Arguments: json.RawMessage(`{"x":1}`)}}, FinishReason: FinishReasonToolCalls},
			{AssistantText: "done", FinishReason: FinishReasonStop},
		},
	}

	fail := ToolExecutionResult{
		Success: false,
		Status:  "failed",
		Content: "same failure",
		Payload: json.RawMessage(`{"success":false,"status":"failed","error":"same failure"}`),
		Error:   "same failure",
	}

	success := ToolExecutionResult{
		Success: true,
		Status:  "ok",
		Content: "ok",
		Payload: json.RawMessage(`{"success":true,"status":"ok"}`),
	}

	executor := &sequenceToolExecutor{
		results: []ToolExecutionResult{fail, success, fail},
	}

	var toolResults []ToolResult
	events := func(event Event) {
		if event.Type == EventToolResult && event.ToolResult != nil {
			toolResults = append(toolResults, *event.ToolResult)
		}
	}

	runner := testRunnerForRepeatedFailureIntegration(
		t,
		llm,
		executor,
		[]tools.Entry{
			testToolEntry("failing_tool"),
			testToolEntry("successful_tool"),
		},
	)

	result, err := runner.Run(context.Background(), RunRequest{Prompt: "test", Events: events})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Completed {
		t.Fatalf("run completed = false, error = %s", result.Error)
	}

	for i, toolResult := range toolResults {
		if strings.Contains(toolResult.Content, warning) {
			t.Fatalf("tool result %d unexpectedly contains warning: %q", i, toolResult.Content)
		}
	}
}

func TestRunnerDoesNotWarnForSameCommandWithDifferentFailureOutput(t *testing.T) {
	warning := "This tool has failed repeatedly with the same error. Do not retry the same call unchanged."

	llm := &sequenceLLM{
		responses: []CompletionResponse{
			{ToolCalls: []ToolCall{{ID: "call-1", Name: "execute_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}}, FinishReason: FinishReasonToolCalls},
			{ToolCalls: []ToolCall{{ID: "call-2", Name: "execute_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}}, FinishReason: FinishReasonToolCalls},
			{AssistantText: "done", FinishReason: FinishReasonStop},
		},
	}

	failFoo := ToolExecutionResult{
		Success: false,
		Status:  "exited_non_zero",
		Content: "go test failed",
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

	failBar := ToolExecutionResult{
		Success: false,
		Status:  "exited_non_zero",
		Content: "go test failed",
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

	executor := &sequenceToolExecutor{
		results: []ToolExecutionResult{failFoo, failBar},
	}

	var toolResults []ToolResult
	events := func(event Event) {
		if event.Type == EventToolResult && event.ToolResult != nil {
			toolResults = append(toolResults, *event.ToolResult)
		}
	}

	runner := testRunnerForRepeatedFailureIntegration(
		t,
		llm,
		executor,
		[]tools.Entry{testToolEntry("execute_command")},
	)

	result, err := runner.Run(context.Background(), RunRequest{Prompt: "test", Events: events})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Completed {
		t.Fatalf("run completed = false, error = %s", result.Error)
	}

	for i, toolResult := range toolResults {
		if strings.Contains(toolResult.Content, warning) {
			t.Fatalf("tool result %d unexpectedly contains warning: %q", i, toolResult.Content)
		}
	}
}
