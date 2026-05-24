package planningtools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestSubmitPlanImplementationBlocksIncompleteTodosBeforeReviewer(t *testing.T) {
	host, reviewer := submitToolHostForTest(t, []agent.Todo{{
		ID: "t1", Content: "Run tests", Status: agent.TodoStatusPending,
	}}, nil)

	result, err := new(SubmitPlanImplementationTool).Execute(context.Background(), host, json.RawMessage(`{"summary":"done","verification_summary":"not run"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || result.Status != "blocked_incomplete_todos" {
		t.Fatalf("result = %+v, want incomplete todo blocker", result)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSubmitPlanImplementationReviewerVerdicts(t *testing.T) {
	tests := []struct {
		name       string
		reviews    []agent.CommandEvidenceReview
		wantStatus []string
		wantAccept []bool
	}{
		{
			name:       "sufficient accepts",
			reviews:    []agent.CommandEvidenceReview{{Verdict: "sufficient", VerificationPerformed: true, Confidence: "high", Reason: "go test passed"}},
			wantStatus: []string{"accepted"},
			wantAccept: []bool{true},
		},
		{
			name:       "not applicable accepts",
			reviews:    []agent.CommandEvidenceReview{{Verdict: "not_applicable", VerificationPerformed: false, Confidence: "high", Reason: "no workspace changes"}},
			wantStatus: []string{"accepted"},
			wantAccept: []bool{true},
		},
		{
			name: "insufficient blocks then warns",
			reviews: []agent.CommandEvidenceReview{
				{Verdict: "insufficient", VerificationPerformed: false, Confidence: "medium", Reason: "only inspection ran"},
				{Verdict: "insufficient", VerificationPerformed: false, Confidence: "medium", Reason: "still no verification"},
			},
			wantStatus: []string{"blocked_verification_review", "accepted_with_verification_warning"},
			wantAccept: []bool{false, true},
		},
		{
			name: "unclear blocks then warns",
			reviews: []agent.CommandEvidenceReview{
				{Verdict: "unclear", VerificationPerformed: false, Confidence: "low", Reason: "reviewer could not tell"},
				{Verdict: "unclear", VerificationPerformed: false, Confidence: "low", Reason: "still unclear"},
			},
			wantStatus: []string{"blocked_verification_review", "accepted_with_verification_warning"},
			wantAccept: []bool{false, true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, _ := submitToolHostForTest(t, []agent.Todo{{
				ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted,
			}}, tt.reviews)

			for i, wantStatus := range tt.wantStatus {
				result, err := new(SubmitPlanImplementationTool).Execute(context.Background(), host, json.RawMessage(`{"summary":"done","verification_summary":"go test ./... passed"}`))
				if err != nil {
					t.Fatalf("Execute %d: %v", i+1, err)
				}
				if result.Status != wantStatus || result.Success != tt.wantAccept[i] {
					t.Fatalf("result %d = %+v, want status=%s success=%v", i+1, result, wantStatus, tt.wantAccept[i])
				}
			}
		})
	}
}

func TestSubmitPlanImplementationNilReviewerBlocksThenWarns(t *testing.T) {
	host, _ := submitToolHostForTest(t, []agent.Todo{{
		ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted,
	}}, []agent.CommandEvidenceReview{{Verdict: "sufficient", VerificationPerformed: true, Confidence: "high", Reason: "would pass with reviewer"}})
	conv, err := host.RequireConversationState()
	if err != nil {
		t.Fatalf("RequireConversationState: %v", err)
	}
	host = api.NewHost(nil, nil, nil, nil, nil, nil, conv, nil, nil)

	first, err := new(SubmitPlanImplementationTool).Execute(context.Background(), host, json.RawMessage(`{"summary":"done","verification_summary":"go test ./... passed"}`))
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if first.Success || first.Status != "blocked_verification_review" {
		t.Fatalf("first result = %+v, want blocked unclear review", first)
	}
	if !strings.Contains(string(first.Payload), "reviewer is unavailable") {
		t.Fatalf("first payload = %s, want nil reviewer reason", first.Payload)
	}

	second, err := new(SubmitPlanImplementationTool).Execute(context.Background(), host, json.RawMessage(`{"summary":"done","verification_summary":"go test ./... passed"}`))
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if !second.Success || second.Status != "accepted_with_verification_warning" {
		t.Fatalf("second result = %+v, want accepted with warning", second)
	}
}

func TestSubmitPlanImplementationReviewerReceivesLastCommandsOnly(t *testing.T) {
	host, reviewer := submitToolHostForTest(t, []agent.Todo{{
		ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted,
	}}, []agent.CommandEvidenceReview{{Verdict: "sufficient", VerificationPerformed: true, Confidence: "high", Reason: "recent tests passed"}})
	stateService, err := host.RequireConversationState()
	if err != nil {
		t.Fatalf("RequireConversationState: %v", err)
	}
	for i := 1; i <= maxReviewCommands+5; i++ {
		if err := stateService.AppendCommandEvidence(agent.CommandEvidenceRecord{
			Command: "cmd-" + string(rune('A'+i-1)),
			Status:  "succeeded", Executed: true, Succeeded: true,
		}); err != nil {
			t.Fatalf("AppendCommandEvidence %d: %v", i, err)
		}
	}

	result, err := new(SubmitPlanImplementationTool).Execute(context.Background(), host, json.RawMessage(`{"summary":"done","verification_summary":"cmd-Y passed"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %+v, want accepted", result)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("reviewer requests = %d, want 1", len(reviewer.requests))
	}
	request := reviewer.requests[0]
	if len(request.Commands) != maxReviewCommands {
		t.Fatalf("commands reviewed = %d, want %d", len(request.Commands), maxReviewCommands)
	}
	if request.Commands[0].Revision != 7 || request.Commands[len(request.Commands)-1].Revision != int64(maxReviewCommands+6) {
		t.Fatalf("reviewed revisions = first %d last %d, want last %d commands", request.Commands[0].Revision, request.Commands[len(request.Commands)-1].Revision, maxReviewCommands)
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	for _, forbidden := range []string{"Workspace" + "Dirty", "Dirty" + "Revision", "after_latest_" + "workspace_change"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("review request contains dirty field %q: %s", forbidden, data)
		}
	}
}

func TestSelectReviewCommandsFiltersByFreshnessCutoff(t *testing.T) {
	evidence := agent.CommandEvidenceState{
		Records: []agent.CommandEvidenceRecord{
			{Command: "pre-plan", Revision: 1},
			{Command: "before-edit", Revision: 2},
			{Command: "after-edit", Revision: 3},
		},
		PlanBaselineRevision:          1,
		LastWorkspaceMutationRevision: 2,
	}

	commands := selectReviewCommands(evidence)
	if len(commands) != 1 || commands[0].Command != "after-edit" {
		t.Fatalf("commands = %+v, want only command after latest freshness cutoff", commands)
	}
}

func TestSelectReviewCommandsKeepsLegacyZeroRevisionWithoutBoundary(t *testing.T) {
	commands := selectReviewCommands(agent.CommandEvidenceState{
		Records: []agent.CommandEvidenceRecord{{Command: "legacy go test", Revision: 0}},
	})
	if len(commands) != 1 || commands[0].Command != "legacy go test" {
		t.Fatalf("commands = %+v, want legacy command eligible without freshness boundary", commands)
	}
}

func TestSubmitPlanImplementationExcludesPrePlanCommand(t *testing.T) {
	host, reviewer := submitToolHostForTest(t, []agent.Todo{{
		ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted,
	}}, []agent.CommandEvidenceReview{{Verdict: "insufficient", VerificationPerformed: false, Confidence: "medium", Reason: "no fresh command evidence"}})
	stateService, err := host.RequireConversationState()
	if err != nil {
		t.Fatalf("RequireConversationState: %v", err)
	}
	if err := stateService.ClearCommandEvidence(); err != nil {
		t.Fatalf("ClearCommandEvidence: %v", err)
	}
	if err := stateService.AppendCommandEvidence(agent.CommandEvidenceRecord{
		Command: "go test ./...", Status: "succeeded", Executed: true, Succeeded: true,
	}); err != nil {
		t.Fatalf("AppendCommandEvidence: %v", err)
	}
	if err := stateService.WritePlanAndTodos(validSubmitPlanForTest(), []agent.Todo{{
		ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted,
	}}); err != nil {
		t.Fatalf("WritePlanAndTodos: %v", err)
	}

	result, err := new(SubmitPlanImplementationTool).Execute(context.Background(), host, json.RawMessage(`{"summary":"done","verification_summary":"go test ./... passed earlier"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || result.Status != "blocked_verification_review" {
		t.Fatalf("result = %+v, want blocked first attempt", result)
	}
	if len(reviewer.requests) != 1 || len(reviewer.requests[0].Commands) != 0 {
		t.Fatalf("review request commands = %+v, want no eligible commands", reviewer.requests)
	}
	if reviewer.requests[0].PlanBaselineRevision != 1 {
		t.Fatalf("plan baseline = %d, want 1", reviewer.requests[0].PlanBaselineRevision)
	}
}

func TestSubmitPlanImplementationExcludesCommandBeforeFinalMutation(t *testing.T) {
	host, reviewer := submitToolHostForTest(t, []agent.Todo{{
		ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted,
	}}, []agent.CommandEvidenceReview{{Verdict: "insufficient", VerificationPerformed: false, Confidence: "medium", Reason: "no command after final edit"}})
	stateService, err := host.RequireConversationState()
	if err != nil {
		t.Fatalf("RequireConversationState: %v", err)
	}
	if _, err := stateService.RecordWorkspaceMutation("edit_file", "2026-01-02T03:04:05Z"); err != nil {
		t.Fatalf("RecordWorkspaceMutation: %v", err)
	}

	result, err := new(SubmitPlanImplementationTool).Execute(context.Background(), host, json.RawMessage(`{"summary":"done","verification_summary":"go test ./... passed before edit"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || result.Status != "blocked_verification_review" {
		t.Fatalf("result = %+v, want blocked first attempt", result)
	}
	if len(reviewer.requests) != 1 || len(reviewer.requests[0].Commands) != 0 {
		t.Fatalf("review request commands = %+v, want stale command excluded", reviewer.requests)
	}
	request := reviewer.requests[0]
	if request.LastWorkspaceMutationRevision != 1 || request.LastWorkspaceMutationTool != "edit_file" || request.LastWorkspaceMutationAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("freshness metadata = %+v, want mutation boundary metadata", request)
	}
}

func TestSubmitPlanImplementationIncludesCommandAfterFinalMutation(t *testing.T) {
	host, reviewer := submitToolHostForTest(t, []agent.Todo{{
		ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted,
	}}, []agent.CommandEvidenceReview{{Verdict: "sufficient", VerificationPerformed: true, Confidence: "high", Reason: "fresh test passed"}})
	stateService, err := host.RequireConversationState()
	if err != nil {
		t.Fatalf("RequireConversationState: %v", err)
	}
	if err := stateService.ClearCommandEvidence(); err != nil {
		t.Fatalf("ClearCommandEvidence: %v", err)
	}
	if err := stateService.WritePlanAndTodos(validSubmitPlanForTest(), []agent.Todo{{
		ID: "t1", Content: "Implement", Status: agent.TodoStatusCompleted,
	}}); err != nil {
		t.Fatalf("WritePlanAndTodos: %v", err)
	}
	if _, err := stateService.RecordWorkspaceMutation("apply_patch", "2026-01-02T03:04:05Z"); err != nil {
		t.Fatalf("RecordWorkspaceMutation: %v", err)
	}
	if err := stateService.AppendCommandEvidence(agent.CommandEvidenceRecord{
		Command: "go test ./...", Status: "succeeded", Executed: true, Succeeded: true,
	}); err != nil {
		t.Fatalf("AppendCommandEvidence: %v", err)
	}

	result, err := new(SubmitPlanImplementationTool).Execute(context.Background(), host, json.RawMessage(`{"summary":"done","verification_summary":"go test ./... passed"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %+v, want accepted", result)
	}
	if len(reviewer.requests) != 1 || len(reviewer.requests[0].Commands) != 1 || reviewer.requests[0].Commands[0].Command != "go test ./..." {
		t.Fatalf("review request commands = %+v, want fresh test included", reviewer.requests)
	}
}

func submitToolHostForTest(t *testing.T, todos []agent.Todo, reviews []agent.CommandEvidenceReview) (*api.Host, *recordingCommandEvidenceReviewer) {
	t.Helper()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	layout, err := state.ResolveLayout(workspaceRoot, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	conv := agent.NewConversationState(
		store.NewPlanStore(layout),
		store.NewTodoStore(layout),
		store.NewMemoryStore(layout),
		store.NewCommandEvidenceStore(layout),
	)
	if err := conv.WritePlanAndTodos(validSubmitPlanForTest(), todos); err != nil {
		t.Fatalf("WritePlanAndTodos: %v", err)
	}
	if err := conv.AppendCommandEvidence(agent.CommandEvidenceRecord{
		Command: "go test ./...", Status: "succeeded", Executed: true, Succeeded: true,
	}); err != nil {
		t.Fatalf("AppendCommandEvidence: %v", err)
	}
	reviewer := &recordingCommandEvidenceReviewer{reviews: append([]agent.CommandEvidenceReview(nil), reviews...)}
	return api.NewHost(nil, nil, nil, nil, nil, nil, conv, reviewer, nil), reviewer
}

type recordingCommandEvidenceReviewer struct {
	reviews  []agent.CommandEvidenceReview
	calls    int
	requests []agent.CommandEvidenceReviewRequest
}

func (r *recordingCommandEvidenceReviewer) ReviewCommandEvidence(_ context.Context, request agent.CommandEvidenceReviewRequest) (agent.CommandEvidenceReview, error) {
	r.calls++
	r.requests = append(r.requests, request)
	if len(r.reviews) == 0 {
		return agent.CommandEvidenceReview{Verdict: "sufficient", VerificationPerformed: true, Confidence: "high", Reason: "ok"}, nil
	}
	review := r.reviews[0]
	if len(r.reviews) > 1 {
		r.reviews = r.reviews[1:]
	}
	return review, nil
}

func validSubmitPlanForTest() string {
	return "# Goal\nImplement the requested behavior with enough detail for validation.\n\n# Files\n- internal/builtintools/submit_plan_implementation.go\n\n# Changes\n1. Apply the implementation change.\n2. Update tests for the submit behavior.\n3. Keep command evidence available for review.\n\n# Verification\nRun the targeted submit-plan tests."
}
