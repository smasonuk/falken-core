package conversation

import (
	"fmt"
	"strings"
	"time"

	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/internal/store"
)

// CommandEvidenceRecord is durable evidence for one command result.
type CommandEvidenceRecord = store.CommandEvidenceRecord

// CommandEvidenceState persists runtime command evidence and reviewer state.
type CommandEvidenceState = store.CommandEvidenceState

// CommandEvidenceReview is the structured reviewer result persisted with evidence state.
type CommandEvidenceReview = store.CommandEvidenceReview

// CompletionBlocker explains why an implementation submission was rejected.
type CompletionBlocker struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
}

// ImplementationCompletionCheck is the deterministic completion decision.
type ImplementationCompletionCheck struct {
	Accepted bool                `json:"accepted"`
	Status   string              `json:"status"`
	Blockers []CompletionBlocker `json:"blockers"`
	Warnings []CompletionWarning `json:"warnings,omitempty"`
}

// CompletionWarning explains why an accepted implementation carries a warning.
type CompletionWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CommandEvidenceManager coordinates command evidence persistence.
type CommandEvidenceManager struct {
	store store.CommandEvidenceBackend
}

// NewCommandEvidenceManager creates a runtime command evidence helper.
func NewCommandEvidenceManager(commandEvidenceStore store.CommandEvidenceBackend) *CommandEvidenceManager {
	return &CommandEvidenceManager{store: commandEvidenceStore}
}

// ShouldRecordCommandEvidence reports whether a command result contains durable command facts.
func ShouldRecordCommandEvidence(result runtimeexec.CommandResult) bool {
	return result.Command != "" || result.Status != ""
}

// CommandEvidenceRecordFromCommandResult converts bounded command metadata into durable evidence.
func CommandEvidenceRecordFromCommandResult(result runtimeexec.CommandResult, recordedAt string) CommandEvidenceRecord {
	workingDir := result.ToolWorkingDir
	if workingDir == "" {
		workingDir = result.WorkingDir
	}
	return CommandEvidenceRecord{
		Command:              result.Command,
		WorkingDir:           workingDir,
		Status:               string(result.Status),
		ExitCode:             result.ExitCode,
		Executed:             result.Executed,
		Succeeded:            result.Executed && result.Status == runtimeexec.CommandStatusSucceeded,
		OutputTruncated:      result.Output.Truncated,
		OutputOriginalBytes:  result.Output.OriginalBytes,
		OutputPreviewBytes:   result.Output.PreviewBytes,
		PolicyOutcome:        string(result.Policy.Outcome),
		PolicyExplanation:    result.Policy.Explanation,
		HasShellWritePattern: result.Policy.BlockedByShellWriteBypass,
		RecordedAt:           recordedAt,
	}
}

// Read returns the current command evidence.
func (m *CommandEvidenceManager) Read() (CommandEvidenceState, error) {
	return m.store.Read()
}

