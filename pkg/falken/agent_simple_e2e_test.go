package falken

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestSimpleAgentE2EToolOnlyTypedToolCall(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "lookup_user",
				Arguments: json.RawMessage(`{"email":"user@example.com"}`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "user u_123 is active", FinishReason: FinishReasonStop},
	}}
	var gotArgs []typedLookupArgs
	lookup := Func(
		"lookup_user",
		"Look up a user by email.",
		func(_ context.Context, args typedLookupArgs) (typedLookupResult, error) {
			gotArgs = append(gotArgs, args)
			return typedLookupResult{ID: "u_123", Email: args.Email}, nil
		},
	)

	agent, err := NewAgent(context.Background(), AgentConfig{
		LLM:   llm,
		Tools: []Tool{lookup},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer agent.Close(context.Background())

	answer, err := agent.Run(context.Background(), "find the user")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "user u_123 is active" {
		t.Fatalf("answer = %q, want final assistant answer", answer)
	}
	if got := sessionAgentToolNames(llm.requests[0].Tools); !reflect.DeepEqual(got, []string{"lookup_user"}) {
		t.Fatalf("initial tools = %v, want typed tool only", got)
	}
	if len(gotArgs) != 1 || gotArgs[0].Email != "user@example.com" {
		t.Fatalf("typed tool args = %+v, want decoded email", gotArgs)
	}
	toolResult := lastToolResultMessage(t, agent.session)
	if toolResult.Name != "lookup_user" || toolResult.Error != "" {
		t.Fatalf("tool result = %+v, want successful lookup_user result", toolResult)
	}
}

func TestSimpleAgentE2EReadOnlyDirectoryToolExposure(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	agent, err := NewAgent(context.Background(), AgentConfig{
		LLM:           llm,
		ReadDirectory: tempWorkspace(t),
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer agent.Close(context.Background())
	if _, err := agent.Run(context.Background(), "list tools"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := sessionAgentToolNames(llm.requests[0].Tools)
	if !reflect.DeepEqual(got, BuiltinReadOnlyFileTools) {
		t.Fatalf("read-only directory tools = %v, want %v", got, BuiltinReadOnlyFileTools)
	}
}

func TestSimpleAgentE2EPermissionEscalationToolExposure(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	agent, err := NewAgent(context.Background(), AgentConfig{
		LLM:           llm,
		ReadDirectory: tempWorkspace(t),
		Permissions: SimplePermissions{
			AllowWriteFiles: true,
			AllowShell:      true,
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer agent.Close(context.Background())
	if _, err := agent.Run(context.Background(), "list tools"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := sessionAgentToolNames(llm.requests[0].Tools)
	for _, want := range []string{"read_file", "write_file", "execute_command"} {
		if !sessionAgentHasTool(got, want) {
			t.Fatalf("escalated tools = %v, missing %s", got, want)
		}
	}
	if sessionAgentHasTool(got, "read_plan") || sessionAgentHasTool(got, "read_memory") {
		t.Fatalf("escalated simple-agent tools = %v, should not include planning or memory", got)
	}
}
