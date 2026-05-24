package runtime_test

import (
	"context"
	"testing"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
)

func TestEvaluateFileAllowed(t *testing.T) {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		AllowedFiles: []policy.FileRule{{
			Path:  "/workspace/notes.txt",
			Match: policy.MatchExact,
			Modes: []policy.FileAccessMode{policy.FileAccessRead},
		}},
		StrictFileAllowlist: true,
	}, nil))

	result, err := evaluator.EvaluateFile(context.Background(), runtimepolicy.FileRequest{
		Path: "/workspace/notes.txt",
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("EvaluateFile: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("expected allowed file result, got %+v", result)
	}
	if result.Decision.Source != policy.DecisionSourceAllowedRule {
		t.Fatalf("expected allowed-rule source, got %+v", result)
	}
}

func TestEvaluateFileBlockedAndStrictAllowlist(t *testing.T) {
	blockedEvaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		BlockedFiles: []policy.FileRule{{
			Path:  "/workspace/secrets.txt",
			Match: policy.MatchExact,
		}},
	}, nil))

	blocked, err := blockedEvaluator.EvaluateFile(context.Background(), runtimepolicy.FileRequest{
		Path: "/workspace/secrets.txt",
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("blocked EvaluateFile: %v", err)
	}
	if blocked.Allowed {
		t.Fatalf("expected blocked file result, got %+v", blocked)
	}
	if blocked.Decision.Source != policy.DecisionSourceBlockedRule {
		t.Fatalf("expected blocked-rule source, got %+v", blocked)
	}

	strictEvaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		StrictFileAllowlist: true,
	}, nil))

	strict, err := strictEvaluator.EvaluateFile(context.Background(), runtimepolicy.FileRequest{
		Path: "/workspace/unlisted.txt",
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("strict EvaluateFile: %v", err)
	}
	if strict.Allowed {
		t.Fatalf("expected strict-allowlist denial, got %+v", strict)
	}
	if strict.Decision.Source != policy.DecisionSourceStrictAllowlistDeny {
		t.Fatalf("expected strict-allowlist denial source, got %+v", strict)
	}
}

func TestEvaluateFileMutationRequiresRuntimeApproval(t *testing.T) {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{}, nil))

	for _, mode := range []policy.FileAccessMode{policy.FileAccessCreate, policy.FileAccessWrite} {
		t.Run(string(mode), func(t *testing.T) {
			result, err := evaluator.EvaluateFile(context.Background(), runtimepolicy.FileRequest{
				Path: "/workspace/target.txt",
				Mode: mode,
			})
			if err != nil {
				t.Fatalf("EvaluateFile: %v", err)
			}
			if result.Allowed || result.ApprovalStatus != runtimepolicy.ApprovalStatusUnavailable {
				t.Fatalf("result = %+v, want denied missing approval handler", result)
			}
			if result.Decision.Source != policy.DecisionSourceMissingApprovalHandler {
				t.Fatalf("decision source = %q, want missing approval handler", result.Decision.Source)
			}
		})
	}
}

func TestEvaluateFileAllowedRuleBypassesMutationApproval(t *testing.T) {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		AllowedFiles: []policy.FileRule{{
			Path:  "/workspace/new.txt",
			Match: policy.MatchExact,
			Modes: []policy.FileAccessMode{policy.FileAccessCreate},
		}},
	}, nil))

	result, err := evaluator.EvaluateFile(context.Background(), runtimepolicy.FileRequest{
		Path: "/workspace/new.txt",
		Mode: policy.FileAccessCreate,
	})
	if err != nil {
		t.Fatalf("EvaluateFile: %v", err)
	}
	if !result.Allowed || result.Decision.Source != policy.DecisionSourceAllowedRule {
		t.Fatalf("result = %+v, want allowed rule without approval", result)
	}
}

