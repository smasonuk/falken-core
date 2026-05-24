package falken

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/smasonuk/falken-core/internal/store"
)

func TestInMemoryStateBackendDefensiveCopies(t *testing.T) {
	backend := NewInMemoryStateBackend()
	input := []byte("hello")
	if err := backend.Write("key", input); err != nil {
		t.Fatalf("Write: %v", err)
	}
	input[0] = 'j'

	got, found, err := backend.Read("key")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !found {
		t.Fatal("Read found = false, want true")
	}
	if string(got) != "hello" {
		t.Fatalf("stored value = %q, want hello", string(got))
	}

	got[0] = 'y'
	again, found, err := backend.Read("key")
	if err != nil {
		t.Fatalf("Read again: %v", err)
	}
	if !found {
		t.Fatal("Read again found = false, want true")
	}
	if string(again) != "hello" {
		t.Fatalf("stored value after mutating read buffer = %q, want hello", string(again))
	}
}

func TestInMemoryStateBackendConcurrentAccess(t *testing.T) {
	backend := NewInMemoryStateBackend()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := backend.Write("key", []byte("value")); err != nil {
				t.Errorf("Write: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, _, err := backend.Read("key"); err != nil {
				t.Errorf("Read: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestSessionUsesInMemoryStateBackend(t *testing.T) {
	setStateHomeEnv(t)

	backend := NewInMemoryStateBackend()
	session, err := New(Config{
		WorkspaceDir: tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{
			Mode: ExecutionModeLocal,
		},
		LLM:                  noopLLM{},
		StateBackendProvider: staticStateBackendProvider{backend: backend},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "remember this"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	historyData, found, err := backend.Read(store.BlobKeyHistory)
	if err != nil {
		t.Fatalf("read history blob: %v", err)
	}
	if !found {
		t.Fatal("history blob missing")
	}
	var entries []string
	if err := json.Unmarshal(historyData, &entries); err != nil {
		t.Fatalf("decode history blob: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("history entries length = 0, want entries")
	}

	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}
	plan := "# Goal\nImplement memory-backed state wiring with enough detail for validation.\n\n# Files\n- pkg/falken/state_memory.go\n- pkg/falken/state_memory_test.go\n\n# Changes\n1. Use an injected byte backend for conversation state.\n2. Verify plan and todo state round-trips through the session APIs.\n\n# Verification\nRun go test ./pkg/falken."
	todos := []Todo{{ID: "t1", Content: "wire backend", Status: "completed"}}
	if err := session.WritePlanAndTodos(plan, todos); err != nil {
		t.Fatalf("WritePlanAndTodos: %v", err)
	}
	gotPlan, err := session.ReadPlan()
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if gotPlan != plan {
		t.Fatalf("plan = %q, want %q", gotPlan, plan)
	}
	gotTodos, err := session.ReadTodos()
	if err != nil {
		t.Fatalf("ReadTodos: %v", err)
	}
	if !reflect.DeepEqual(gotTodos, todos) {
		t.Fatalf("todos = %#v, want %#v", gotTodos, todos)
	}

	if session.resources.agentRunner == nil {
		t.Fatal("agent runner is unavailable")
	}

	memoryStore := store.NewBlobMemoryStore(backend)
	if err := memoryStore.Write(store.MemoryState{Entries: []string{"memory note"}}); err != nil {
		t.Fatalf("write memory blob: %v", err)
	}
	memory, err := session.ReadMemory()
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	if !reflect.DeepEqual(memory.Entries, []string{"memory note"}) {
		t.Fatalf("memory entries = %#v, want note", memory.Entries)
	}

	evidenceStore := store.NewBlobCommandEvidenceStore(backend)
	if err := evidenceStore.Write(store.CommandEvidenceState{Records: []store.CommandEvidenceRecord{{Command: "go test ./...", Status: "succeeded", RecordedAt: "2026-01-02T03:04:05Z"}}}); err != nil {
		t.Fatalf("write command evidence blob: %v", err)
	}
	evidence, err := evidenceStore.Read()
	if err != nil {
		t.Fatalf("read command evidence blob: %v", err)
	}
	if len(evidence.Records) != 1 {
		t.Fatalf("command evidence records = %d, want 1", len(evidence.Records))
	}
}

func TestNewInMemoryStateBackendProviderCreatesSeparateStores(t *testing.T) {
	setStateHomeEnv(t)

	provider := NewInMemoryStateBackendProvider()
	firstLLM := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "first", FinishReason: FinishReasonStop}}}
	first := newTestSessionWithConfig(t, SessionConfig{
		LLM:                  firstLLM,
		StateBackendProvider: provider,
	})
	if err := first.Start(); err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if _, err := first.Run(context.Background(), RunRequest{Prompt: "first prompt"}); err != nil {
		t.Fatalf("Run first: %v", err)
	}

	secondLLM := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "second", FinishReason: FinishReasonStop}}}
	second := newTestSessionWithConfig(t, SessionConfig{
		LLM:                  secondLLM,
		StateBackendProvider: provider,
	})
	if err := second.Start(); err != nil {
		t.Fatalf("Start second: %v", err)
	}
	if _, err := second.Run(context.Background(), RunRequest{Prompt: "second prompt"}); err != nil {
		t.Fatalf("Run second: %v", err)
	}

	firstHistory := loadSessionAgentHistory(t, first)
	secondHistory := loadSessionAgentHistory(t, second)
	if len(firstHistory) != 3 || firstHistory[1].Content != "first prompt" {
		t.Fatalf("first history = %+v, want only first session prompt", firstHistory)
	}
	if len(secondHistory) != 3 || secondHistory[1].Content != "second prompt" {
		t.Fatalf("second history = %+v, want only second session prompt", secondHistory)
	}
}

type staticStateBackendProvider struct {
	backend StateBackend
}

func (p staticStateBackendProvider) NewStateBackend(StateBackendRequest) (StateBackend, error) {
	return p.backend, nil
}
