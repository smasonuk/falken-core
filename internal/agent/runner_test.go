package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestRunnerNoToolRunEmitsAndPersistsAssistantText(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.responses = []agent.CompletionResponse{{
		AssistantText: "done",
		FinishReason:  agent.FinishReasonStop,
	}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{
		Prompt: "hello",
		Events: h.captureEvent,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalOutput != "done" {
		t.Fatalf("final output = %q, want done", result.FinalOutput)
	}
	if !result.Completed || result.Error != "" {
		t.Fatalf("result = %+v, want completed success", result)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventAssistantText, agent.EventRunCompleted}) {
		t.Fatalf("events = %v", got)
	}

	messages, err := h.history.Load()
	if err != nil {
		t.Fatalf("Load history: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("history length = %d, want system,user,assistant", len(messages))
	}
	if messages[0].Role != agent.RoleSystem || strings.Contains(messages[0].Content, "--- CURRENT MODE ---") {
		t.Fatalf("system message = %+v", messages[0])
	}
	if messages[1].Role != agent.RoleUser || messages[1].Content != "hello" {
		t.Fatalf("user message = %+v", messages[1])
	}
	if messages[2].Role != agent.RoleAssistant || messages[2].Content != "done" {
		t.Fatalf("assistant message = %+v", messages[2])
	}
}

func TestRunnerSingleToolCallExecutesPersistsAndContinues(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("read_file")}
	h.llm.responses = []agent.CompletionResponse{
		{
			AssistantText: "I will read.",
			ToolCalls: []agent.ToolCall{{
				ID:        "call-1",
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			}},
			FinishReason: agent.FinishReasonToolCalls,
		},
		{
			AssistantText: "The file says hello.",
			FinishReason:  agent.FinishReasonStop,
		},
	}
	h.executor.results = []agent.ToolExecutionResult{{
		Success: true,
		Status:  "succeeded",
		Payload: json.RawMessage(`{"content":"hello"}`),
	}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "read", Events: h.captureEvent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalOutput != "The file says hello." {
		t.Fatalf("final output = %q", result.FinalOutput)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{
		agent.EventAssistantText,
		agent.EventToolCall,
		agent.EventToolResult,
		agent.EventAssistantText,
		agent.EventRunCompleted,
	}) {
		t.Fatalf("events = %v", got)
	}
	if len(h.executor.requests) != 1 || h.executor.requests[0].Name != "read_file" {
		t.Fatalf("executor requests = %+v", h.executor.requests)
	}
	if len(h.llm.requests) != 2 {
		t.Fatalf("llm request count = %d, want 2", len(h.llm.requests))
	}
	secondMessages := h.llm.requests[1].Messages
	if secondMessages[len(secondMessages)-2].Role != agent.RoleTool {
		t.Fatalf("second LLM request penultimate message = %+v, want tool result", secondMessages[len(secondMessages)-2])
	}
	if secondMessages[len(secondMessages)-1].Role != agent.RoleSystem || !strings.Contains(secondMessages[len(secondMessages)-1].Content, "mode: default") {
		t.Fatalf("second LLM request last message = %+v, want runtime mode", secondMessages[len(secondMessages)-1])
	}

	messages, err := h.history.Load()
	if err != nil {
		t.Fatalf("Load history: %v", err)
	}
	if messages[len(messages)-2].Role != agent.RoleTool || messages[len(messages)-2].ToolResult == nil {
		t.Fatalf("tool result was not persisted before final assistant: %+v", messages)
	}
}

func TestRunnerMultipleToolCallsExecuteDeterministically(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("read_file"), testToolEntry("search_files")}
	h.llm.responses = []agent.CompletionResponse{
		{
			ToolCalls: []agent.ToolCall{
				{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a"}`)},
				{ID: "call-2", Name: "search_files", Arguments: json.RawMessage(`{"query":"needle"}`)},
			},
			FinishReason: agent.FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: agent.FinishReasonStop},
	}
	h.executor.results = []agent.ToolExecutionResult{
		{Success: true, Status: "succeeded", Payload: json.RawMessage(`{"a":1}`)},
		{Success: true, Status: "succeeded", Payload: json.RawMessage(`{"b":2}`)},
	}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "do tools", Events: h.captureEvent}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := executedNames(h.executor.requests)
	if !reflect.DeepEqual(got, []string{"read_file", "search_files"}) {
		t.Fatalf("executed tools = %v", got)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{
		agent.EventToolCall,
		agent.EventToolResult,
		agent.EventToolCall,
		agent.EventToolResult,
		agent.EventAssistantText,
		agent.EventRunCompleted,
	}) {
		t.Fatalf("events = %v", got)
	}
}

func TestRunnerUnknownInactiveAndMalformedToolCallsReturnToolErrors(t *testing.T) {
	tests := []struct {
		name       string
		call       agent.ToolCall
		wantStatus string
	}{
		{
			name:       "unknown tool",
			call:       agent.ToolCall{ID: "call-1", Name: "missing_tool", Arguments: json.RawMessage(`{}`)},
			wantStatus: "inactive_or_unknown_tool",
		},
		{
			name:       "malformed arguments",
			call:       agent.ToolCall{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":`)},
			wantStatus: "malformed_arguments",
		},
		{
			name:       "missing id",
			call:       agent.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{}`)},
			wantStatus: "missing_tool_call_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newRunnerHarness(t)
			h.available = []tools.Entry{testToolEntry("read_file")}
			h.llm.responses = []agent.CompletionResponse{
				{ToolCalls: []agent.ToolCall{tt.call}, FinishReason: agent.FinishReasonToolCalls},
				{AssistantText: "recovered", FinishReason: agent.FinishReasonStop},
			}

			result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "call", Events: h.captureEvent})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.FinalOutput != "recovered" {
				t.Fatalf("final output = %q, want recovered", result.FinalOutput)
			}
			if len(h.executor.requests) != 0 {
				t.Fatalf("executor requests = %+v, want none", h.executor.requests)
			}
			toolResult := firstToolResultEvent(t, h.events)
			if toolResult.Error == "" || !strings.Contains(string(toolResult.Payload), tt.wantStatus) {
				t.Fatalf("tool result = %+v, want status %q", toolResult, tt.wantStatus)
			}
		})
	}
}

func TestRunnerPlanModeBlocksMutatingToolsAndFiltersExposure(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("read_file"), testToolEntry("write_file")}
	if err := h.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	h.llm.responses = []agent.CompletionResponse{
		{
			ToolCalls: []agent.ToolCall{
				{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"a","content":"x"}`)},
				{ID: "call-2", Name: "read_file", Arguments: json.RawMessage(`{"path":"a"}`)},
			},
			FinishReason: agent.FinishReasonToolCalls,
		},
		{AssistantText: "planned", FinishReason: agent.FinishReasonStop},
	}
	h.executor.results = []agent.ToolExecutionResult{{Success: true, Status: "succeeded", Payload: json.RawMessage(`{"content":"ok"}`)}}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "plan", Events: h.captureEvent}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := toolDefinitionNames(h.llm.requests[0].Tools); !reflect.DeepEqual(got, []string{"read_file"}) {
		t.Fatalf("plan-mode exposed tools = %v, want read_file only", got)
	}
	if got := executedNames(h.executor.requests); !reflect.DeepEqual(got, []string{"read_file"}) {
		t.Fatalf("executed tools = %v, want read_file only", got)
	}
	results := toolResultEvents(h.events)
	if len(results) != 2 {
		t.Fatalf("tool result events = %d, want 2", len(results))
	}
	if !strings.Contains(string(results[0].Payload), "blocked_by_mode") {
		t.Fatalf("first result = %+v, want blocked_by_mode", results[0])
	}
	if results[1].Error != "" {
		t.Fatalf("second result = %+v, want successful read", results[1])
	}
}