func TestEvaluateNetworkAllowedAndBlocked(t *testing.T) {
	allowedEvaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		AllowedNetwork: []policy.NetworkRule{{
			Host:  "api.example.com",
			Match: policy.MatchExact,
		}},
	}, nil))

	allowed, err := allowedEvaluator.EvaluateNetwork(context.Background(), runtimepolicy.NetworkRequest{
		Host: "api.example.com",
	})
	if err != nil {
		t.Fatalf("allowed EvaluateNetwork: %v", err)
	}
	if !allowed.Allowed || allowed.Decision.Source != policy.DecisionSourceAllowedRule {
		t.Fatalf("expected allowed network result, got %+v", allowed)
	}

	blockedEvaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		BlockedNetwork: []policy.NetworkRule{{
			Host:  "blocked.example.com",
			Match: policy.MatchExact,
		}},
	}, nil))

	blocked, err := blockedEvaluator.EvaluateNetwork(context.Background(), runtimepolicy.NetworkRequest{
		Host: "blocked.example.com",
	})
	if err != nil {
		t.Fatalf("blocked EvaluateNetwork: %v", err)
	}
	if blocked.Allowed || blocked.Decision.Source != policy.DecisionSourceBlockedRule {
		t.Fatalf("expected blocked network result, got %+v", blocked)
	}
}

func TestEvaluateNetworkCarriesPortToPolicy(t *testing.T) {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		StrictNetworkAllowlist: true,
		AllowedNetwork: []policy.NetworkRule{{
			Host:  "github.com",
			Match: policy.MatchExact,
			Ports: []int{443},
		}},
	}, nil))

	allowed, err := evaluator.EvaluateNetwork(context.Background(), runtimepolicy.NetworkRequest{
		Host:   "github.com",
		Port:   443,
		Method: "CONNECT",
		Target: "github.com:443",
	})
	if err != nil {
		t.Fatalf("allowed EvaluateNetwork: %v", err)
	}
	if !allowed.Allowed {
		t.Fatalf("github.com:443 result = %+v, want allowed", allowed)
	}

	denied, err := evaluator.EvaluateNetwork(context.Background(), runtimepolicy.NetworkRequest{
		Host:   "github.com",
		Port:   22,
		Method: "CONNECT",
		Target: "github.com:22",
	})
	if err != nil {
		t.Fatalf("denied EvaluateNetwork: %v", err)
	}
	if denied.Allowed {
		t.Fatalf("github.com:22 result = %+v, want denied", denied)
	}
}

func TestEvaluateShellAllowedCommandAllowed(t *testing.T) {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		AllowedShell: []policy.ShellRule{{
			Command: "git status",
			Match:   policy.MatchExact,
		}},
		StrictCommandAllowlist: true,
	}, nil))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command: "git status",
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeAllowed || !result.Allowed {
		t.Fatalf("expected allowed shell result, got %+v", result)
	}
	if result.AccessDecision.Source != policy.DecisionSourceAllowedRule {
		t.Fatalf("expected allowed-rule access decision, got %+v", result)
	}
}

func TestEvaluateShellMutatingCommandFollowsConfiguredPolicy(t *testing.T) {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{}, nil))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command: "mkdir -p build",
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeAllowed || !result.Allowed {
		t.Fatalf("expected default policy to allow normal mutating shell result, got %+v", result)
	}
	if result.AccessDecision.Source != policy.DecisionSourceDefaultAllow {
		t.Fatalf("decision source = %q, want default allow", result.AccessDecision.Source)
	}
}

func TestEvaluateShellUnclassifiedCommandFollowsConfiguredPolicy(t *testing.T) {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{}, nil))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command: `python -c 'open("file","w").write("hi")'`,
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeAllowed || !result.Allowed {
		t.Fatalf("result = %+v, want command allowed by default policy", result)
	}
	if result.AccessDecision.Source != policy.DecisionSourceDefaultAllow {
		t.Fatalf("result = %+v, want default-allow metadata", result)
	}
}

func TestEvaluateShellExplicitlyAllowedCommandCanRun(t *testing.T) {
	command := `python -c 'print("hi")'`
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		AllowedShell: []policy.ShellRule{{Command: command, Match: policy.MatchExact}},
	}, nil))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command: command,
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeAllowed || !result.Allowed {
		t.Fatalf("result = %+v, want explicitly allowed command", result)
	}
	if result.AccessDecision.Source != policy.DecisionSourceAllowedRule {
		t.Fatalf("result = %+v, want allowed-rule metadata", result)
	}
}

