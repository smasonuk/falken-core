package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const commandEvidenceReviewToolName = "record_command_evidence_review"

// CommandEvidenceReviewer reviews recent command evidence in a separate model call.
type CommandEvidenceReviewer interface {
	ReviewCommandEvidence(context.Context, CommandEvidenceReviewRequest) (CommandEvidenceReview, error)
}

// CommandEvidenceReviewRequest is the compact context supplied to the reviewer.
type CommandEvidenceReviewRequest struct {
	PlanSummary           string
	VerificationSection   string
	ImplementationSummary string
	VerificationSummary   string
	KnownIssues           []string
	Commands              []CommandEvidenceCommand
	Attempt               int

	PlanBaselineRevision          int64
	LastWorkspaceMutationRevision int64
	LastWorkspaceMutationAt       string
	LastWorkspaceMutationTool     string
}

// CommandEvidenceCommand is the bounded command fact shape sent to the reviewer.
type CommandEvidenceCommand struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir,omitempty"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	Executed   bool   `json:"executed"`
	Succeeded  bool   `json:"succeeded"`
	RecordedAt string `json:"recorded_at,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
}

// LLMCommandEvidenceReviewer asks an LLM to review command evidence with a forced tool call.
type LLMCommandEvidenceReviewer struct {
	LLM LLM
}

// NewLLMCommandEvidenceReviewer creates an LLM-backed command evidence reviewer.
func NewLLMCommandEvidenceReviewer(llm LLM) *LLMCommandEvidenceReviewer {
	return &LLMCommandEvidenceReviewer{LLM: llm}
}

// ReviewCommandEvidence implements CommandEvidenceReviewer.
func (r *LLMCommandEvidenceReviewer) ReviewCommandEvidence(ctx context.Context, request CommandEvidenceReviewRequest) (CommandEvidenceReview, error) {
	if r == nil || r.LLM == nil {
		return CommandEvidenceReview{}, errors.New("command evidence reviewer LLM is unavailable")
	}
	payload, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return CommandEvidenceReview{}, fmt.Errorf("encode command evidence review request: %w", err)
	}
	response, err := r.LLM.Complete(ctx, CompletionRequest{
		Messages: []Message{
			SystemMessage(commandEvidenceReviewerInstruction),
			UserMessage("Review this command evidence request and call " + commandEvidenceReviewToolName + " exactly once.\n\n" + string(payload)),
		},
		Tools: []ToolDefinition{commandEvidenceReviewToolDefinition()},
		ToolChoice: &ToolChoice{
			Type: "tool",
			Name: commandEvidenceReviewToolName,
		},
	})
	if err != nil {
		return CommandEvidenceReview{}, err
	}
	review, err := decodeCommandEvidenceReview(response.ToolCalls)
	if err != nil {
		return CommandEvidenceReview{}, err
	}
	if strings.TrimSpace(review.RecordedAt) == "" {
		review.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return review, nil
}

const commandEvidenceReviewerInstruction = `You are reviewing whether an autonomous coding agent appears to have run a reasonable verification step after implementing code changes.

You are not proving correctness.
You are not judging code quality.
You are only checking whether the recent command evidence includes a plausible verification action, such as tests, build, lint, typecheck, smoke run, or a project-specific validation command.

Important rules:
- Treat command names flexibly.
- Do not require hard-coded known commands.
- Custom commands like make check, just validate, npm run custom, or ./scripts/check.sh may be sufficient if their name, context, or verification summary indicates validation.
- Simple inspection commands like ls, pwd, cat, grep, git status, and git diff are usually insufficient.
- Failed commands are not sufficient verification unless the implementation summary explicitly says failure is expected and acceptable.
- If recent command evidence does not include a successful plausible verification command, verdict should be insufficient or unclear.
- If the implementation summary indicates no code or workspace changes were made, verdict may be not_applicable.

Return structured JSON only by calling the provided tool.`

func commandEvidenceReviewToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        commandEvidenceReviewToolName,
		Description: "Record a structured command evidence review decision.",
		Parameters: json.RawMessage(`{
		  "type": "object",
		  "properties": {
		    "verdict": {
		      "type": "string",
		      "enum": ["sufficient", "insufficient", "unclear", "not_applicable"]
		    },
		    "verification_performed": {
		      "type": "boolean"
		    },
		    "confidence": {
		      "type": "string",
		      "enum": ["low", "medium", "high"]
		    },
		    "reason": {
		      "type": "string"
		    },
		    "relevant_commands": {
		      "type": "array",
		      "items": {"type": "string"}
		    },
		    "suggested_next_step": {
		      "type": "string"
		    },
		    "warning": {
		      "type": "string"
		    }
		  },
		  "required": ["verdict", "verification_performed", "confidence", "reason"]
		}`),
	}
}

func decodeCommandEvidenceReview(calls []ToolCall) (CommandEvidenceReview, error) {
	if len(calls) != 1 {
		return CommandEvidenceReview{}, fmt.Errorf("reviewer did not call %s exactly once", commandEvidenceReviewToolName)
	}
	call := calls[0]
	if call.Name != commandEvidenceReviewToolName {
		return CommandEvidenceReview{}, fmt.Errorf("reviewer called %q, want %s", call.Name, commandEvidenceReviewToolName)
	}

	var review CommandEvidenceReview
	if err := json.Unmarshal(call.Arguments, &review); err != nil {
		return CommandEvidenceReview{}, fmt.Errorf("decode %s arguments: %w", commandEvidenceReviewToolName, err)
	}
	review.Verdict = strings.TrimSpace(review.Verdict)
	switch review.Verdict {
	case "sufficient", "insufficient", "unclear", "not_applicable":
	default:
		return CommandEvidenceReview{}, fmt.Errorf("invalid command evidence review verdict: %q", review.Verdict)
	}
	review.Confidence = strings.TrimSpace(review.Confidence)
	switch review.Confidence {
	case "low", "medium", "high":
	default:
		return CommandEvidenceReview{}, fmt.Errorf("invalid command evidence review confidence: %q", review.Confidence)
	}
	review.Reason = strings.TrimSpace(review.Reason)
	if review.Reason == "" {
		return CommandEvidenceReview{}, errors.New("command evidence review missing reason")
	}
	if review.RelevantCommands == nil {
		review.RelevantCommands = []string{}
	}
	return review, nil
}
