package falken

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStaticToolProviderHappyPath(t *testing.T) {
	setStateHomeEnv(t)

	tool := ToolFunc(testPlanSafeToolDescriptor("echo"), func(_ context.Context, invocation ToolInvocation) (ToolExecutionResult, error) {
		return ToolExecutionResult{
			Success: true,
			Status:  "succeeded",
			Content: string(invocation.Arguments),
			Payload: json.RawMessage(`{"echo":true}`),
		}, nil
	})
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-echo",
				Name:      "echo",
				Arguments: json.RawMessage(`{"text":"hello"}`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		ToolProviders:    []ToolProvider{StaticToolProvider(tool)},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "echo"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	result := lastToolResultMessage(t, session)
	if result.Name != "echo" || result.Content != `{"text":"hello"}` || string(result.Payload) != `{"echo":true}` {
		t.Fatalf("tool result = %+v, want echo result", result)
	}
}

func TestStaticToolProviderRejectsInvalidSchema(t *testing.T) {
	provider := StaticToolProvider(ToolFunc(ToolDescriptor{
		Name:        "bad_schema",
		Description: "bad schema",
		Parameters:  json.RawMessage(`{"type":"string"}`),
	}, func(context.Context, ToolInvocation) (ToolExecutionResult, error) {
		return ToolExecutionResult{}, nil
	}))

	err := provider.Start(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "JSON object schema") {
		t.Fatalf("Start error = %v, want object schema rejection", err)
	}
}

func TestStaticToolProviderRejectsDuplicateNames(t *testing.T) {
	provider := StaticToolProvider(
		ToolFunc(testToolDescriptor("dupe"), func(context.Context, ToolInvocation) (ToolExecutionResult, error) {
			return ToolExecutionResult{}, nil
		}),
		ToolFunc(testToolDescriptor("dupe"), func(context.Context, ToolInvocation) (ToolExecutionResult, error) {
			return ToolExecutionResult{}, nil
		}),
	)

	err := provider.Start(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate static tool name") {
		t.Fatalf("Start error = %v, want duplicate name rejection", err)
	}
}

func TestStaticToolProviderExecutionErrorFlowsToToolResult(t *testing.T) {
	setStateHomeEnv(t)

	boom := errors.New("boom")
	tool := ToolFunc(testPlanSafeToolDescriptor("explode"), func(context.Context, ToolInvocation) (ToolExecutionResult, error) {
		return ToolExecutionResult{}, boom
	})
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls:    []ToolCall{{ID: "call-explode", Name: "explode", Arguments: json.RawMessage(`{}`)}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		ToolProviders:    []ToolProvider{StaticToolProvider(tool)},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "explode"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	result := lastToolResultMessage(t, session)
	if result.Name != "explode" || !strings.Contains(result.Error, "boom") || !strings.Contains(string(result.Payload), "execution_error") {
		t.Fatalf("tool result = %+v, want execution_error boom", result)
	}
}

func TestStaticToolProviderClonesDescriptorsAndPayloads(t *testing.T) {
	descriptor := testToolDescriptor("clone_check")
	descriptor.Keywords = []string{"original"}
	descriptor.Parameters = json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	payload := json.RawMessage(`{"value":"original"}`)
	provider := StaticToolProvider(ToolFunc(descriptor, func(context.Context, ToolInvocation) (ToolExecutionResult, error) {
		return ToolExecutionResult{Success: true, Status: "succeeded", Payload: payload}, nil
	}))
	descriptor.Keywords[0] = "mutated"
	descriptor.Parameters[0] = '!'

	if err := provider.Start(context.Background(), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	descriptors, err := provider.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	descriptors[0].Keywords[0] = "caller-mutated"
	descriptors[0].Parameters[0] = '!'
	again, err := provider.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools again: %v", err)
	}
	if again[0].Keywords[0] != "original" || !json.Valid(again[0].Parameters) {
		t.Fatalf("descriptor was not defensively cloned: %+v", again[0])
	}

	result, err := provider.ExecuteTool(context.Background(), ToolInvocation{Name: "clone_check"})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	result.Payload[0] = '!'
	resultAgain, err := provider.ExecuteTool(context.Background(), ToolInvocation{Name: "clone_check"})
	if err != nil {
		t.Fatalf("ExecuteTool again: %v", err)
	}
	if !json.Valid(resultAgain.Payload) || string(resultAgain.Payload) != `{"value":"original"}` {
		t.Fatalf("payload was not defensively cloned: %q", string(resultAgain.Payload))
	}
}