func TestEvaluateShellBlocksShellWriteBypass(t *testing.T) {
	commands := []string{
		"echo hi > file.txt",
		"cat > file.txt",
		"cat <<'EOF' > file.txt\nhello\nEOF",
		"echo hi | tee file.txt",
		"echo hi | tee -a file.txt",
	}

	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
				AllowedShell: []policy.ShellRule{{
					Command: command,
					Match:   policy.MatchExact,
				}},
				StrictCommandAllowlist: true,
			}, nil))

			result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
				Command: command,
			})
			if err != nil {
				t.Fatalf("EvaluateShell: %v", err)
			}
			if result.Outcome != runtimepolicy.ShellOutcomeBlockedByShellWriteBypass {
				t.Fatalf("expected shell-write bypass outcome, got %+v", result)
			}
			if result.Allowed {
				t.Fatalf("expected shell-write bypass to block execution, got %+v", result)
			}
			if !result.BlockedByShellWriteBypass {
				t.Fatalf("expected shell-write bypass flag, got %+v", result)
			}
			if result.AccessDecision.Source != policy.DecisionSourceStrictAllowlistDeny || result.ApprovalStatus != runtimepolicy.ApprovalStatusNotNeeded {
				t.Fatalf("expected complete pre-approval denial metadata, got %+v", result)
			}
		})
	}
}

func TestEvaluateShellAllowedRuleCannotBypassDestructiveApproval(t *testing.T) {
	handler := &countingApprovalHandler{shellScope: policy.ApprovalScopeSession}
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		ApprovalRequiredShell: []policy.ShellRule{{
			Command: "rm ",
			Match:   policy.MatchPrefix,
		}},
		AllowedShell: []policy.ShellRule{{
			Command: "rm -rf build",
			Match:   policy.MatchExact,
		}},
		StrictCommandAllowlist: true,
	}, handler))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command: "rm -rf build",
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeAllowed || !result.Allowed {
		t.Fatalf("expected approved destructive command despite explicit allow rule, got %+v", result)
	}
	if result.AccessDecision.Source != policy.DecisionSourceApprovalGrantedSession || result.ApprovalStatus != runtimepolicy.ApprovalStatusGranted {
		t.Fatalf("expected explicit approval metadata, got %+v", result)
	}
	if handler.shellCalls != 1 {
		t.Fatalf("shell approval calls = %d, want 1", handler.shellCalls)
	}
}

func TestShellWriteBypassDoesNotRequestApproval(t *testing.T) {
	handler := &countingApprovalHandler{shellScope: policy.ApprovalScopeSession}
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{}, handler))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command:          "echo hi > file.txt",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeBlockedByShellWriteBypass {
		t.Fatalf("outcome = %q, want shell-write bypass", result.Outcome)
	}
	if handler.shellCalls != 0 {
		t.Fatalf("shell approval calls = %d, want 0", handler.shellCalls)
	}
}

func TestShellWriteBypassDoesNotRecordApproval(t *testing.T) {
	handler := &countingApprovalHandler{shellScope: policy.ApprovalScopeSession}
	engine := policy.NewEngine(policy.Config{}, handler)
	evaluator := runtimepolicy.NewEvaluator(engine)

	blocked, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command:          "echo hi > file.txt",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("blocked EvaluateShell: %v", err)
	}
	if blocked.Allowed {
		t.Fatalf("blocked result = %+v, want denied", blocked)
	}

	normal, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command:          "go test ./...",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("normal EvaluateShell: %v", err)
	}
	if normal.ApprovalStatus != runtimepolicy.ApprovalStatusGranted {
		t.Fatalf("normal approval status = %q, want granted", normal.ApprovalStatus)
	}
	if handler.shellCalls != 1 {
		t.Fatalf("shell approval calls = %d, want only normal command approval", handler.shellCalls)
	}
}

func TestApprovalRequiredShellRequestsApproval(t *testing.T) {
	tests := []struct {
		command string
		rule    policy.ShellRule
	}{
		{command: "rm -rf build", rule: policy.ShellRule{Command: "rm ", Match: policy.MatchPrefix}},
		{command: "git clean -fdx", rule: policy.ShellRule{Command: "git clean ", Match: policy.MatchPrefix}},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			handler := &countingApprovalHandler{shellScope: policy.ApprovalScopeSession}
			evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
				ApprovalRequiredShell: []policy.ShellRule{tt.rule},
			}, handler))

			result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
				Command: tt.command,
			})
			if err != nil {
				t.Fatalf("EvaluateShell: %v", err)
			}
			if result.Outcome != runtimepolicy.ShellOutcomeAllowed || !result.Allowed {
				t.Fatalf("result = %+v, want approved command", result)
			}
			if result.ApprovalStatus != runtimepolicy.ApprovalStatusGranted {
				t.Fatalf("result = %+v, want granted approval", result)
			}
			if handler.shellCalls != 1 {
				t.Fatalf("shell approval calls = %d, want 1", handler.shellCalls)
			}
		})
	}
}

