package planningtools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/conversation"
)

const (
	maxReviewPlanSummaryBytes = 1200
	maxReviewCommands         = 20
)

// SubmitPlanImplementationTool submits a completed runtime plan for validation.
type SubmitPlanImplementationTool struct{}

func (t *SubmitPlanImplementationTool) Descriptor() api.Descriptor {
	return api.Descriptor{
		Name: "submit_plan_implementation",
		Description: `Submit the completed implementation of the current runtime plan for final validation.

This tool checks todo completion and asks a separate reviewer whether recent
command evidence appears to show verification. If the first review is
insufficient or unclear, the tool will ask you to run a better check and submit
again. A second insufficient review is accepted with a warning.`,
		Parameters: api.MustSchema(api.ObjectSchema(map[string]any{
			"summary": api.StringProp("Concise summary of what was implemented."),
			"verification_summary": api.StringProp(
				"Concise summary of verification performed and results."),
			"known_issues": api.ArrayProp(
				api.StringProp("Known remaining issue, limitation, or skipped work."),
				"Known remaining issues, limitations, or skipped work."),
		}, "summary", "verification_summary")),
		Category:    "planning",
		Keywords:    []string{"plan", "implementation", "submit", "complete", "verify"},
		AlwaysLoad:  true,
		DefaultLoad: true,
		Safety:      api.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true, MutatesHostState: true},
	}
}

func (t *SubmitPlanImplementationTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	var params struct {
		Summary             string   `json:"summary"`
		VerificationSummary string   `json:"verification_summary"`
		KnownIssues         []string `json:"known_issues"`
	}
	if err := api.DecodeArgs(args, &params); err != nil {
		return api.Fail("invalid_arguments", err.Error()), nil
	}

	state, err := host.RequireConversationState()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	plan, err := state.ReadPlan()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	todos, err := state.ReadTodos()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	todoCheck := conversation.ValidatePlanTodosComplete(todos)
	basePayload := map[string]any{
		"success":              todoCheck.Accepted,
		"accepted":             todoCheck.Accepted,
		"plan_completed":       false,
		"todos_cleared":        false,
		"active_plan_cleared":  false,
		"status":               todoCheck.Status,
		"summary":              params.Summary,
		"verification_summary": params.VerificationSummary,
		"known_issues":         nonNilStrings(params.KnownIssues),
		"blockers":             todoCheck.Blockers,
		"warnings":             todoCheck.Warnings,
	}
	if !todoCheck.Accepted {
		basePayload["success"] = false
		basePayload["accepted"] = false
		return api.ResultFromPayload(false, todoCheck.Status, renderCompletionBlockers(todoCheck.Blockers), basePayload)
	}

	evidence, err := state.ReadCommandEvidence()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	priorAttempts := evidence.ReviewAttempts
	request := buildCommandEvidenceReviewRequest(plan, params.Summary, params.VerificationSummary, params.KnownIssues, evidence, priorAttempts+1)
	review := reviewCommandEvidence(ctx, host.CommandEvidenceReviewer(), request)
	updatedEvidence, err := state.RecordCommandEvidenceReview(review)
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}

	check := conversation.CompletionDecisionFromReview(todos, review, priorAttempts)
	payload := basePayload
	payload["success"] = check.Accepted
	payload["accepted"] = check.Accepted
	payload["plan_completed"] = check.Accepted
	payload["status"] = check.Status
	payload["blockers"] = check.Blockers
	payload["warnings"] = check.Warnings
	payload["reviewer_verdict"] = review.Verdict
	payload["reviewer_reason"] = review.Reason
	payload["reviewer_confidence"] = review.Confidence
	payload["reviewer_verification_performed"] = review.VerificationPerformed
	payload["reviewer_relevant_commands"] = nonNilStrings(review.RelevantCommands)
	payload["review_attempt"] = priorAttempts + 1
	payload["review_attempts"] = updatedEvidence.ReviewAttempts
	payload["command_count_reviewed"] = len(request.Commands)

	if !check.Accepted {
		return api.ResultFromPayload(false, check.Status, renderCompletionBlockers(check.Blockers), payload)
	}
	if err := state.CompletePlanImplementation(); err != nil {
		return agent.ToolExecutionResult{}, err
	}
	payload["todos_cleared"] = true
	payload["active_plan_cleared"] = true
	content := "Plan implementation accepted."
	if check.Status == "accepted_with_verification_warning" {
		content = "Plan implementation accepted with warning: verification was not confirmed by the reviewer."
	}
	return api.ResultFromPayload(true, check.Status, content, payload)
}

