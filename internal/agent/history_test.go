package agent_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestHistoryManagerLoadMissingExistingAndMalformedHistory(t *testing.T) {
	layout := testAgentLayout(t)
	historyStore := store.NewHistoryStore(layout)
	memoryStore := store.NewMemoryStore(layout)
	manager := agent.NewHistoryManager(historyStore, memoryStore)

	missing, err := manager.Load()
	if err != nil {
		t.Fatalf("Load missing history: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing history length = %d, want 0", len(missing))
	}

	entries := []string{
		mustHistoryEntry(t, agent.UserMessage("hello")),
		mustHistoryEntry(t, agent.AssistantMessage("hi")),
	}
	if err := historyStore.Write(entries); err != nil {
		t.Fatalf("Write history: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("Load existing history: %v", err)
	}
	if len(loaded) != 2 || loaded[0].Role != agent.RoleUser || loaded[1].Content != "hi" {
		t.Fatalf("loaded history = %+v, want user then assistant", loaded)
	}

	if err := historyStore.Write([]string{"not-json"}); err != nil {
		t.Fatalf("Write malformed history: %v", err)
	}
	if _, err := manager.Load(); err == nil || !strings.Contains(err.Error(), "decode history entry 0") {
		t.Fatalf("Load malformed history error = %v, want deterministic decode error", err)
	}
}

func TestHistoryManagerAppendPersistsMessages(t *testing.T) {
	layout := testAgentLayout(t)
	manager := agent.NewHistoryManager(store.NewHistoryStore(layout), store.NewMemoryStore(layout))

	call := agent.ToolCall{
		ID:        "call-1",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"README.md"}`),
	}
	if err := manager.Append(agent.SystemMessage("system prompt")); err != nil {
		t.Fatalf("Append system: %v", err)
	}
	if err := manager.Append(agent.UserMessage("read README")); err != nil {
		t.Fatalf("Append user: %v", err)
	}
	if err := manager.Append(agent.AssistantMessage("checking", call)); err != nil {
		t.Fatalf("Append assistant: %v", err)
	}
	if err := manager.Append(agent.ToolResultMessage(agent.ToolResult{
		CallID:  "call-1",
		Name:    "read_file",
		Content: "contents",
		Payload: json.RawMessage(`{"bytes":8}`),
	})); err != nil {
		t.Fatalf("Append tool result: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("Load appended history: %v", err)
	}
	if len(loaded) != 4 {
		t.Fatalf("loaded history length = %d, want 4", len(loaded))
	}
	if loaded[0].Role != agent.RoleSystem || loaded[0].Content != "system prompt" {
		t.Fatalf("system message = %+v", loaded[0])
	}
	if loaded[1].Role != agent.RoleUser || loaded[1].Content != "read README" {
		t.Fatalf("user message = %+v", loaded[1])
	}
	if loaded[2].Role != agent.RoleAssistant || len(loaded[2].ToolCalls) != 1 || loaded[2].ToolCalls[0].Name != "read_file" {
		t.Fatalf("assistant message = %+v", loaded[2])
	}
	if loaded[3].Role != agent.RoleTool || loaded[3].ToolResult == nil || loaded[3].ToolResult.CallID != "call-1" {
		t.Fatalf("tool result message = %+v", loaded[3])
	}
}

func TestHistoryManagerPrepareRunWithEmptyHistoryAndMemory(t *testing.T) {
	layout := testAgentLayout(t)
	manager := agent.NewHistoryManager(store.NewHistoryStore(layout), store.NewMemoryStore(layout))

	messages, err := manager.PrepareRun(agent.PrepareRunRequest{
		BaseSystemPrompt: "You are Falken.",
		UserPrompt:       "Hello",
	})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("prepared message length = %d, want 2", len(messages))
	}
	if messages[0].Role != agent.RoleSystem || !strings.Contains(messages[0].Content, "You are Falken.") {
		t.Fatalf("system message = %+v", messages[0])
	}
	if !strings.Contains(messages[0].Content, "(no memory entries)") {
		t.Fatalf("system message missing empty memory section: %q", messages[0].Content)
	}
	if messages[1].Role != agent.RoleUser || messages[1].Content != "Hello" {
		t.Fatalf("user message = %+v", messages[1])
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("Load persisted prepared history: %v", err)
	}
	if len(loaded) != 2 || loaded[0].Content != messages[0].Content || loaded[1].Content != "Hello" {
		t.Fatalf("persisted prepared history = %+v", loaded)
	}
}

func TestHistoryManagerPrepareRunRefreshesSystemMemoryWithoutDuplication(t *testing.T) {
	layout := testAgentLayout(t)
	historyStore := store.NewHistoryStore(layout)
	memoryStore := store.NewMemoryStore(layout)
	manager := agent.NewHistoryManager(historyStore, memoryStore)

	if err := memoryStore.Write(store.MemoryState{Entries: []string{"first fact"}}); err != nil {
		t.Fatalf("Write memory: %v", err)
	}
	first, err := manager.PrepareRun(agent.PrepareRunRequest{
		BaseSystemPrompt: "Base prompt",
		UserPrompt:       "first prompt",
	})
	if err != nil {
		t.Fatalf("PrepareRun first: %v", err)
	}
	if !strings.Contains(first[0].Content, "- first fact") {
		t.Fatalf("first system prompt = %q, want first fact", first[0].Content)
	}

	if err := memoryStore.Write(store.MemoryState{Entries: []string{"second fact"}}); err != nil {
		t.Fatalf("Write refreshed memory: %v", err)
	}
	second, err := manager.PrepareRun(agent.PrepareRunRequest{
		BaseSystemPrompt: "Base prompt",
		UserPrompt:       "second prompt",
	})
	if err != nil {
		t.Fatalf("PrepareRun second: %v", err)
	}

	if len(second) != 3 {
		t.Fatalf("second prepared length = %d, want refreshed system plus two users", len(second))
	}
	if strings.Count(second[0].Content, "--- CURRENT AGENT MEMORY ---") != 1 {
		t.Fatalf("system prompt duplicated memory section: %q", second[0].Content)
	}
	if strings.Contains(second[0].Content, "first fact") || !strings.Contains(second[0].Content, "second fact") {
		t.Fatalf("system prompt = %q, want refreshed second fact only", second[0].Content)
	}
	if second[1].Content != "first prompt" || second[2].Content != "second prompt" {
		t.Fatalf("prepared history order = %+v", second)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("Load refreshed history: %v", err)
	}
	if len(loaded) != 3 || loaded[0].Content != second[0].Content {
		t.Fatalf("persisted refreshed history = %+v", loaded)
	}
}

func TestRenderMemoryIsDeterministic(t *testing.T) {
	empty := agent.RenderMemory(store.MemoryState{})
	if empty != "--- CURRENT AGENT MEMORY ---\n(no memory entries)" {
		t.Fatalf("empty memory render = %q", empty)
	}

	populated := agent.RenderMemory(store.MemoryState{Entries: []string{"zeta", "alpha"}})
	want := "--- CURRENT AGENT MEMORY ---\nNotes:\n- zeta\n- alpha"
	if populated != want {
		t.Fatalf("populated memory render = %q, want %q", populated, want)
	}

	system := agent.RenderSystemPrompt("Base\n", store.MemoryState{Entries: []string{"fact"}})
	if system != "Base\n\n--- CURRENT AGENT MEMORY ---\nNotes:\n- fact" {
		t.Fatalf("system prompt = %q", system)
	}
}

func TestHistoryManagerPrepareRunIncludesActivePlanContext(t *testing.T) {
	layout := testAgentLayout(t)
	planStore := store.NewPlanStore(layout)
	mode := agent.NewModeState(planStore)
	if err := mode.Plan().Write(validImplementationPlanForTest()); err != nil {
		t.Fatalf("Write plan: %v", err)
	}
	manager := agent.NewHistoryManager(
		store.NewHistoryStore(layout),
		store.NewMemoryStore(layout),
		agent.WithModeState(mode),
	)

	messages, err := manager.PrepareRun(agent.PrepareRunRequest{
		BaseSystemPrompt: "Base",
		UserPrompt:       "continue",
	})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	if !strings.Contains(messages[0].Content, "--- CURRENT PLAN ---") || !strings.Contains(messages[0].Content, "Implement the requested runtime mode behavior") {
		t.Fatalf("system prompt missing inline active plan: %q", messages[0].Content)
	}
}

func TestRenderActivePlanSummarizesLargePlans(t *testing.T) {
	got := agent.RenderActivePlan("# Goal\n" + strings.Repeat("large\n", 400))
	if !strings.Contains(got, "active plan exists; use read_plan") || strings.Contains(got, "large\nlarge\nlarge") {
		t.Fatalf("large plan render = %q", got)
	}
	if empty := agent.RenderActivePlan(""); !strings.Contains(empty, "(no active plan)") {
		t.Fatalf("empty plan render = %q", empty)
	}
}

func TestHistoryManagerCompactionSeam(t *testing.T) {
	layout := testAgentLayout(t)
	historyStore := store.NewHistoryStore(layout)
	memoryStore := store.NewMemoryStore(layout)
	called := false
	manager := agent.NewHistoryManager(historyStore, memoryStore, agent.WithCompactor(agent.MessageCompactorFunc(func(messages []agent.Message) ([]agent.Message, error) {
		called = true
		if len(messages) != 3 {
			return nil, errors.New("unexpected message count")
		}

		return []agent.Message{messages[0], messages[2]}, nil
	})))

	if err := historyStore.Write([]string{mustHistoryEntry(t, agent.UserMessage("old prompt"))}); err != nil {
		t.Fatalf("Write history: %v", err)
	}
	messages, err := manager.PrepareRun(agent.PrepareRunRequest{
		BaseSystemPrompt: "Base",
		UserPrompt:       "new prompt",
	})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	if !called {
		t.Fatal("compactor was not called")
	}
	if len(messages) != 2 || messages[0].Role != agent.RoleSystem || messages[1].Content != "new prompt" {
		t.Fatalf("compacted messages = %+v", messages)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("Load compacted history: %v", err)
	}
	if len(loaded) != 2 || loaded[1].Content != "new prompt" {
		t.Fatalf("persisted compacted history = %+v", loaded)
	}
}

func mustHistoryEntry(t *testing.T, message agent.Message) string {
	t.Helper()

	data, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal history message: %v", err)
	}

	return string(data)
}

func testAgentLayout(t *testing.T) state.Layout {
	t.Helper()

	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	stateRoot := filepath.Join(t.TempDir(), "state")
	layout, err := state.ResolveLayout(workspaceRoot, stateRoot)
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}

	return layout
}