func TestApprovalRequiredShellDeniedApprovalIsBlocked(t *testing.T) {
	handler := &countingApprovalHandler{shellScope: policy.ApprovalScopeDeny}
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		ApprovalRequiredShell: []policy.ShellRule{{Command: "rm ", Match: policy.MatchPrefix}},
	}, handler))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command: "rm -rf build",
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeDenied || result.Allowed {
		t.Fatalf("result = %+v, want denied destructive command", result)
	}
	if result.ApprovalStatus != runtimepolicy.ApprovalStatusDenied {
		t.Fatalf("approval status = %q, want denied", result.ApprovalStatus)
	}
	if handler.shellCalls != 1 {
		t.Fatalf("shell approval calls = %d, want 1", handler.shellCalls)
	}
}

func TestNormalShellCommandStillRequestsApprovalWhenRequired(t *testing.T) {
	handler := &countingApprovalHandler{shellScope: policy.ApprovalScopeSession}
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{}, handler))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command:          "go test ./...",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeAllowed || result.ApprovalStatus != runtimepolicy.ApprovalStatusGranted {
		t.Fatalf("result = %+v, want approved normal command", result)
	}
	if handler.shellCalls != 1 {
		t.Fatalf("shell approval calls = %d, want 1", handler.shellCalls)
	}
}

func TestPolicyDeniedShellCommandStillDenied(t *testing.T) {
	handler := &countingApprovalHandler{shellScope: policy.ApprovalScopeSession}
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
		BlockedShell: []policy.ShellRule{{Command: "go test ./...", Match: policy.MatchExact}},
	}, handler))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command:          "go test ./...",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeDenied || result.Allowed {
		t.Fatalf("result = %+v, want denied", result)
	}
	if handler.shellCalls != 0 {
		t.Fatalf("shell approval calls = %d, want 0 for blocked rule", handler.shellCalls)
	}
}

func TestEvaluateShellApprovalSemanticsArePreserved(t *testing.T) {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{}, stubApprovalHandler{
		shellScope: policy.ApprovalScopeSession,
	}))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command:          "go test ./...",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeAllowed || !result.Allowed {
		t.Fatalf("expected approved shell result, got %+v", result)
	}
	if result.ApprovalStatus != runtimepolicy.ApprovalStatusGranted {
		t.Fatalf("expected granted approval status, got %+v", result)
	}
	if result.AccessDecision.Source != policy.DecisionSourceApprovalGrantedSession {
		t.Fatalf("expected approval-granted session source, got %+v", result)
	}
}

func TestEvaluateShellMissingApprovalHandlerIsPreserved(t *testing.T) {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{}, nil))

	result, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command:          "go test ./...",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if result.Outcome != runtimepolicy.ShellOutcomeDenied || result.Allowed {
		t.Fatalf("expected denied shell result, got %+v", result)
	}
	if result.ApprovalStatus != runtimepolicy.ApprovalStatusUnavailable {
		t.Fatalf("expected unavailable approval status, got %+v", result)
	}
	if result.AccessDecision.Source != policy.DecisionSourceMissingApprovalHandler {
		t.Fatalf("expected missing-handler source, got %+v", result)
	}
}

type stubApprovalHandler struct {
	shellScope policy.ApprovalScope
}

func (h stubApprovalHandler) ApproveFile(context.Context, policy.FileRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeDeny, nil
}

func (h stubApprovalHandler) ApproveShell(context.Context, policy.ShellRequest) (policy.ApprovalScope, error) {
	return h.shellScope, nil
}

func (h stubApprovalHandler) ApproveNetwork(context.Context, policy.NetworkRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeDeny, nil
}

type countingApprovalHandler struct {
	shellScope policy.ApprovalScope
	shellCalls int
}

func (h *countingApprovalHandler) ApproveFile(context.Context, policy.FileRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeDeny, nil
}

func (h *countingApprovalHandler) ApproveShell(context.Context, policy.ShellRequest) (policy.ApprovalScope, error) {
	h.shellCalls++
	return h.shellScope, nil
}

func (h *countingApprovalHandler) ApproveNetwork(context.Context, policy.NetworkRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeDeny, nil
}