func TestRunnerDefaultModeExposesActiveTools(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("read_file"), testToolEntry("write_file"), testToolEntry("write_plan")}
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "done", FinishReason: agent.FinishReasonStop}}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "default"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := toolDefinitionNames(h.llm.requests[0].Tools); !reflect.DeepEqual(got, []string{"read_file", "write_file"}) {
		t.Fatalf("default exposed tools = %v", got)
	}
}

func TestRunnerNoTodosDoesNotRequireImplementationSubmission(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "done", FinishReason: agent.FinishReasonStop}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "simple", Events: h.captureEvent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed {
		t.Fatalf("result = %+v, want completed", result)
	}
	if got := eventTypes(h.events); got[len(got)-1] != agent.EventRunCompleted {
		t.Fatalf("events = %v, want final run_completed", got)
	}
}

func TestRunnerNudgesWhenTodosExistAndSubmissionMissing(t *testing.T) {
	h := newRunnerHarness(t)
	if err := h.todos.Replace([]agent.Todo{{ID: "t1", Content: "Implement", Status: agent.TodoStatusPending}}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}
	h.available = []tools.Entry{testToolEntry("submit_plan_implementation")}
	h.llm.responses = []agent.CompletionResponse{
		{AssistantText: "done early", FinishReason: agent.FinishReasonStop},
		{AssistantText: "still done", FinishReason: agent.FinishReasonStop},
	}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "complex", Events: h.captureEvent})
	if !errors.Is(err, agent.ErrImplementationSubmissionRequired) {
		t.Fatalf("Run error = %v, want implementation-submission-required failure", err)
	}
	if result.Completed {
		t.Fatalf("result = %+v, want not completed", result)
	}
	messages, err := h.history.Load()
	if err != nil {
		t.Fatalf("Load history: %v", err)
	}
	nudges := 0
	for _, message := range messages {
		if message.Role == agent.RoleSystem && strings.Contains(message.Content, "call submit_plan_implementation") {
			nudges++
		}
	}
	if nudges != 1 {
		t.Fatalf("history = %+v, want submit_plan_implementation nudge", messages)
	}
	if len(h.llm.requests) >= h.maxToolRounds {
		t.Fatalf("llm requests = %d, want failure before consuming max tool rounds", len(h.llm.requests))
	}
	if got := eventTypes(h.events); got[len(got)-1] != agent.EventRunFailed {
		t.Fatalf("events = %v, want run_failed without accepted submission", got)
	}
}

func TestRunnerAddsTransientTodoProgressNudgeAfterMutatingWork(t *testing.T) {
	h := newRunnerHarnessWithMaxRounds(t, 1)
	if err := h.todos.Replace([]agent.Todo{
		{ID: "impl", Content: "Implement change", Status: agent.TodoStatusInProgress},
		{ID: "verify", Content: "Run verification", Status: agent.TodoStatusPending},
	}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}
	h.available = []tools.Entry{testToolEntry("write_file")}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{
			ID:        "call-1",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
		}}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "done without submit", FinishReason: agent.FinishReasonStop},
	}
	h.executor.results = []agent.ToolExecutionResult{{
		Success: true,
		Status:  "succeeded",
		Payload: json.RawMessage(`{"success":true,"status":"succeeded","workspace_mutation":{"observed":true,"files_changed":1}}`),
	}}

	_, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "implement", Events: h.captureEvent})
	if !errors.Is(err, agent.ErrMaxToolRoundsExceeded) {
		t.Fatalf("Run error = %v, want max-round guard after captured nudge request", err)
	}
	if len(h.llm.requests) < 2 {
		t.Fatalf("llm request count = %d, want at least 2", len(h.llm.requests))
	}
	nudge := findSystemMessageContaining(h.llm.requests[1].Messages, "TODO progress checkpoint:")
	if nudge == "" {
		t.Fatalf("second LLM request messages = %+v, want TODO progress checkpoint", h.llm.requests[1].Messages)
	}
	if !strings.Contains(nudge, agent.RenderTodos([]agent.Todo{
		{ID: "impl", Content: "Implement change", Status: agent.TodoStatusInProgress},
		{ID: "verify", Content: "Run verification", Status: agent.TodoStatusPending},
	})) {
		t.Fatalf("nudge = %q, want rendered todos", nudge)
	}
	if !strings.Contains(nudge, "do not defer TODO completion") {
		t.Fatalf("nudge = %q, want defer warning", nudge)
	}

	messages, err := h.history.Load()
	if err != nil {
		t.Fatalf("Load history: %v", err)
	}
	if findSystemMessageContaining(messages, "TODO progress checkpoint:") != "" {
		t.Fatalf("history = %+v, TODO progress nudge should be transient", messages)
	}
}

