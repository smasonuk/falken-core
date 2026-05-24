package agent_test

import (
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestConversationStateWritePlanAndTodosIsAtomic(t *testing.T) {
	layout := testAgentLayout(t)
	state := agent.NewConversationState(store.NewPlanStore(layout), store.NewTodoStore(layout), store.NewMemoryStore(layout))
	if err := state.WritePlanAndTodos(validImplementationPlanForTest(), []agent.Todo{{ID: " task-1 ", Content: " Inspect ", Status: agent.TodoStatusPending}}); err != nil {
		t.Fatalf("WritePlanAndTodos: %v", err)
	}
	todos, err := state.ReadTodos()
	if err != nil {
		t.Fatalf("ReadTodos: %v", err)
	}
	if len(todos) != 1 || todos[0].ID != "task-1" || todos[0].Content != "Inspect" {
		t.Fatalf("todos = %+v, want normalized values", todos)
	}

	err = state.WritePlanAndTodos("# Goal\nToo short", []agent.Todo{{ID: "task-2", Content: "Task", Status: agent.TodoStatusPending}})
	if err == nil {
		t.Fatal("WritePlanAndTodos invalid plan succeeded")
	}
	plan, err := state.ReadPlan()
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if !strings.Contains(plan, "Implement the requested runtime mode behavior") {
		t.Fatalf("plan changed after failed transaction: %q", plan)
	}
}

func TestConversationStatePlanLifecycleResetsReviewAttemptsOnly(t *testing.T) {
	layout := testAgentLayout(t)
	state := agent.NewConversationState(
		store.NewPlanStore(layout),
		store.NewTodoStore(layout),
		store.NewMemoryStore(layout),
		store.NewCommandEvidenceStore(layout),
	)
	if err := state.AppendCommandEvidence(agent.CommandEvidenceRecord{Command: "go test ./...", Status: "succeeded", Executed: true, Succeeded: true}); err != nil {
		t.Fatalf("AppendCommandEvidence: %v", err)
	}
	if _, err := state.RecordCommandEvidenceReview(agent.CommandEvidenceReview{Verdict: "unclear", Confidence: "low", Reason: "no verification"}); err != nil {
		t.Fatalf("RecordCommandEvidenceReview: %v", err)
	}

	if err := state.WritePlanAndTodos(validImplementationPlanForTest(), []agent.Todo{{ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted}}); err != nil {
		t.Fatalf("WritePlanAndTodos: %v", err)
	}
	evidence, err := state.ReadCommandEvidence()
	if err != nil {
		t.Fatalf("ReadCommandEvidence after write plan: %v", err)
	}
	if len(evidence.Records) != 1 || evidence.ReviewAttempts != 0 || evidence.LastReview != nil {
		t.Fatalf("evidence after write plan = %+v, want records kept and review reset", evidence)
	}
	if _, err := state.RecordCommandEvidenceReview(agent.CommandEvidenceReview{Verdict: "insufficient", Confidence: "medium", Reason: "still no verification"}); err != nil {
		t.Fatalf("RecordCommandEvidenceReview second: %v", err)
	}

	if err := state.CompletePlanImplementation(); err != nil {
		t.Fatalf("CompletePlanImplementation: %v", err)
	}
	evidence, err = state.ReadCommandEvidence()
	if err != nil {
		t.Fatalf("ReadCommandEvidence after complete: %v", err)
	}
	if len(evidence.Records) != 1 || evidence.ReviewAttempts != 0 || evidence.LastReview != nil {
		t.Fatalf("evidence after complete = %+v, want records kept and review reset", evidence)
	}
	plan, err := state.ReadPlan()
	if err != nil {
		t.Fatalf("ReadPlan after complete: %v", err)
	}
	if strings.TrimSpace(plan) != "" {
		t.Fatalf("plan after complete = %q, want cleared", plan)
	}
	todos, err := state.ReadTodos()
	if err != nil {
		t.Fatalf("ReadTodos after complete: %v", err)
	}
	if len(todos) != 0 {
		t.Fatalf("todos after complete = %+v, want cleared", todos)
	}
}

func TestConversationStateMemoryUpdate(t *testing.T) {
	layout := testAgentLayout(t)
	state := agent.NewConversationState(store.NewPlanStore(layout), store.NewTodoStore(layout), store.NewMemoryStore(layout))

	updated, err := state.UpdateMemory(agent.MemoryUpdate{CurrentGoal: "  ship it  ", AddEntries: []string{" note "}})
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	if updated.CurrentGoal != "ship it" || len(updated.Entries) != 1 || updated.Entries[0] != "note" {
		t.Fatalf("memory = %+v, want normalized update", updated)
	}
}
