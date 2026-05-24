package runtime

import (
	"context"

	"github.com/smasonuk/falken-core/internal/policy"
	"github.com/smasonuk/falken-core/internal/policy/commands"
)

// ApprovalStatus summarizes how approval factored into a runtime-facing policy decision.
type ApprovalStatus string

const (
	ApprovalStatusNotNeeded         ApprovalStatus = "not_needed"
	ApprovalStatusPreviouslyGranted ApprovalStatus = "previously_granted"
	ApprovalStatusGranted           ApprovalStatus = "granted"
	ApprovalStatusDenied            ApprovalStatus = "denied"
	ApprovalStatusUnavailable       ApprovalStatus = "unavailable"
)

// ShellOutcome identifies the runtime-facing shell policy result.
type ShellOutcome string

const (
	ShellOutcomeAllowed                   ShellOutcome = "allowed"
	ShellOutcomeDenied                    ShellOutcome = "denied"
	ShellOutcomeBlockedByShellWriteBypass ShellOutcome = "blocked_shell_write_bypass"
)

// FileRequest is the runtime-facing file policy request.
type FileRequest struct {
	Path             string
	Mode             policy.FileAccessMode
	ApprovalRequired bool
}

// NetworkRequest is the runtime-facing network policy request.
type NetworkRequest struct {
	Host             string
	Port             int
	Method           string
	Target           string
	ApprovalRequired bool
}

// ShellRequest is the runtime-facing shell policy request.
type ShellRequest struct {
	Command          string
	ApprovalRequired bool
}

// FileResult is the runtime-facing file policy decision.
type FileResult struct {
	Allowed        bool
	Decision       policy.Decision
	ApprovalStatus ApprovalStatus
	Explanation    string
}

// NetworkResult is the runtime-facing network policy decision.
type NetworkResult struct {
	Allowed        bool
	Decision       policy.Decision
	ApprovalStatus ApprovalStatus
	Explanation    string
}

// ShellResult is the runtime-facing shell policy decision.
type ShellResult struct {
	Outcome                   ShellOutcome
	Allowed                   bool
	AccessDecision            policy.Decision
	ApprovalStatus            ApprovalStatus
	BlockedByShellWriteBypass bool
	Explanation               string
}

// Evaluator joins the access engine and shell-write detector into a runtime-facing policy surface.
type Evaluator struct {
	engine *policy.Engine
}

// NewEvaluator creates a runtime-facing policy evaluator around the shared access engine.
func NewEvaluator(engine *policy.Engine) *Evaluator {
	return &Evaluator{engine: engine}
}

// EvaluateFile evaluates runtime-facing file access policy.
func (e *Evaluator) EvaluateFile(ctx context.Context, request FileRequest) (FileResult, error) {
	decision, err := e.engine.EvaluateFile(ctx, policy.FileRequest{
		Path:             request.Path,
		Mode:             request.Mode,
		ApprovalRequired: request.ApprovalRequired || fileRequiresApproval(request.Mode),
	})
	if err != nil {
		return FileResult{}, err
	}

	return FileResult{
		Allowed:        decision.Allowed,
		Decision:       decision,
		ApprovalStatus: approvalStatus(decision),
		Explanation:    decisionExplanation(decision, "file access"),
	}, nil
}

// EvaluateNetwork evaluates runtime-facing network access policy.
func (e *Evaluator) EvaluateNetwork(ctx context.Context, request NetworkRequest) (NetworkResult, error) {
	decision, err := e.engine.EvaluateNetwork(ctx, policy.NetworkRequest{
		Host:             request.Host,
		Port:             request.Port,
		Method:           request.Method,
		Target:           request.Target,
		ApprovalRequired: request.ApprovalRequired,
	})
	if err != nil {
		return NetworkResult{}, err
	}

	return NetworkResult{
		Allowed:        decision.Allowed,
		Decision:       decision,
		ApprovalStatus: approvalStatus(decision),
		Explanation:    decisionExplanation(decision, "network access"),
	}, nil
}

