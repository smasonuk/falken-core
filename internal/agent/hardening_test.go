package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestAgentContextHardening_PreparationRefreshesAllRuntimeSections(t *testing.T) {
	layout := testAgentLayout(t)
	historyStore := store.NewHistoryStore(layout)
	memoryStore := store.NewMemoryStore(layout)
	todoStore := store.NewTodoStore(layout)
	mode := agent.NewModeState(store.NewPlanStore(layout))
	manager := agent.NewHistoryManager(
		historyStore,
		memoryStore,
		agent.WithTodoStore(todoStore),
		agent.WithModeState(mode),
	)

	if err := memoryStore.Write(store.MemoryState{Entries: []string{"remember alpha"}}); err != nil {
		t.Fatalf("Write memory: %v", err)
	}
	todos := agent.NewTodoManager(todoStore)
	if err := todos.Replace([]agent.Todo{{ID: "task-1", Content: "Inspect only", Status: agent.TodoStatusInProgress}}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}

	first, err := manager.PrepareRun(agent.PrepareRunRequest{BaseSystemPrompt: "Base", UserPrompt: "first"})
	if err != nil {
		t.Fatalf("PrepareRun first: %v", err)
	}
	assertSectionCount(t, first[0].Content, "--- CURRENT MODE ---", 0)
	assertSectionCount(t, first[0].Content, "--- CURRENT AGENT MEMORY ---", 1)
	assertSectionCount(t, first[0].Content, "--- CURRENT TODOS ---", 1)
	for _, want := range []string{"- remember alpha", "[>] task-1: Inspect only"} {
		if !strings.Contains(first[0].Content, want) {
			t.Fatalf("first system prompt missing %q: %q", want, first[0].Content)
		}
	}

	if err := memoryStore.Write(store.MemoryState{Entries: []string{"remember beta"}}); err != nil {
		t.Fatalf("Write refreshed memory: %v", err)
	}
	if err := todos.Replace([]agent.Todo{{ID: "task-2", Content: "Write plan", Status: agent.TodoStatusPending}}); err != nil {
		t.Fatalf("Replace refreshed todos: %v", err)
	}
	if err := mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	second, err := manager.PrepareRun(agent.PrepareRunRequest{BaseSystemPrompt: "Base", UserPrompt: "second"})
	if err != nil {
		t.Fatalf("PrepareRun second: %v", err)
	}
	assertSectionCount(t, second[0].Content, "--- CURRENT MODE ---", 0)
	assertSectionCount(t, second[0].Content, "--- CURRENT AGENT MEMORY ---", 1)
	assertSectionCount(t, second[0].Content, "--- CURRENT TODOS ---", 1)
	for _, stale := range []string{"remember alpha", "task-1", "Plan mode is read-only", "mode: plan"} {
		if strings.Contains(second[0].Content, stale) {
			t.Fatalf("second system prompt retained stale %q: %q", stale, second[0].Content)
		}
	}
	for _, want := range []string{"- remember beta", "[ ] task-2: Write plan"} {
		if !strings.Contains(second[0].Content, want) {
			t.Fatalf("second system prompt missing %q: %q", want, second[0].Content)
		}
	}
	if second[1].Content != "first" || second[2].Content != "second" {
		t.Fatalf("prepared history ordering = %+v", second)
	}
}