func TestRunnerSkipsTodoProgressNudgeWhenWriteTodosSucceedsInSameRound(t *testing.T) {
	h := newRunnerHarnessWithMaxRounds(t, 1)
	if err := h.todos.Replace([]agent.Todo{
		{ID: "impl", Content: "Implement change", Status: agent.TodoStatusInProgress},
		{ID: "verify", Content: "Run verification", Status: agent.TodoStatusPending},
	}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}
	h.available = []tools.Entry{testToolEntry("write_file"), testToolEntry("write_todos")}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{
			{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`)},
			{ID: "call-2", Name: "write_todos", Arguments: json.RawMessage(`{"todos":[]}`)},
		}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "done without submit", FinishReason: agent.FinishReasonStop},
	}
	h.executor.results = []agent.ToolExecutionResult{
		{
			Success: true,
			Status:  "succeeded",
			Payload: json.RawMessage(`{"success":true,"status":"succeeded","workspace_mutation":{"observed":true,"files_changed":1}}`),
		},
		{
			Success: true,
			Status:  "succeeded",
			Payload: json.RawMessage(`{"success":true,"status":"succeeded","todos_written":true}`),
		},
	}

	_, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "implement", Events: h.captureEvent})
	if !errors.Is(err, agent.ErrMaxToolRoundsExceeded) {
		t.Fatalf("Run error = %v, want max-round guard after second request", err)
	}
	if len(h.llm.requests) < 2 {
		t.Fatalf("llm request count = %d, want at least 2", len(h.llm.requests))
	}
	if nudge := findSystemMessageContaining(h.llm.requests[1].Messages, "TODO progress checkpoint:"); nudge != "" {
		t.Fatalf("unexpected TODO progress nudge in second request: %q", nudge)
	}
}

func TestRunnerCompletedTodosAcceptedSubmissionAllowsFinish(t *testing.T) {
	h := newRunnerHarness(t)
	if err := h.todos.Replace([]agent.Todo{{ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted}}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}
	h.available = []tools.Entry{testToolEntry("submit_plan_implementation")}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{
			ID: "submit-1", Name: "submit_plan_implementation", Arguments: json.RawMessage(`{"summary":"done","verification_summary":"passed"}`),
		}}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "final", FinishReason: agent.FinishReasonStop},
	}
	h.executor.results = []agent.ToolExecutionResult{{
		Success: true,
		Status:  "accepted",
		Payload: json.RawMessage(`{"success":true,"accepted":true,"status":"accepted"}`),
	}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "complex", Events: h.captureEvent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "final" {
		t.Fatalf("result = %+v, want final completion", result)
	}
	if got := eventTypes(h.events); got[len(got)-1] != agent.EventRunCompleted {
		t.Fatalf("events = %v, want run_completed", got)
	}
}

func TestRunnerSubmissionAfterRequiredNudgeAllowsFinish(t *testing.T) {
	h := newRunnerHarness(t)
	if err := h.todos.Replace([]agent.Todo{{ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted}}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}
	h.available = []tools.Entry{testToolEntry("submit_plan_implementation")}
	h.llm.responses = []agent.CompletionResponse{
		{AssistantText: "done early", FinishReason: agent.FinishReasonStop},
		{ToolCalls: []agent.ToolCall{{
			ID: "submit-1", Name: "submit_plan_implementation", Arguments: json.RawMessage(`{"summary":"done","verification_summary":"passed"}`),
		}}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "final", FinishReason: agent.FinishReasonStop},
	}
	h.executor.results = []agent.ToolExecutionResult{{
		Success: true,
		Status:  "accepted",
		Payload: json.RawMessage(`{"success":true,"accepted":true,"status":"accepted"}`),
	}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "complex", Events: h.captureEvent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "final" {
		t.Fatalf("result = %+v, want final completion", result)
	}
	if nudge := findSystemMessageContaining(h.llm.requests[1].Messages, "call submit_plan_implementation"); nudge == "" {
		t.Fatalf("second LLM request messages = %+v, want submit-required nudge", h.llm.requests[1].Messages)
	}
	messages, err := h.history.Load()
	if err != nil {
		t.Fatalf("Load history: %v", err)
	}
	nudges := 0
	for _, message := range messages {
		if message.Role == agent.RoleSystem && strings.Contains(message.Content, "call submit_plan_implementation") {
			nudges++
		}
	}
	if nudges != 1 {
		t.Fatalf("history = %+v, want one submit-required nudge", messages)
	}
	if got := eventTypes(h.events); got[len(got)-1] != agent.EventRunCompleted {
		t.Fatalf("events = %v, want run_completed", got)
	}
}

func TestRunnerPlanRouterEntersPlanModeForComplexWork(t *testing.T) {
	h := newRunnerHarnessWithPlanRouter(t, agent.NewHeuristicPlanRouter())
	h.available = []tools.Entry{testToolEntry("read_file"), testToolEntry("write_file"), testToolEntry("write_plan")}
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "planned", FinishReason: agent.FinishReasonStop}}

	_, err := h.runner.Run(context.Background(), agent.RunRequest{
		Prompt: "Write a production-ready multi-package Go application from scratch using SQLite, YAML configuration, concurrency with worker pools, and unit tests.",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.mode.Current() != agent.ModePlan {
		t.Fatalf("mode = %q, want plan", h.mode.Current())
	}
	if got := toolDefinitionNames(h.llm.requests[0].Tools); !reflect.DeepEqual(got, []string{"read_file", "write_plan"}) {
		t.Fatalf("routed plan tools = %v, want read_file/write_plan", got)
	}
}

func TestRunnerPlanRouterKeepsSimpleRequestInDefaultMode(t *testing.T) {
	h := newRunnerHarnessWithPlanRouter(t, agent.NewHeuristicPlanRouter())
	h.available = []tools.Entry{testToolEntry("read_file"), testToolEntry("write_file")}
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "done", FinishReason: agent.FinishReasonStop}}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "list the files in this dir"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.mode.Current() != agent.ModeDefault {
		t.Fatalf("mode = %q, want default", h.mode.Current())
	}
	if got := toolDefinitionNames(h.llm.requests[0].Tools); !reflect.DeepEqual(got, []string{"read_file", "write_file"}) {
		t.Fatalf("default tools = %v, want read_file/write_file", got)
	}
}

func TestRunnerAutoPlanUsesSameLLMForRoutingAndAgentCall(t *testing.T) {
	h := newRunnerHarnessWithAutoPlan(t)
	h.available = []tools.Entry{
		testToolEntry("read_file"),
		testToolEntry("read_files"),
		testToolEntry("write_file"),
		testToolEntry("execute_command"),
		testToolEntry("read_plan"),
		testToolEntry("write_plan"),
	}
	largePrompt := strings.Repeat("large user request line\n", 5000)
	h.llm.responses = []agent.CompletionResponse{
		routingResponse(true, "complex", "high", []string{"large"}),
		{AssistantText: "planning", FinishReason: agent.FinishReasonStop},
	}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: largePrompt, Events: h.captureEvent}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.llm.requests) != 2 {
		t.Fatalf("llm request count = %d, want routing and first agent call", len(h.llm.requests))
	}
	routingReq := h.llm.requests[0]
	agentReq := h.llm.requests[1]
	assertSharedMessagePrefix(t, routingReq.Messages, agentReq.Messages)
	if routingReq.Messages[len(routingReq.Messages)-2].Content != largePrompt {
		t.Fatalf("routing request large prompt position = %+v", routingReq.Messages)
	}
	if !strings.Contains(routingReq.Messages[len(routingReq.Messages)-1].Content, "Call decide_plan_mode exactly once") {
		t.Fatalf("routing tail = %q, want routing instruction", routingReq.Messages[len(routingReq.Messages)-1].Content)
	}
	if !strings.Contains(agentReq.Messages[len(agentReq.Messages)-1].Content, "mode: plan") {
		t.Fatalf("agent tail = %q, want plan runtime mode", agentReq.Messages[len(agentReq.Messages)-1].Content)
	}
	if got := toolDefinitionNames(routingReq.Tools); !reflect.DeepEqual(got, []string{"decide_plan_mode"}) {
		t.Fatalf("routing tools = %v, want decide_plan_mode", got)
	}
	if routingReq.ToolChoice == nil || routingReq.ToolChoice.Type != "tool" || routingReq.ToolChoice.Name != "decide_plan_mode" {
		t.Fatalf("routing tool choice = %+v, want forced decide_plan_mode", routingReq.ToolChoice)
	}
	if got := toolDefinitionNames(agentReq.Tools); !reflect.DeepEqual(got, []string{"read_file", "read_files", "read_plan", "write_plan"}) {
		t.Fatalf("plan-mode tools = %v, want read/planning tools", got)
	}
	if got := eventTypes(h.events); len(got) == 0 || got[0] != agent.EventPlanRouting {
		t.Fatalf("events = %v, want first event plan routing", got)
	}
}

func TestRunnerAutoPlanSimpleRequestGetsDefaultToolsWithoutPlanningTools(t *testing.T) {
	h := newRunnerHarnessWithAutoPlan(t)
	h.available = []tools.Entry{testToolEntry("read_file"), testToolEntry("write_file"), testToolEntry("read_plan"), testToolEntry("write_plan")}
	h.llm.responses = []agent.CompletionResponse{
		routingResponse(false, "simple", "high", nil),
		{AssistantText: "done", FinishReason: agent.FinishReasonStop},
	}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "small edit"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.mode.Current() != agent.ModeDefault {
		t.Fatalf("mode = %q, want default", h.mode.Current())
	}
	if got := toolDefinitionNames(h.llm.requests[1].Tools); !reflect.DeepEqual(got, []string{"read_file", "write_file", "read_plan"}) {
		t.Fatalf("default tools = %v, want implementation tools plus read_plan", got)
	}
}

func TestRunnerSkipsPlanRoutingWhenImplementationSubmissionRequired(t *testing.T) {
	h := newRunnerHarnessWithAutoPlan(t)
	if err := h.todos.Replace([]agent.Todo{{ID: "t1", Content: "Implement", Status: agent.TodoStatusPending}}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}
	h.available = []tools.Entry{testToolEntry("submit_plan_implementation")}
	h.llm.responses = []agent.CompletionResponse{
		{AssistantText: "done early", FinishReason: agent.FinishReasonStop},
		{AssistantText: "still done", FinishReason: agent.FinishReasonStop},
	}

	_, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "continue implementation"})
	if !errors.Is(err, agent.ErrImplementationSubmissionRequired) {
		t.Fatalf("Run error = %v, want implementation submission required", err)
	}
	if len(h.llm.requests) != 2 {
		t.Fatalf("llm requests = %d, want only main completion calls", len(h.llm.requests))
	}
	for _, request := range h.llm.requests {
		if request.ToolChoice != nil && request.ToolChoice.Name == "decide_plan_mode" {
			t.Fatalf("unexpected plan-routing LLM request: %+v", request)
		}
	}
}

func TestRunnerAutoPlanRejectsAssistantTextOnlyRouterResponse(t *testing.T) {
	h := newRunnerHarnessWithAutoPlan(t)
	h.available = []tools.Entry{testToolEntry("read_file"), testToolEntry("write_file")}
	h.llm.responses = []agent.CompletionResponse{
		{AssistantText: `{"reason":"missing requires plan","confidence":"high"}`, FinishReason: agent.FinishReasonStop},
	}

	_, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "do work", Events: h.captureEvent})
	if err == nil || !strings.Contains(err.Error(), "route plan mode: router did not call decide_plan_mode exactly once") {
		t.Fatalf("Run error = %v, want clear routing error", err)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventRunFailed}) {
		t.Fatalf("events = %v, want run_failed only", got)
	}
}

func TestRunnerAutoPlanRejectsWrongRoutingTool(t *testing.T) {
	h := newRunnerHarnessWithAutoPlan(t)
	h.llm.responses = []agent.CompletionResponse{{
		ToolCalls: []agent.ToolCall{{
			ID:        "route-1",
			Name:      "some_other_tool",
			Arguments: json.RawMessage(`{"requires_plan":false,"reason":"simple","confidence":"high","signals":[]}`),
		}},
		FinishReason: agent.FinishReasonToolCalls,
	}}

	_, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "do work"})
	if err == nil || !strings.Contains(err.Error(), `route plan mode: router called "some_other_tool", want decide_plan_mode`) {
		t.Fatalf("Run error = %v, want wrong-tool routing error", err)
	}
}

func TestRunnerAutoPlanRejectsMalformedRoutingArguments(t *testing.T) {
	tests := []struct {
		name      string
		argument  json.RawMessage
		wantError string
	}{
		{
			name:      "invalid JSON",
			argument:  json.RawMessage(`not-json`),
			wantError: "route plan mode: decode decide_plan_mode arguments:",
		},
		{
			name:      "invalid confidence",
			argument:  json.RawMessage(`{"requires_plan":false,"reason":"simple","confidence":"certain","signals":[]}`),
			wantError: `route plan mode: invalid plan routing confidence: "certain"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newRunnerHarnessWithAutoPlan(t)
			h.llm.responses = []agent.CompletionResponse{{
				ToolCalls: []agent.ToolCall{{
					ID:        "route-1",
					Name:      "decide_plan_mode",
					Arguments: tt.argument,
				}},
				FinishReason: agent.FinishReasonToolCalls,
			}}

			_, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "do work"})
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Run error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestRunnerLoopGuardFailsDeterministically(t *testing.T) {
	h := newRunnerHarnessWithMaxRounds(t, 1)
	h.available = []tools.Entry{testToolEntry("read_file")}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{ToolCalls: []agent.ToolCall{{ID: "call-2", Name: "read_file", Arguments: json.RawMessage(`{}`)}}, FinishReason: agent.FinishReasonToolCalls},
	}
	h.executor.results = []agent.ToolExecutionResult{{Success: true, Status: "succeeded", Payload: json.RawMessage(`{"ok":true}`)}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "loop", Events: h.captureEvent})
	if !errors.Is(err, agent.ErrMaxToolRoundsExceeded) {
		t.Fatalf("Run error = %v, want ErrMaxToolRoundsExceeded", err)
	}
	if result.Completed {
		t.Fatalf("result = %+v, want failed completion", result)
	}
	if !strings.Contains(result.Error, "limit=1") {
		t.Fatalf("run result error = %q, want limit", result.Error)
	}
	if got := eventTypes(h.events); got[len(got)-1] != agent.EventRunFailed {
		t.Fatalf("last event = %v, want run_failed", got)
	}
	if got := executedNames(h.executor.requests); !reflect.DeepEqual(got, []string{"read_file"}) {
		t.Fatalf("executed tools = %v, want only first tool round", got)
	}
	messages, err := h.history.Load()
	if err != nil {
		t.Fatalf("Load history: %v", err)
	}
	for i, message := range messages {
		if message.Role == agent.RoleAssistant && len(message.ToolCalls) != 0 {
			if i+1 >= len(messages) || messages[i+1].Role != agent.RoleTool {
				t.Fatalf("assistant tool call at history index %d has no following tool result: %+v", i, messages)
			}
		}
	}
}