// EvaluateShell evaluates runtime-facing shell command policy.
func (e *Evaluator) EvaluateShell(ctx context.Context, request ShellRequest) (ShellResult, error) {
	hasShellWrite, _, parseErr := commands.HasShellWritePatterns(request.Command)
	result := ShellResult{
		BlockedByShellWriteBypass: hasShellWrite,
	}

	// Parse errors are not hard-blocked here. The policy engine still applies
	// shell allow/approval/deny rules. Blocking on parse errors would break
	// shell constructs the parser does not yet understand.
	_ = parseErr

	if hasShellWrite {
		result.Outcome = ShellOutcomeBlockedByShellWriteBypass
		result.Allowed = false
		result.AccessDecision = blockedShellWriteDecision()
		result.ApprovalStatus = ApprovalStatusNotNeeded
		result.Explanation = "shell command is blocked because it writes files directly through the shell"
		return result, nil
	}

	decision, err := e.engine.EvaluateShell(ctx, policy.ShellRequest{
		Command:          request.Command,
		ApprovalRequired: request.ApprovalRequired,
	})
	if err != nil {
		return ShellResult{}, err
	}
	result.AccessDecision = decision
	result.ApprovalStatus = approvalStatus(decision)

	switch {
	case !decision.Allowed:
		result.Outcome = ShellOutcomeDenied
		result.Allowed = false
		result.Explanation = decisionExplanation(decision, "shell access")
	default:
		result.Outcome = ShellOutcomeAllowed
		result.Allowed = true
		result.Explanation = decisionExplanation(decision, "shell access")
	}

	return result, nil
}

func fileRequiresApproval(mode policy.FileAccessMode) bool {
	switch mode {
	case policy.FileAccessCreate, policy.FileAccessWrite:
		return true
	default:
		return false
	}
}

func blockedShellWriteDecision() policy.Decision {
	return policy.Decision{
		Kind:    policy.RequestKindShell,
		Allowed: false,
		Source:  policy.DecisionSourceStrictAllowlistDeny,
	}
}

func approvalStatus(decision policy.Decision) ApprovalStatus {
	switch decision.Source {
	case policy.DecisionSourceSessionApproval, policy.DecisionSourceProjectApproval:
		return ApprovalStatusPreviouslyGranted
	case policy.DecisionSourceApprovalGrantedOnce,
		policy.DecisionSourceApprovalGrantedSession,
		policy.DecisionSourceApprovalGrantedProject:
		return ApprovalStatusGranted
	case policy.DecisionSourceApprovalDenied:
		return ApprovalStatusDenied
	case policy.DecisionSourceMissingApprovalHandler:
		return ApprovalStatusUnavailable
	default:
		return ApprovalStatusNotNeeded
	}
}

func decisionExplanation(decision policy.Decision, subject string) string {
	switch decision.Source {
	case policy.DecisionSourceBlockedRule:
		return subject + " is denied by a blocked rule"
	case policy.DecisionSourceAllowedRule:
		return subject + " is allowed by an allowed rule"
	case policy.DecisionSourceProjectApproval:
		return subject + " is allowed by a project-scoped approval"
	case policy.DecisionSourceSessionApproval:
		return subject + " is allowed by a session-scoped approval"
	case policy.DecisionSourceStrictAllowlistDeny:
		return subject + " is denied by strict allowlist policy"
	case policy.DecisionSourceMissingApprovalHandler:
		return subject + " is denied because approval is required and no handler is available"
	case policy.DecisionSourceApprovalDenied:
		return subject + " is denied because approval was not granted"
	case policy.DecisionSourceApprovalGrantedOnce:
		return subject + " is allowed by a one-time approval"
	case policy.DecisionSourceApprovalGrantedSession:
		return subject + " is allowed by a session approval"
	case policy.DecisionSourceApprovalGrantedProject:
		return subject + " is allowed by a project approval"
	case policy.DecisionSourceDefaultAllow:
		return subject + " is allowed by default policy"
	default:
		return subject + " policy decision is available"
	}
}