func reviewCommandEvidence(ctx context.Context, reviewer agent.CommandEvidenceReviewer, request agent.CommandEvidenceReviewRequest) conversation.CommandEvidenceReview {
	if reviewer == nil {
		return unclearCommandEvidenceReview("command evidence reviewer is unavailable")
	}
	review, err := reviewer.ReviewCommandEvidence(ctx, request)
	if err != nil {
		return unclearCommandEvidenceReview("command evidence reviewer returned an error: " + err.Error())
	}
	return normalizeCommandEvidenceReview(review)
}

func normalizeCommandEvidenceReview(review conversation.CommandEvidenceReview) conversation.CommandEvidenceReview {
	switch review.Verdict {
	case "sufficient", "insufficient", "unclear", "not_applicable":
	default:
		review.Verdict = "unclear"
	}
	switch review.Confidence {
	case "low", "medium", "high":
	default:
		review.Confidence = "low"
	}
	if strings.TrimSpace(review.Reason) == "" {
		review.Reason = "The reviewer did not provide a usable reason."
	}
	if review.RelevantCommands == nil {
		review.RelevantCommands = []string{}
	}
	if strings.TrimSpace(review.RecordedAt) == "" {
		review.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return review
}

func unclearCommandEvidenceReview(reason string) conversation.CommandEvidenceReview {
	return conversation.CommandEvidenceReview{
		Verdict:               "unclear",
		VerificationPerformed: false,
		Confidence:            "low",
		Reason:                reason,
		RelevantCommands:      []string{},
		RecordedAt:            time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func buildCommandEvidenceReviewRequest(plan, summary, verificationSummary string, knownIssues []string, evidence conversation.CommandEvidenceState, attempt int) agent.CommandEvidenceReviewRequest {
	return agent.CommandEvidenceReviewRequest{
		PlanSummary:                   truncateBytes(strings.TrimSpace(plan), maxReviewPlanSummaryBytes),
		VerificationSection:           extractPlanVerificationSection(plan),
		ImplementationSummary:         strings.TrimSpace(summary),
		VerificationSummary:           strings.TrimSpace(verificationSummary),
		KnownIssues:                   nonNilStrings(knownIssues),
		Commands:                      selectReviewCommands(evidence),
		Attempt:                       attempt,
		PlanBaselineRevision:          evidence.PlanBaselineRevision,
		LastWorkspaceMutationRevision: evidence.LastWorkspaceMutationRevision,
		LastWorkspaceMutationAt:       evidence.LastWorkspaceMutationAt,
		LastWorkspaceMutationTool:     evidence.LastWorkspaceMutationTool,
	}
}

func selectReviewCommands(evidence conversation.CommandEvidenceState) []agent.CommandEvidenceCommand {
	cutoff := evidence.PlanBaselineRevision
	if evidence.LastWorkspaceMutationRevision > cutoff {
		cutoff = evidence.LastWorkspaceMutationRevision
	}

	records := make([]conversation.CommandEvidenceRecord, 0, len(evidence.Records))
	for _, record := range evidence.Records {
		if cutoff == 0 && evidence.LastWorkspaceMutationAt == "" && evidence.LastWorkspaceMutationTool == "" {
			records = append(records, record)
			continue
		}
		if record.Revision > cutoff {
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		return []agent.CommandEvidenceCommand{}
	}
	start := len(records) - maxReviewCommands
	if start < 0 {
		start = 0
	}
	commands := make([]agent.CommandEvidenceCommand, 0, len(records[start:]))
	for _, record := range records[start:] {
		commands = append(commands, agent.CommandEvidenceCommand{
			Command:    record.Command,
			WorkingDir: record.WorkingDir,
			Status:     record.Status,
			ExitCode:   record.ExitCode,
			Executed:   record.Executed,
			Succeeded:  record.Succeeded,
			RecordedAt: record.RecordedAt,
			Revision:   record.Revision,
		})
	}
	return commands
}

func extractPlanVerificationSection(plan string) string {
	lines := strings.Split(plan, "\n")
	start := -1
	for i, line := range lines {
		heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
		heading = strings.ToLower(strings.TrimSpace(heading))
		if strings.HasPrefix(heading, "verification") || strings.HasPrefix(heading, "validation") {
			start = i
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			end = i
			break
		}
	}
	return truncateBytes(strings.TrimSpace(strings.Join(lines[start:end], "\n")), maxReviewPlanSummaryBytes)
}

func truncateBytes(value string, maxBytes int) string {
	if len([]byte(value)) <= maxBytes {
		return value
	}
	if maxBytes <= 0 {
		return ""
	}
	data := []byte(value)
	return string(data[:maxBytes])
}

func renderCompletionBlockers(blockers []agent.CompletionBlocker) string {
	if len(blockers) == 0 {
		return "Implementation submission blocked."
	}
	return blockers[0].Message
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