func TestRunnerRepeatedIdenticalToolFailuresWarnAndStop(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("write_plan")}
	failed := agent.ToolExecutionResult{
		Success: false,
		Status:  "invalid_plan",
		Content: "Plan is missing required heading: Changes",
		Error:   "Plan is missing required heading: Changes",
	}
	h.executor.results = []agent.ToolExecutionResult{failed, failed, failed}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "write_plan", Arguments: json.RawMessage(`{"plan":"bad"}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{ToolCalls: []agent.ToolCall{{ID: "call-2", Name: "write_plan", Arguments: json.RawMessage(`{"plan":"bad"}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{ToolCalls: []agent.ToolCall{{ID: "call-3", Name: "write_plan", Arguments: json.RawMessage(`{"plan":"bad"}`)}}, FinishReason: agent.FinishReasonToolCalls},
	}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "loop", Events: h.captureEvent})
	if !errors.Is(err, agent.ErrRepeatedToolFailure) {
		t.Fatalf("Run error = %v, want ErrRepeatedToolFailure", err)
	}
	if result.Completed {
		t.Fatalf("result = %+v, want failed", result)
	}
	results := toolResultEvents(h.events)
	if len(results) < 3 {
		t.Fatalf("tool results = %d, want at least 3", len(results))
	}
	foundWarning := false
	for _, result := range results {
		if strings.Contains(result.Content, "Do not retry the same call unchanged") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("tool results = %+v, want repeated failure warning", results)
	}
	if got := eventTypes(h.events); got[len(got)-1] != agent.EventRunFailed {
		t.Fatalf("last event = %v, want run_failed", got)
	}
}