// Append persists one command evidence record.
func (m *CommandEvidenceManager) Append(record CommandEvidenceRecord) error {
	state, err := m.store.Read()
	if err != nil {
		return err
	}
	record.Revision = maxCommandEvidenceRevision(state) + 1
	if strings.TrimSpace(record.RecordedAt) == "" {
		record.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	state.Records = append(state.Records, record)
	return m.store.Write(state)
}

// RecordReview persists the latest reviewer result and increments attempts for non-sufficient verdicts.
func (m *CommandEvidenceManager) RecordReview(review CommandEvidenceReview) (CommandEvidenceState, error) {
	state, err := m.store.Read()
	if err != nil {
		return CommandEvidenceState{}, err
	}
	if strings.TrimSpace(review.RecordedAt) == "" {
		review.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	state.LastReview = &review
	if review.Verdict != "sufficient" {
		state.ReviewAttempts++
	}
	if err := m.store.Write(state); err != nil {
		return CommandEvidenceState{}, err
	}
	return state, nil
}

// ResetReviewAttempts clears reviewer attempts while keeping historical command records.
func (m *CommandEvidenceManager) ResetReviewAttempts() error {
	state, err := m.store.Read()
	if err != nil {
		return err
	}
	state.ReviewAttempts = 0
	state.LastReview = nil
	return m.store.Write(state)
}

// RecordPlanBaseline records the command evidence boundary for a newly
// committed implementation plan while preserving historical records.
func (m *CommandEvidenceManager) RecordPlanBaseline() error {
	state, err := m.store.Read()
	if err != nil {
		return err
	}
	state = assignMissingCommandEvidenceRevisions(state)
	state.PlanBaselineRevision = maxCommandEvidenceRevision(state)
	state.LastWorkspaceMutationRevision = 0
	state.LastWorkspaceMutationAt = ""
	state.LastWorkspaceMutationTool = ""
	state.ReviewAttempts = 0
	state.LastReview = nil
	return m.store.Write(state)
}

// RecordWorkspaceMutation records the command evidence boundary after a
// managed workspace mutation succeeds or may have occurred.
func (m *CommandEvidenceManager) RecordWorkspaceMutation(toolName string, recordedAt string) (CommandEvidenceState, error) {
	state, err := m.store.Read()
	if err != nil {
		return CommandEvidenceState{}, err
	}
	if strings.TrimSpace(recordedAt) == "" {
		recordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	state = assignMissingCommandEvidenceRevisions(state)
	state.LastWorkspaceMutationRevision = maxCommandEvidenceRevision(state)
	state.LastWorkspaceMutationAt = recordedAt
	state.LastWorkspaceMutationTool = strings.TrimSpace(toolName)
	state.ReviewAttempts = 0
	state.LastReview = nil
	if err := m.store.Write(state); err != nil {
		return CommandEvidenceState{}, err
	}
	return state, nil
}

// Clear removes all command evidence.
func (m *CommandEvidenceManager) Clear() error {
	return m.store.Write(CommandEvidenceState{Records: []CommandEvidenceRecord{}})
}

// ValidatePlanTodosComplete applies the hard todo completion gate.
func ValidatePlanTodosComplete(todos []Todo) ImplementationCompletionCheck {
	if details := incompleteTodoDetails(todos); len(details) != 0 {
		return ImplementationCompletionCheck{
			Accepted: false,
			Status:   "blocked_incomplete_todos",
			Blockers: []CompletionBlocker{{
				Code:    "incomplete_todos",
				Message: "Runtime todos are not complete.",
				Details: details,
			}},
		}
	}
	return acceptedCompletionCheck()
}

// CompletionDecisionFromReview applies the reviewer retry policy after todos pass.
func CompletionDecisionFromReview(todos []Todo, review CommandEvidenceReview, attempts int) ImplementationCompletionCheck {
	if check := ValidatePlanTodosComplete(todos); !check.Accepted {
		return check
	}
	if review.Verdict == "sufficient" || review.Verdict == "not_applicable" {
		return acceptedCompletionCheck()
	}
	if attempts <= 0 {
		return ImplementationCompletionCheck{
			Accepted: false,
			Status:   "blocked_verification_review",
			Blockers: []CompletionBlocker{{
				Code:    "verification_review_insufficient",
				Message: "A verification reviewer could not identify a sufficient verification step.",
				Details: []string{
					"Run an appropriate test, build, linter, typecheck, smoke check, or project-specific validation command, then submit again.",
				},
			}},
		}
	}
	return ImplementationCompletionCheck{
		Accepted: true,
		Status:   "accepted_with_verification_warning",
		Blockers: []CompletionBlocker{},
		Warnings: []CompletionWarning{{
			Code:    "verification_not_confirmed",
			Message: "A verification reviewer could not confirm that sufficient verification was performed.",
		}},
	}
}

func acceptedCompletionCheck() ImplementationCompletionCheck {
	return ImplementationCompletionCheck{
		Accepted: true,
		Status:   "accepted",
		Blockers: []CompletionBlocker{},
		Warnings: []CompletionWarning{},
	}
}

func incompleteTodoDetails(todos []Todo) []string {
	var details []string
	for _, todo := range todos {
		if todo.Status == TodoStatusCompleted {
			continue
		}
		details = append(details, fmt.Sprintf("%s: %s is %s", todo.ID, todo.Content, todo.Status))
	}
	return details
}

func maxCommandEvidenceRevision(state CommandEvidenceState) int64 {
	var maxRevision int64
	for _, record := range state.Records {
		if record.Revision > maxRevision {
			maxRevision = record.Revision
		}
	}
	return maxRevision
}

func assignMissingCommandEvidenceRevisions(state CommandEvidenceState) CommandEvidenceState {
	nextRevision := maxCommandEvidenceRevision(state) + 1
	for i := range state.Records {
		if state.Records[i].Revision > 0 {
			continue
		}
		state.Records[i].Revision = nextRevision
		nextRevision++
	}
	return state
}