func TestAgentHistoryHardening_AssistantAndToolResultsPersistInOrder(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("read_file")}
	h.llm.responses = []agent.CompletionResponse{
		{
			AssistantText: "checking",
			ToolCalls: []agent.ToolCall{{
				ID:        "call-1",
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"a"}`),
			}},
			FinishReason: agent.FinishReasonToolCalls,
		},
		{AssistantText: "final", FinishReason: agent.FinishReasonStop},
	}
	h.executor.results = []agent.ToolExecutionResult{{Success: true, Status: "succeeded", Payload: json.RawMessage(`{"ok":true}`)}}

	if _, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "start"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	history, err := h.history.Load()
	if err != nil {
		t.Fatalf("Load history: %v", err)
	}
	got := messageRoles(history)
	want := []agent.Role{agent.RoleSystem, agent.RoleUser, agent.RoleAssistant, agent.RoleTool, agent.RoleAssistant}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history roles = %v, want %v", got, want)
	}
	if history[2].Content != "checking" || len(history[2].ToolCalls) != 1 {
		t.Fatalf("assistant tool-call message = %+v", history[2])
	}
	if history[3].ToolResult == nil || history[3].ToolResult.CallID != "call-1" || history[4].Content != "final" {
		t.Fatalf("history = %+v, want tool result before final assistant", history)
	}
}

func TestAgentModePolicyHardening_V1ModesAndPlanRestrictions(t *testing.T) {
	available := []tools.Entry{
		{Name: "read_file", Category: "files", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true}},
		{Name: "write_file", Category: "files", Runtime: "builtin"},
		{Name: "edit_file", Category: "files", Runtime: "builtin"},
		{Name: "apply_patch", Category: "files", Runtime: "builtin"},
		{Name: "shell_execute", Category: "command", Runtime: "builtin"},
		{Name: "start_background_process", Category: "command", Runtime: "builtin"},
		{Name: "write_plan", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true}},
		{Name: "read_todos", Category: "todo", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true}},
	}

	defaultTools, err := agent.FilterTools(agent.ModeDefault, available)
	if err != nil {
		t.Fatalf("FilterTools default: %v", err)
	}
	if len(defaultTools) != len(available)-1 {
		t.Fatalf("default tools = %d, want %d", len(defaultTools), len(available)-1)
	}

	planTools, err := agent.FilterTools(agent.ModePlan, available)
	if err != nil {
		t.Fatalf("FilterTools plan: %v", err)
	}
	got := toolNames(planTools)
	want := []string{"read_file", "write_plan", "read_todos"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan tools = %v, want %v", got, want)
	}
	for _, name := range []string{"write_file", "edit_file", "apply_patch", "shell_execute", "start_background_process"} {
		decision := agent.CheckToolCall(agent.ModePlan, name, available)
		if decision.Allowed || decision.Reason == "" {
			t.Fatalf("%s decision = %+v, want blocked with reason", name, decision)
		}
	}
	for _, unsupported := range []agent.Mode{"verify", "explore"} {
		if _, err := agent.FilterTools(unsupported, available); !errors.Is(err, agent.ErrUnsupportedMode) {
			t.Fatalf("FilterTools(%q) error = %v, want ErrUnsupportedMode", unsupported, err)
		}
	}
}

func TestAgentLoopHardening_ToolExecutionErrorIsToolResultNotRunFailure(t *testing.T) {
	h := newRunnerHarness(t)
	h.available = []tools.Entry{testToolEntry("read_file")}
	h.llm.responses = []agent.CompletionResponse{
		{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{}`)}}, FinishReason: agent.FinishReasonToolCalls},
		{AssistantText: "recovered", FinishReason: agent.FinishReasonStop},
	}
	h.executor.err = errors.New("tool runtime failed")

	result, err := h.runner.Run(context.Background(), agent.RunRequest{Prompt: "call", Events: h.captureEvent})
	if err != nil {
		t.Fatalf("Run should continue after tool execution error: %v", err)
	}
	if !result.Completed || result.FinalOutput != "recovered" {
		t.Fatalf("result = %+v, want recovered completion", result)
	}
	got := eventTypes(h.events)
	want := []agent.EventType{agent.EventToolCall, agent.EventToolResult, agent.EventAssistantText, agent.EventRunCompleted}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	toolResult := firstToolResultEvent(t, h.events)
	if !strings.Contains(toolResult.Error, "tool runtime failed") || !strings.Contains(string(toolResult.Payload), "execution_error") {
		t.Fatalf("tool result = %+v, want structured execution error", toolResult)
	}
}

func TestAgentEventsHardening_CommandChunkAndThoughtEventsAreConstructible(t *testing.T) {
	thought := agent.ThoughtEvent("considering")
	if thought.Type != agent.EventThought || thought.Text != "considering" {
		t.Fatalf("thought event = %+v", thought)
	}
	// Command chunks are part of the stable event model, even though the v1 core loop
	// does not synthesize them directly unless lower-level execution surfaces emit them.
	chunk := agent.CommandChunkEvent(testStreamChunk("hello"))
	if chunk.Type != agent.EventCommandChunk || chunk.CommandChunk == nil || string(chunk.CommandChunk.Data) != "hello" {
		t.Fatalf("command chunk event = %+v", chunk)
	}
}

func TestAgentRequiresSubmissionWhenTodosExist(t *testing.T) {
	layout := testAgentLayout(t)
	todoStore := store.NewTodoStore(layout)
	if err := agent.NewTodoManager(todoStore).Replace([]agent.Todo{
		{ID: "task-1", Content: "Still pending", Status: agent.TodoStatusPending},
	}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}
	history := agent.NewHistoryManager(
		store.NewHistoryStore(layout),
		store.NewMemoryStore(layout),
		agent.WithTodoStore(todoStore),
	)
	llm := &sequenceLLM{responses: []agent.CompletionResponse{{AssistantText: "normal completion", FinishReason: agent.FinishReasonStop}}}
	runner, err := agent.NewRunner(agent.RunnerConfig{
		LLM:     llm,
		History: history,
		Tools:   agent.ActiveToolProviderFunc(func(context.Context) ([]tools.Entry, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	result, err := runner.Run(context.Background(), agent.RunRequest{Prompt: "finish"})
	if !errors.Is(err, agent.ErrImplementationSubmissionRequired) {
		t.Fatalf("Run error = %v, want implementation submission required failure", err)
	}
	if result.Completed {
		t.Fatalf("result = %+v, want completion blocked", result)
	}
}

func assertSectionCount(t *testing.T, text, section string, want int) {
	t.Helper()
	if got := strings.Count(text, section); got != want {
		t.Fatalf("%q count = %d, want %d in %q", section, got, want, text)
	}
}

func messageRoles(messages []agent.Message) []agent.Role {
	roles := make([]agent.Role, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, message.Role)
	}
	return roles
}

func testStreamChunk(text string) runtimeexec.StreamChunk {
	return runtimeexec.StreamChunk{Stream: runtimeexec.StreamStdout, Data: []byte(text)}
}