func TestRunnerRepeatedCommandFailuresIncludeCommandInKey(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("execute_command")}
	failed := func(command string) agent.ToolExecutionResult {
		return agent.ToolExecutionResult{
			Success: false,
			Status:  "exited_non_zero",
			Content: "failed",
			Error:   "exit status 1",
			Payload: json.RawMessage(`{"success":false,"status":"exited_non_zero","error":"exit status 1","command":` + strconv.Quote(command) + `}`),
		}
	}
	h.executor.results = []agent.ToolExecutionResult{failed("false"), failed("go test ./missing")}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "execute_command", Arguments: json.RawMessage(`{"command":"false"}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{ToolCalls: []agent.ToolCall{{ID: "call-2", Name: "execute_command", Arguments: json.RawMessage(`{"command":"go test ./missing"}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "done", FinishReason: agent.FinishReasonStop},
	}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "run", Events: h.captureEvent}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, result := range toolResultEvents(h.events) {
		if strings.Contains(result.Content, "Do not retry the same call unchanged") {
			t.Fatalf("tool results = %+v, unrelated commands should not trigger repeated warning", toolResultEvents(h.events))
		}
	}
}

func TestRunnerRecordsManagedWorkspaceMutationBoundary(t *testing.T) {
	h := newRunnerHarness(t)
	if err := h.state.AppendCommandEvidence(agent.CommandEvidenceRecord{
		Command: "go test ./...", Status: "succeeded", Executed: true, Succeeded: true,
	}); err != nil {
		t.Fatalf("AppendCommandEvidence: %v", err)
	}
	h.available = []tools.Entry{testToolEntry("write_file")}
	h.executor.results = []agent.ToolExecutionResult{{
		Success: true,
		Status:  "created",
		Payload: json.RawMessage(`{"success":true,"status":"created","created":true}`),
	}}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"x.txt","content":"x"}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "done", FinishReason: agent.FinishReasonStop},
	}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "write", Events: h.captureEvent}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	evidence, err := h.state.ReadCommandEvidence()
	if err != nil {
		t.Fatalf("ReadCommandEvidence: %v", err)
	}
	if evidence.LastWorkspaceMutationRevision != 1 || evidence.LastWorkspaceMutationTool != "write_file" || evidence.LastWorkspaceMutationAt == "" {
		t.Fatalf("evidence = %+v, want managed mutation boundary at existing command revision", evidence)
	}
}

func TestRunnerDoesNotRecordExecuteCommandMutationBoundary(t *testing.T) {
	h := newRunnerHarness(t)
	if err := h.state.AppendCommandEvidence(agent.CommandEvidenceRecord{
		Command: "go test ./...", Status: "succeeded", Executed: true, Succeeded: true,
	}); err != nil {
		t.Fatalf("AppendCommandEvidence: %v", err)
	}
	h.available = []tools.Entry{testToolEntry("execute_command")}
	h.executor.results = []agent.ToolExecutionResult{{
		Success: true,
		Status:  "succeeded",
		Payload: json.RawMessage(`{"success":true,"executed":true,"command":"go test ./..."}`),
	}}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "execute_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "done", FinishReason: agent.FinishReasonStop},
	}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "test", Events: h.captureEvent}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	evidence, err := h.state.ReadCommandEvidence()
	if err != nil {
		t.Fatalf("ReadCommandEvidence: %v", err)
	}
	if evidence.LastWorkspaceMutationRevision != 0 || evidence.LastWorkspaceMutationTool != "" {
		t.Fatalf("evidence = %+v, want execute_command not to set mutation boundary", evidence)
	}
}

func TestRunnerPlanModeExitsAfterAcceptedPlan(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("write_plan"), testToolEntry("write_file")}
	if err := h.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	h.executor.results = []agent.ToolExecutionResult{{
		Success: true,
		Status:  "ok",
		Content: "Plan written successfully.",
		Payload: json.RawMessage(`{"success":true,"plan_written":true,"todos_written":true,"plan_valid":true,"todos_valid":true,"ready_for_implementation":true}`),
	}}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "write_plan", Arguments: json.RawMessage(`{"plan":"# Goal\nOk\n# Files\nOk\n# Changes\nOk\n# Verification\nOk"}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "implementing", FinishReason: agent.FinishReasonStop},
	}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "plan"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.mode.Current() != agent.ModeDefault {
		t.Fatalf("mode = %q, want default", h.mode.Current())
	}
	if got := toolDefinitionNames(h.llm.requests[1].Tools); !reflect.DeepEqual(got, []string{"write_file"}) {
		t.Fatalf("post-plan tools = %v, want default-mode implementation tools", got)
	}
}

func TestRunnerPlanModeRemainsAfterFailedWritePlan(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("write_plan"), testToolEntry("write_file")}
	if err := h.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	h.executor.results = []agent.ToolExecutionResult{{
		Success: false,
		Status:  "invalid_plan",
		Content: "Plan is missing required heading: Changes",
		Error:   "Plan is missing required heading: Changes",
		Payload: json.RawMessage(`{"success":false,"status":"invalid_plan","error":"Plan is missing required heading: Changes"}`),
	}}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "write_plan", Arguments: json.RawMessage(`{"plan":"bad"}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "still planning", FinishReason: agent.FinishReasonStop},
	}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "plan"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.mode.Current() != agent.ModePlan {
		t.Fatalf("mode = %q, want plan", h.mode.Current())
	}
	if got := toolDefinitionNames(h.llm.requests[1].Tools); !reflect.DeepEqual(got, []string{"write_plan"}) {
		t.Fatalf("post-failed-plan tools = %v, want still plan-mode restricted", got)
	}
}

