package agent_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestMemoryManagerReadLegacyAndStructuredMemory(t *testing.T) {
	layout := testAgentLayout(t)
	memoryStore := store.NewMemoryStore(layout)
	if err := os.MkdirAll(filepath.Dir(layout.MemoryPath), 0o755); err != nil {
		t.Fatalf("mkdir memory parent: %v", err)
	}
	if err := os.WriteFile(layout.MemoryPath, []byte(`{"entries":[" legacy note ","legacy note"]}`), 0o600); err != nil {
		t.Fatalf("write legacy memory: %v", err)
	}

	got, err := agent.NewMemoryManager(memoryStore).Read()
	if err != nil {
		t.Fatalf("Read legacy: %v", err)
	}
	if !reflect.DeepEqual(got.Entries, []string{"legacy note"}) {
		t.Fatalf("entries = %#v, want normalized legacy note", got.Entries)
	}

	if err := memoryStore.Write(store.MemoryState{
		CurrentGoal:    " add search tools ",
		ImportantFiles: []string{" internal/builtintools/registry.go "},
		Decisions:      []string{"Search does not issue read tokens."},
		OpenQuestions:  []string{"Which docs need examples?"},
	}); err != nil {
		t.Fatalf("Write structured: %v", err)
	}
	structured, err := agent.NewMemoryManager(memoryStore).Read()
	if err != nil {
		t.Fatalf("Read structured: %v", err)
	}
	if structured.CurrentGoal != "add search tools" || structured.ImportantFiles[0] != "internal/builtintools/registry.go" {
		t.Fatalf("structured memory = %+v, want trimmed fields", structured)
	}
}

func TestRenderMemoryEmptyLegacyAndStructured(t *testing.T) {
	if empty := agent.RenderMemory(store.MemoryState{}); empty != "--- CURRENT AGENT MEMORY ---\n(no memory entries)" {
		t.Fatalf("empty render = %q", empty)
	}

	legacy := agent.RenderMemory(store.MemoryState{Entries: []string{"fact"}})
	if legacy != "--- CURRENT AGENT MEMORY ---\nNotes:\n- fact" {
		t.Fatalf("legacy render = %q", legacy)
	}

	structured := agent.RenderMemory(store.MemoryState{
		CurrentGoal:    "finish built-ins",
		ImportantFiles: []string{"internal/builtintools/registry.go"},
		Decisions:      []string{"Keep search read-only."},
		OpenQuestions:  []string{"Need public API?"},
		Entries:        []string{"Legacy note."},
	})
	for _, want := range []string{"Current goal:\n- finish built-ins", "Important files:\n- internal/builtintools/registry.go", "Decisions:\n- Keep search read-only.", "Open questions:\n- Need public API?", "Notes:\n- Legacy note."} {
		if !strings.Contains(structured, want) {
			t.Fatalf("structured render = %q, missing %q", structured, want)
		}
	}
}

func TestMemoryManagerUpdateMergeRemovalAndClearGoal(t *testing.T) {
	layout := testAgentLayout(t)
	manager := agent.NewMemoryManager(store.NewMemoryStore(layout))

	first, err := manager.Update(agent.MemoryUpdate{
		CurrentGoal:       " Add memory tools ",
		AddEntries:        []string{" note one ", "note one", ""},
		AddImportantFiles: []string{" internal/agent/memory.go "},
		AddDecisions:      []string{"Use merge semantics."},
		AddOpenQuestions:  []string{"What docs need updates?"},
	})
	if err != nil {
		t.Fatalf("Update first: %v", err)
	}
	if first.CurrentGoal != "Add memory tools" || len(first.Entries) != 1 || len(first.ImportantFiles) != 1 {
		t.Fatalf("first memory = %+v, want normalized merge", first)
	}

	second, err := manager.Update(agent.MemoryUpdate{
		ClearCurrentGoal:     true,
		AddEntries:           []string{"note two"},
		RemoveEntries:        []string{"note one"},
		AddImportantFiles:    []string{"pkg/falken/memory.go"},
		RemoveImportantFiles: []string{"internal/agent/memory.go"},
		AddDecisions:         []string{"Expose public read only."},
		RemoveDecisions:      []string{"Use merge semantics."},
		AddOpenQuestions:     []string{"Any migration notes?"},
		RemoveOpenQuestions:  []string{"What docs need updates?"},
	})
	if err != nil {
		t.Fatalf("Update second: %v", err)
	}
	want := store.MemoryState{
		Entries:        []string{"note two"},
		ImportantFiles: []string{"pkg/falken/memory.go"},
		Decisions:      []string{"Expose public read only."},
		OpenQuestions:  []string{"Any migration notes?"},
	}
	if !agent.MemoryEqual(second, want) {
		t.Fatalf("second memory = %+v, want %+v", second, want)
	}
}

func TestMemoryManagerValidationLimits(t *testing.T) {
	layout := testAgentLayout(t)
	manager := agent.NewMemoryManager(store.NewMemoryStore(layout))

	_, err := manager.Update(agent.MemoryUpdate{CurrentGoal: strings.Repeat("x", 501)})
	if err == nil || !strings.Contains(err.Error(), "current_goal exceeds maximum length") {
		t.Fatalf("long goal error = %v, want descriptive current_goal limit", err)
	}

	many := make([]string, 51)
	for i := range many {
		many[i] = "entry " + strconv.Itoa(i)
	}
	_, err = manager.Update(agent.MemoryUpdate{AddEntries: many})
	if err == nil || !strings.Contains(err.Error(), "entries has too many items") {
		t.Fatalf("too many entries error = %v, want descriptive limit", err)
	}
}
