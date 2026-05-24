package agent_test

import (
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestValidatePlanTodosComplete(t *testing.T) {
	tests := []struct {
		name       string
		todos      []agent.Todo
		wantAccept bool
		wantStatus string
	}{
		{name: "empty", wantAccept: true, wantStatus: "accepted"},
		{
			name: "pending",
			todos: []agent.Todo{{
				ID: "t1", Content: "Add tests", Status: agent.TodoStatusPending,
			}},
			wantStatus: "blocked_incomplete_todos",
		},
		{
			name: "completed",
			todos: []agent.Todo{{
				ID: "t1", Content: "Run tests", Status: agent.TodoStatusCompleted,
			}},
			wantAccept: true,
			wantStatus: "accepted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agent.ValidatePlanTodosComplete(tt.todos)
			if got.Accepted != tt.wantAccept || got.Status != tt.wantStatus {
				t.Fatalf("check = %+v, want accepted=%v status=%s", got, tt.wantAccept, tt.wantStatus)
			}
			if !tt.wantAccept && len(got.Blockers) == 0 {
				t.Fatalf("blockers = nil, want actionable blocker")
			}
		})
	}
}

func TestCompletionDecisionFromReview(t *testing.T) {
	completedTodos := []agent.Todo{{ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted}}
	sufficient := agent.CommandEvidenceReview{Verdict: "sufficient", Confidence: "high", Reason: "go test passed"}
	notApplicable := agent.CommandEvidenceReview{Verdict: "not_applicable", Confidence: "high", Reason: "no verification needed"}
	insufficient := agent.CommandEvidenceReview{Verdict: "insufficient", Confidence: "medium", Reason: "only ls ran"}
	unclear := agent.CommandEvidenceReview{Verdict: "unclear", Confidence: "low", Reason: "reviewer error"}

	tests := []struct {
		name       string
		review     agent.CommandEvidenceReview
		attempts   int
		wantStatus string
		wantAccept bool
	}{
		{name: "sufficient", review: sufficient, wantStatus: "accepted", wantAccept: true},
		{name: "not applicable", review: notApplicable, wantStatus: "accepted", wantAccept: true},
		{name: "insufficient first", review: insufficient, wantStatus: "blocked_verification_review"},
		{name: "insufficient second", review: insufficient, attempts: 1, wantStatus: "accepted_with_verification_warning", wantAccept: true},
		{name: "unclear first", review: unclear, wantStatus: "blocked_verification_review"},
		{name: "unclear second", review: unclear, attempts: 1, wantStatus: "accepted_with_verification_warning", wantAccept: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agent.CompletionDecisionFromReview(completedTodos, tt.review, tt.attempts)
			if got.Accepted != tt.wantAccept || got.Status != tt.wantStatus {
				t.Fatalf("check = %+v, want accepted=%v status=%s", got, tt.wantAccept, tt.wantStatus)
			}
		})
	}

	pendingTodos := []agent.Todo{{ID: "t1", Content: "Implement", Status: agent.TodoStatusPending}}
	got := agent.CompletionDecisionFromReview(pendingTodos, sufficient, 0)
	if got.Accepted || got.Status != "blocked_incomplete_todos" {
		t.Fatalf("incomplete todo check = %+v, want incomplete todos to block before review verdict", got)
	}
}

func TestCommandEvidenceManagerAppendAssignsIncreasingRevisions(t *testing.T) {
	layout := testAgentLayout(t)
	manager := agent.NewCommandEvidenceManager(store.NewCommandEvidenceStore(layout))

	if err := manager.Append(agent.CommandEvidenceRecord{
		Command: "go test ./...", Status: "succeeded", Executed: true, Succeeded: true,
	}); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if err := manager.Append(agent.CommandEvidenceRecord{
		Command: "go vet ./...", Status: "succeeded", Executed: true, Succeeded: true,
	}); err != nil {
		t.Fatalf("second Append: %v", err)
	}
	state, err := manager.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(state.Records) != 2 || state.Records[0].Revision != 1 || state.Records[1].Revision != 2 {
		t.Fatalf("records = %+v, want increasing revisions", state.Records)
	}
}