func TestRunnerPlanModeDoesNotExitAfterWriteTodos(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("write_plan"), testToolEntry("write_todos"), testToolEntry("write_file")}
	if err := h.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	h.executor.results = []agent.ToolExecutionResult{{
		Success: true,
		Status:  "ok",
		Content: "Todos written successfully.",
		Payload: json.RawMessage(`{"success":true,"todos_written":true,"todos_valid":true}`),
	}}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "write_todos", Arguments: json.RawMessage(`{"todos":[]}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "still planning", FinishReason: agent.FinishReasonStop},
	}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "plan"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.mode.Current() != agent.ModePlan {
		t.Fatalf("mode = %q, want plan", h.mode.Current())
	}
	if got := toolDefinitionNames(h.llm.requests[1].Tools); !reflect.DeepEqual(got, []string{"write_plan", "write_todos"}) {
		t.Fatalf("post-write_todos tools = %v, want still plan-mode tools", got)
	}
}

func TestRunnerLLMErrorEmitsRunFailed(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.err = errors.New("llm unavailable")

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "hello", Events: h.captureEvent})
	if err == nil || !strings.Contains(err.Error(), "llm unavailable") {
		t.Fatalf("Run error = %v, want llm unavailable", err)
	}
	if result.Completed || result.Error != "llm unavailable" {
		t.Fatalf("result = %+v, want failed llm result", result)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventRunFailed}) {
		t.Fatalf("events = %v, want run_failed only", got)
	}
}

func TestRunnerRuntimeDependencyErrorEmitsRunFailed(t *testing.T) {
	h := newRunnerHarness(t)
	h.toolErr = errors.New("tool registry unavailable")

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "hello", Events: h.captureEvent})
	if err == nil || !strings.Contains(err.Error(), "tool registry unavailable") {
		t.Fatalf("Run error = %v, want tool registry unavailable", err)
	}
	if result.Completed || result.Error != "tool registry unavailable" {
		t.Fatalf("result = %+v, want failed runtime result", result)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventRunFailed}) {
		t.Fatalf("events = %v, want run_failed only", got)
	}
}

func TestRunnerCompletionCallbackBehavior(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "finished", FinishReason: agent.FinishReasonStop}}
	called := false
	var callbackResult agent.RunResult

	result, err := h.runner.Run(context.Background(), agent.RunRequest{
		Prompt: "finish",
		Events: h.captureEvent,
		OnCompleted: func(_ context.Context, result agent.RunResult) error {
			called = true
			callbackResult = result
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("completion callback was not invoked")
	}
	if !callbackResult.Completed || callbackResult.FinalOutput != "finished" {
		t.Fatalf("callback result = %+v", callbackResult)
	}
	if result != callbackResult {
		t.Fatalf("run result = %+v, callback result = %+v", result, callbackResult)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventAssistantText, agent.EventRunCompleted}) {
		t.Fatalf("events = %v, want assistant_text, run_completed", got)
	}
}

func TestRunnerCompletionCallbackSuccessEmitsRunCompleted(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "finished", FinishReason: agent.FinishReasonStop}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{
		Prompt: "finish",
		Events: h.captureEvent,
		OnCompleted: func(context.Context, agent.RunResult) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "finished" {
		t.Fatalf("result = %+v, want success", result)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventAssistantText, agent.EventRunCompleted}) {
		t.Fatalf("events = %v, want only assistant_text/run_completed", got)
	}
}

func TestRunnerAbsentCompletionCallbackIsNoOp(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "finished", FinishReason: agent.FinishReasonStop}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "finish", Events: h.captureEvent})
	if err != nil {
		t.Fatalf("Run without callback: %v", err)
	}
	if !result.Completed || result.FinalOutput != "finished" {
		t.Fatalf("result = %+v, want completed", result)
	}
}

func TestRunnerCompletionCallbackErrorFailsRunDeterministically(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "finished", FinishReason: agent.FinishReasonStop}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{
		Prompt: "finish",
		Events: h.captureEvent,
		OnCompleted: func(context.Context, agent.RunResult) error {
			return errors.New("host callback failed")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "completion callback: host callback failed") {
		t.Fatalf("Run error = %v, want callback failure", err)
	}
	if result.Completed || !strings.Contains(result.Error, "completion callback: host callback failed") {
		t.Fatalf("result = %+v, want callback failure result", result)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventAssistantText, agent.EventRunFailed}) {
		t.Fatalf("events = %v, want assistant_text, run_failed only", got)
	}
}

func TestRunnerCompletionCallbackErrorEmitsRunFailedOnly(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "finished", FinishReason: agent.FinishReasonStop}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{
		Prompt: "finish",
		Events: h.captureEvent,
		OnCompleted: func(context.Context, agent.RunResult) error {
			return errors.New("callback boom")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "completion callback: callback boom") {
		t.Fatalf("Run error = %v, want callback boom", err)
	}
	if result.Completed || result.FinalOutput != "finished" || !strings.Contains(result.Error, "callback boom") {
		t.Fatalf("result = %+v, want failed callback result preserving output", result)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventAssistantText, agent.EventRunFailed}) {
		t.Fatalf("events = %v, want no run_completed before callback failure", got)
	}
}

func TestRunnerCompletionCallbackReceivesFinalOutput(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "final answer", FinishReason: agent.FinishReasonStop}}
	var received agent.RunResult

	_, err := h.runner.Run(context.Background(), agent.RunRequest{
		Prompt: "finish",
		OnCompleted: func(_ context.Context, result agent.RunResult) error {
			received = result
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !received.Completed || received.FinalOutput != "final answer" || received.Error != "" {
		t.Fatalf("callback result = %+v, want successful final output", received)
	}
}

func TestRunnerCallbackNotInvokedOnFailedRun(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.err = errors.New("llm failed")
	called := false

	_, err := h.runner.Run(context.Background(), agent.RunRequest{
		Prompt: "fail",
		OnCompleted: func(context.Context, agent.RunResult) error {
			called = true
			return nil
		},
	})
	if err == nil {
		t.Fatal("Run succeeded, want failure")
	}
	if called {
		t.Fatal("completion callback should not run on failed run")
	}
}

func TestRunnerCompletionCallbackNotCalledOnLLMFailure(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.err = errors.New("llm failed")
	called := false

	result, err := h.runner.Run(context.Background(), agent.RunRequest{
		Prompt: "fail",
		Events: h.captureEvent,
		OnCompleted: func(context.Context, agent.RunResult) error {
			called = true
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "llm failed") {
		t.Fatalf("Run error = %v, want llm failed", err)
	}
	if called {
		t.Fatal("completion callback should not be called on LLM failure")
	}
	if result.Completed {
		t.Fatalf("result = %+v, want failed", result)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventRunFailed}) {
		t.Fatalf("events = %v, want run_failed only", got)
	}
}

func TestRunnerCompletionHasNoSubmitWorkflowOrTodoPlanVerificationGate(t *testing.T) {
	h := newRunnerHarness(t)
	if err := h.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	h.llm.responses = []agent.CompletionResponse{{AssistantText: "normal finish", FinishReason: agent.FinishReasonStop}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "finish in plan"})
	if err != nil {
		t.Fatalf("Run should complete without submit/approval/verification gates: %v", err)
	}
	if !result.Completed || result.FinalOutput != "normal finish" {
		t.Fatalf("result = %+v, want normal completion", result)
	}
}

func TestRunnerEmptyFinalAssistantTextCompletesDeterministically(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.responses = []agent.CompletionResponse{{FinishReason: agent.FinishReasonStop}}

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "empty", Events: h.captureEvent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "" || result.Error != "" {
		t.Fatalf("result = %+v, want completed empty output", result)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventRunCompleted}) {
		t.Fatalf("events = %v, want run_completed only", got)
	}
}

func TestRunnerStreamingLLMEmitsChunksAndPersistsFinalMessageOnce(t *testing.T) {
	h := newRunnerHarness(t)
	streaming := &streamingSequenceLLM{
		turns: []streamingTurn{
			{
				chunks: []string{"hello ", "there"},
				response: agent.CompletionResponse{
					AssistantText: "hello there",
					FinishReason:  agent.FinishReasonStop,
				},
			},
		},
	}
	h.llm = nil
	runner, err := agent.NewRunner(agent.RunnerConfig{
		LLM:     streaming,
		History: h.history,
		Mode:    h.mode,
		Tools: agent.ActiveToolProviderFunc(func(context.Context) ([]tools.Entry, error) {
			return nil, nil
		}),
		Executor: h.executor,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	result, err := runner.Run(context.Background(), agent.RunRequest{Prompt: "stream", Events: h.captureEvent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "hello there" {
		t.Fatalf("result = %+v, want streamed final output", result)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{
		agent.EventAssistantText,
		agent.EventAssistantText,
		agent.EventRunCompleted,
	}) {
		t.Fatalf("events = %v, want two chunks and completion", got)
	}
	if h.events[0].Text != "hello " || h.events[1].Text != "there" {
		t.Fatalf("streamed event texts = %q, %q", h.events[0].Text, h.events[1].Text)
	}
	messages, err := h.history.Load()
	if err != nil {
		t.Fatalf("Load history: %v", err)
	}
	assistantCount := 0
	for _, message := range messages {
		if message.Role == agent.RoleAssistant {
			assistantCount++
			if message.Content != "hello there" {
				t.Fatalf("assistant message content = %q, want final response", message.Content)
			}
		}
	}
	if assistantCount != 1 {
		t.Fatalf("assistant messages persisted = %d, want 1", assistantCount)
	}
}

func TestRunnerStreamingLLMUsesStreamedTextWhenFinalResponseIsEmpty(t *testing.T) {
	h := newRunnerHarness(t)
	streaming := &streamingSequenceLLM{
		turns: []streamingTurn{
			{
				chunks: []string{"he", "llo"},
				response: agent.CompletionResponse{
					FinishReason: agent.FinishReasonStop,
				},
			},
		},
	}
	runner, err := agent.NewRunner(agent.RunnerConfig{
		LLM:     streaming,
		History: h.history,
		Mode:    h.mode,
		Tools: agent.ActiveToolProviderFunc(func(context.Context) ([]tools.Entry, error) {
			return nil, nil
		}),
		Executor: h.executor,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	result, err := runner.Run(context.Background(), agent.RunRequest{Prompt: "stream only", Events: h.captureEvent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalOutput != "hello" {
		t.Fatalf("final output = %q, want streamed text", result.FinalOutput)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{
		agent.EventAssistantText,
		agent.EventAssistantText,
		agent.EventRunCompleted,
	}) {
		t.Fatalf("events = %v, want chunks without duplicate final assistant event", got)
	}
	messages, err := h.history.Load()
	if err != nil {
		t.Fatalf("Load history: %v", err)
	}
	if messages[len(messages)-1].Role != agent.RoleAssistant || messages[len(messages)-1].Content != "hello" {
		t.Fatalf("last message = %+v, want streamed assistant text persisted", messages[len(messages)-1])
	}
}

func TestRunnerStreamingLLMToolCallsStillExecuteAfterText(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("read_file")}
	streaming := &streamingSequenceLLM{
		turns: []streamingTurn{
			{
				chunks: []string{"checking"},
				response: agent.CompletionResponse{
					AssistantText: "checking",
					ToolCalls: []agent.ToolCall{{
						ID:        "call-1",
						Name:      "read_file",
						Arguments: json.RawMessage(`{"path":"README.md"}`),
					}},
					FinishReason: agent.FinishReasonToolCalls,
				},
			},
			{
				chunks: []string{"done"},
				response: agent.CompletionResponse{
					AssistantText: "done",
					FinishReason:  agent.FinishReasonStop,
				},
			},
		},
	}
	runner, err := agent.NewRunner(agent.RunnerConfig{
		LLM:     streaming,
		History: h.history,
		Mode:    h.mode,
		Tools: agent.ActiveToolProviderFunc(func(context.Context) ([]tools.Entry, error) {
			return append([]tools.Entry(nil), h.available...), nil
		}),
		Executor: h.executor,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	result, err := runner.Run(context.Background(), agent.RunRequest{Prompt: "stream tool", Events: h.captureEvent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalOutput != "done" {
		t.Fatalf("final output = %q, want done", result.FinalOutput)
	}
	if got := executedNames(h.executor.requests); !reflect.DeepEqual(got, []string{"read_file"}) {
		t.Fatalf("executed tools = %v, want streamed tool call executed", got)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{
		agent.EventAssistantText,
		agent.EventToolCall,
		agent.EventToolResult,
		agent.EventAssistantText,
		agent.EventRunCompleted,
	}) {
		t.Fatalf("events = %v, want streamed text, tool events, final stream, completion", got)
	}
}

func TestRunnerContextCancellationBehavior(t *testing.T) {
	h := newRunnerHarness(t)
	h.llm.err = context.Canceled

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "cancel", Events: h.captureEvent})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if result.Completed || result.Error != context.Canceled.Error() {
		t.Fatalf("result = %+v, want canceled failure", result)
	}
	if got := eventTypes(h.events); !reflect.DeepEqual(got, []agent.EventType{agent.EventRunFailed}) {
		t.Fatalf("events = %v, want run_failed", got)
	}
}

type runnerHarness struct {
	llm           *sequenceLLM
	history       *agent.HistoryManager
	state         *agent.ConversationState
	mode          *agent.ModeState
	todos         *agent.TodoManager
	executor      *recordingToolExecutor
	runner        *agent.Runner
	available     []tools.Entry
	events        []agent.Event
	toolErr       error
	maxToolRounds int
}

func newRunnerHarness(t *testing.T) *runnerHarness {
	t.Helper()
	return newRunnerHarnessWithMaxRounds(t, 8)
}

func newRunnerHarnessWithMaxRounds(t *testing.T, maxRounds int) *runnerHarness {
	t.Helper()
	return newRunnerHarnessWithConfig(t, maxRounds, nil)
}

func newRunnerHarnessWithPlanRouter(t *testing.T, router agent.PlanRouter) *runnerHarness {
	t.Helper()
	return newRunnerHarnessWithConfig(t, 8, router)
}

func newRunnerHarnessWithConfig(t *testing.T, maxRounds int, router agent.PlanRouter) *runnerHarness {
	t.Helper()
	return newRunnerHarnessWithConfigAndAutoPlan(t, maxRounds, router, false)
}

func newRunnerHarnessWithAutoPlan(t *testing.T) *runnerHarness {
	t.Helper()
	return newRunnerHarnessWithConfigAndAutoPlan(t, 8, nil, true)
}

func newRunnerHarnessWithConfigAndAutoPlan(t *testing.T, maxRounds int, router agent.PlanRouter, autoPlan bool) *runnerHarness {
	t.Helper()

	layout := testAgentLayout(t)
	planStore := store.NewPlanStore(layout)
	todoStore := store.NewTodoStore(layout)
	memoryStore := store.NewMemoryStore(layout)
	commandEvidenceStore := store.NewCommandEvidenceStore(layout)
	h := &runnerHarness{
		llm:           &sequenceLLM{},
		mode:          agent.NewModeState(planStore),
		todos:         agent.NewTodoManager(todoStore),
		state:         agent.NewConversationState(planStore, todoStore, memoryStore, commandEvidenceStore),
		executor:      &recordingToolExecutor{},
		available:     []tools.Entry{},
		maxToolRounds: maxRounds,
	}
	h.history = agent.NewHistoryManager(
		store.NewHistoryStore(layout),
		memoryStore,
		agent.WithModeState(h.mode),
		agent.WithTodoStore(todoStore),
	)
	runner, err := agent.NewRunner(agent.RunnerConfig{
		LLM:     h.llm,
		History: h.history,
		State:   h.state,
		Mode:    h.mode,
		Tools: agent.ActiveToolProviderFunc(func(context.Context) ([]tools.Entry, error) {
			if h.toolErr != nil {
				return nil, h.toolErr
			}
			return append([]tools.Entry(nil), h.available...), nil
		}),
		Executor:      h.executor,
		MaxToolRounds: maxRounds,
		PlanRouter:    router,
		AutoPlanMode:  autoPlan,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	h.runner = runner
	return h
}

func (h *runnerHarness) captureEvent(event agent.Event) {
	h.events = append(h.events, event)
}

type sequenceLLM struct {
	requests  []agent.CompletionRequest
	responses []agent.CompletionResponse
	err       error
}

func (l *sequenceLLM) Complete(_ context.Context, request agent.CompletionRequest) (agent.CompletionResponse, error) {
	l.requests = append(l.requests, request)
	if l.err != nil {
		return agent.CompletionResponse{}, l.err
	}
	if len(l.responses) == 0 {
		return agent.CompletionResponse{AssistantText: "default", FinishReason: agent.FinishReasonStop}, nil
	}
	response := l.responses[0]
	l.responses = l.responses[1:]
	return response, nil
}

type streamingSequenceLLM struct {
	requests []agent.CompletionRequest
	turns    []streamingTurn
	err      error
}

type streamingTurn struct {
	chunks   []string
	response agent.CompletionResponse
}

func (l *streamingSequenceLLM) Complete(context.Context, agent.CompletionRequest) (agent.CompletionResponse, error) {
	panic("Complete should not be called for streamingSequenceLLM")
}

func (l *streamingSequenceLLM) StreamComplete(_ context.Context, request agent.CompletionRequest, sink agent.AssistantTextSink) (agent.CompletionResponse, error) {
	l.requests = append(l.requests, request)
	if l.err != nil {
		return agent.CompletionResponse{}, l.err
	}
	if len(l.turns) == 0 {
		return agent.CompletionResponse{AssistantText: "default", FinishReason: agent.FinishReasonStop}, nil
	}
	turn := l.turns[0]
	l.turns = l.turns[1:]
	for _, chunk := range turn.chunks {
		if sink != nil {
			sink(chunk)
		}
	}
	return turn.response, nil
}

type recordingToolExecutor struct {
	requests []agent.ToolExecutionRequest
	results  []agent.ToolExecutionResult
	err      error
}

func (e *recordingToolExecutor) ExecuteTool(_ context.Context, request agent.ToolExecutionRequest) (agent.ToolExecutionResult, error) {
	e.requests = append(e.requests, request)
	if e.err != nil {
		return agent.ToolExecutionResult{}, e.err
	}
	if len(e.results) == 0 {
		return agent.ToolExecutionResult{Success: true, Status: "succeeded", Payload: json.RawMessage(`{"ok":true}`)}, nil
	}
	result := e.results[0]
	e.results = e.results[1:]
	return result, nil
}

func testToolEntry(name string) tools.Entry {
	entry := tools.Entry{
		Name:        name,
		Description: name + " tool",
		InputSchema: []byte(`{"type":"object"}`),
		Category:    "files",
		Runtime:     "builtin",
	}
	switch name {
	case "read_file", "read_files", "glob", "grep", "read_plan", "write_plan", "read_todos", "write_todos", "read_command_evidence", "submit_plan_implementation", "read_memory", "update_memory":
		entry.Safety.PlanSafe = true
	}
	return entry
}

func eventTypes(events []agent.Event) []agent.EventType {
	types := make([]agent.EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func executedNames(requests []agent.ToolExecutionRequest) []string {
	names := make([]string, 0, len(requests))
	for _, request := range requests {
		names = append(names, request.Name)
	}
	return names
}

func toolDefinitionNames(definitions []agent.ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func findSystemMessageContaining(messages []agent.Message, needle string) string {
	for _, message := range messages {
		if message.Role == agent.RoleSystem && strings.Contains(message.Content, needle) {
			return message.Content
		}
	}
	return ""
}

func routingResponse(requiresPlan bool, reason, confidence string, signals []string) agent.CompletionResponse {
	if signals == nil {
		signals = []string{}
	}
	arguments, err := json.Marshal(map[string]any{
		"requires_plan": requiresPlan,
		"reason":        reason,
		"confidence":    confidence,
		"signals":       signals,
	})
	if err != nil {
		panic(err)
	}
	return agent.CompletionResponse{
		ToolCalls: []agent.ToolCall{{
			ID:        "route-1",
			Name:      "decide_plan_mode",
			Arguments: arguments,
		}},
		FinishReason: agent.FinishReasonToolCalls,
	}
}

func assertSharedMessagePrefix(t *testing.T, first, second []agent.Message) {
	t.Helper()
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("empty message list: first=%d second=%d", len(first), len(second))
	}
	prefixLen := len(first) - 1
	if len(second) < prefixLen {
		t.Fatalf("second message length = %d, want at least shared prefix %d", len(second), prefixLen)
	}
	for i := 0; i < prefixLen; i++ {
		if !reflect.DeepEqual(first[i], second[i]) {
			t.Fatalf("message prefix mismatch at %d:\nfirst=%+v\nsecond=%+v", i, first[i], second[i])
		}
	}
}

func firstToolResultEvent(t *testing.T, events []agent.Event) agent.ToolResult {
	t.Helper()

	results := toolResultEvents(events)
	if len(results) == 0 {
		t.Fatal("no tool result events")
	}
	return results[0]
}

func toolResultEvents(events []agent.Event) []agent.ToolResult {
	results := make([]agent.ToolResult, 0)
	for _, event := range events {
		if event.Type == agent.EventToolResult && event.ToolResult != nil {
			results = append(results, *event.ToolResult)
		}
	}
	return results
}