func TestCommandEvidenceManagerRecordsPlanBaselineAndWorkspaceMutation(t *testing.T) {
	layout := testAgentLayout(t)
	manager := agent.NewCommandEvidenceManager(store.NewCommandEvidenceStore(layout))

	if err := manager.Append(agent.CommandEvidenceRecord{
		Command: "go test ./...", Status: "succeeded", Executed: true, Succeeded: true,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := manager.RecordReview(agent.CommandEvidenceReview{Verdict: "unclear", Confidence: "low", Reason: "stale"}); err != nil {
		t.Fatalf("RecordReview: %v", err)
	}
	if err := manager.RecordPlanBaseline(); err != nil {
		t.Fatalf("RecordPlanBaseline: %v", err)
	}
	state, err := manager.Read()
	if err != nil {
		t.Fatalf("Read baseline: %v", err)
	}
	if state.PlanBaselineRevision != 1 || state.LastWorkspaceMutationRevision != 0 || state.ReviewAttempts != 0 || state.LastReview != nil {
		t.Fatalf("baseline state = %+v, want baseline at revision 1 and review reset", state)
	}

	if _, err := manager.RecordWorkspaceMutation("edit_file", "2026-01-02T03:04:05Z"); err != nil {
		t.Fatalf("RecordWorkspaceMutation: %v", err)
	}
	state, err = manager.Read()
	if err != nil {
		t.Fatalf("Read mutation: %v", err)
	}
	if state.LastWorkspaceMutationRevision != 1 || state.LastWorkspaceMutationTool != "edit_file" || state.LastWorkspaceMutationAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("mutation state = %+v, want mutation boundary at current max revision", state)
	}
}

func TestCommandEvidenceManagerPlanBaselineAssignsMissingRevisions(t *testing.T) {
	layout := testAgentLayout(t)
	evidenceStore := store.NewCommandEvidenceStore(layout)
	if err := evidenceStore.Write(agent.CommandEvidenceState{
		Records: []agent.CommandEvidenceRecord{{
			Command: "legacy test", Status: "succeeded", Executed: true, Succeeded: true,
		}},
	}); err != nil {
		t.Fatalf("Write legacy evidence: %v", err)
	}
	manager := agent.NewCommandEvidenceManager(evidenceStore)

	if err := manager.RecordPlanBaseline(); err != nil {
		t.Fatalf("RecordPlanBaseline: %v", err)
	}
	state, err := manager.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if state.PlanBaselineRevision != 1 || len(state.Records) != 1 || state.Records[0].Revision != 1 {
		t.Fatalf("state = %+v, want missing revision assigned before baseline", state)
	}
}

func TestCommandEvidenceManagerReviewAttemptsAndResetKeepRecords(t *testing.T) {
	layout := testAgentLayout(t)
	manager := agent.NewCommandEvidenceManager(store.NewCommandEvidenceStore(layout))

	if err := manager.Append(agent.CommandEvidenceRecord{
		Command: "go test ./...", Status: "succeeded", Executed: true, Succeeded: true,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	state, err := manager.RecordReview(agent.CommandEvidenceReview{Verdict: "unclear", Confidence: "low", Reason: "no command after edit"})
	if err != nil {
		t.Fatalf("RecordReview: %v", err)
	}
	if len(state.Records) != 1 || state.Records[0].Revision != 1 {
		t.Fatalf("records = %+v, want one record with revision 1", state.Records)
	}
	if state.ReviewAttempts != 1 || state.LastReview == nil || state.LastReview.Verdict != "unclear" {
		t.Fatalf("review state = %+v, want one unclear attempt", state)
	}

	state, err = manager.RecordReview(agent.CommandEvidenceReview{Verdict: "sufficient", Confidence: "high", Reason: "go test passed"})
	if err != nil {
		t.Fatalf("RecordReview sufficient: %v", err)
	}
	if state.ReviewAttempts != 1 || state.LastReview == nil || state.LastReview.Verdict != "sufficient" {
		t.Fatalf("review state = %+v, want sufficient review without increment", state)
	}
	if len(state.Records) != 1 {
		t.Fatalf("records after review = %+v, want historical records retained", state.Records)
	}

	if err := manager.ResetReviewAttempts(); err != nil {
		t.Fatalf("ResetReviewAttempts: %v", err)
	}
	reset, err := manager.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(reset.Records) != 1 {
		t.Fatalf("records after reset = %+v, want historical records retained", reset.Records)
	}
	if reset.ReviewAttempts != 0 || reset.LastReview != nil {
		t.Fatalf("reset state = %+v, want review state cleared", reset)
	}
}
