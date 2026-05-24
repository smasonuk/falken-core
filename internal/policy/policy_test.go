package policy_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/policy"
)

func TestBlockedFileRuleOverridesAllowedFileRule(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		BlockedFiles: []policy.FileRule{{Path: "/workspace", Match: policy.MatchPrefix}},
		AllowedFiles: []policy.FileRule{{Path: "/workspace/file.txt", Match: policy.MatchExact}},
	}, nil)

	decision, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
		Path: "/workspace/file.txt",
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("EvaluateFile: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected blocked rule to win")
	}
	if decision.Source != policy.DecisionSourceBlockedRule {
		t.Fatalf("decision source = %q, want %q", decision.Source, policy.DecisionSourceBlockedRule)
	}
}

func TestFilePrefixRulesArePathBoundaryAware(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		AllowedFiles: []policy.FileRule{{
			Path:  "/repo/foo",
			Match: policy.MatchPrefix,
			Modes: []policy.FileAccessMode{policy.FileAccessRead},
		}},
		StrictFileAllowlist: true,
	}, nil)

	tests := []struct {
		path string
		want bool
	}{
		{path: "/repo/foo", want: true},
		{path: "/repo/foo/bar.txt", want: true},
		{path: "/repo/foobar", want: false},
		{path: `C:\repo\foo\bar.txt`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			decision, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
				Path: tt.path,
				Mode: policy.FileAccessRead,
			})
			if err != nil {
				t.Fatalf("EvaluateFile: %v", err)
			}
			if decision.Allowed != tt.want {
				t.Fatalf("decision = %+v, want allowed=%v", decision, tt.want)
			}
		})
	}
}

func TestFilePrefixRulesNormalizeWindowsSeparators(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		AllowedFiles: []policy.FileRule{{
			Path:  `C:\repo\foo`,
			Match: policy.MatchPrefix,
			Modes: []policy.FileAccessMode{policy.FileAccessRead},
		}},
		StrictFileAllowlist: true,
	}, nil)

	allowed, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
		Path: `C:\repo\foo\bar.txt`,
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("allowed EvaluateFile: %v", err)
	}
	if !allowed.Allowed {
		t.Fatalf("allowed decision = %+v, want allowed", allowed)
	}
	denied, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
		Path: `C:\repo\foobar`,
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("denied EvaluateFile: %v", err)
	}
	if denied.Allowed {
		t.Fatalf("denied decision = %+v, want denied", denied)
	}
}

func TestReplaceProjectApprovalsPreservesOtherPolicyState(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		BlockedShell: []policy.ShellRule{{Command: "blocked ", Match: policy.MatchPrefix}},
	}, stubApprovalHandler{shellScope: policy.ApprovalScopeSession})

	decision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command:          "session-only run",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell session approval: %v", err)
	}
	if !decision.Allowed || decision.Source != policy.DecisionSourceApprovalGrantedSession {
		t.Fatalf("decision = %+v, want granted session approval", decision)
	}

	engine.ReplaceProjectApprovals(nil, []policy.ShellRule{{Command: "project ", Match: policy.MatchPrefix}}, nil)

	decision, err = engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command:          "session-only run",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell after replace: %v", err)
	}
	if !decision.Allowed || decision.Source != policy.DecisionSourceSessionApproval {
		t.Fatalf("decision = %+v, want preserved session approval", decision)
	}
	blocked, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command:          "blocked command",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell blocked after replace: %v", err)
	}
	if blocked.Allowed || blocked.Source != policy.DecisionSourceBlockedRule {
		t.Fatalf("blocked decision = %+v, want blocked rule", blocked)
	}
	project, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command: "project run",
	})
	if err != nil {
		t.Fatalf("EvaluateShell project after replace: %v", err)
	}
	if !project.Allowed || project.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("project decision = %+v, want replacement project approval", project)
	}
}

func TestBlockedShellRuleOverridesAllowedShellRule(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		BlockedShell: []policy.ShellRule{{Command: "rm", Match: policy.MatchPrefix}},
		AllowedShell: []policy.ShellRule{{Command: "rm -rf /tmp/example", Match: policy.MatchExact}},
	}, nil)

	decision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{Command: "rm -rf /tmp/example"})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected blocked rule to win")
	}
	if decision.Source != policy.DecisionSourceBlockedRule {
		t.Fatalf("decision source = %q, want %q", decision.Source, policy.DecisionSourceBlockedRule)
	}
}

func TestBlockedShellRuleHardDeniesApprovalRequiredCommand(t *testing.T) {
	handler := &countingShellApprovalHandler{scope: policy.ApprovalScopeSession}
	engine := policy.NewEngine(policy.Config{
		BlockedShell:          []policy.ShellRule{{Command: "rm ", Match: policy.MatchPrefix}},
		ApprovalRequiredShell: []policy.ShellRule{{Command: "rm ", Match: policy.MatchPrefix}},
	}, handler)

	decision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{Command: "rm -rf /tmp/example"})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if decision.Allowed || decision.Source != policy.DecisionSourceBlockedRule {
		t.Fatalf("decision = %+v, want hard blocked rule", decision)
	}
	if handler.calls != 0 {
		t.Fatalf("approval calls = %d, want 0", handler.calls)
	}
}

func TestApprovalRequiredShellWithoutHandlerDenies(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		ApprovalRequiredShell: []policy.ShellRule{{Command: "rm ", Match: policy.MatchPrefix}},
	}, nil)

	decision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{Command: "rm -rf /tmp/example"})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if decision.Allowed || decision.Source != policy.DecisionSourceMissingApprovalHandler {
		t.Fatalf("decision = %+v, want missing approval handler deny", decision)
	}
}

func TestApprovalRequiredShellOnceApprovalAllowsCommand(t *testing.T) {
	handler := &countingShellApprovalHandler{scope: policy.ApprovalScopeOnce}
	engine := policy.NewEngine(policy.Config{
		ApprovalRequiredShell: []policy.ShellRule{{Command: "rm ", Match: policy.MatchPrefix}},
	}, handler)

	decision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{Command: "rm -rf /tmp/example"})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if !decision.Allowed || decision.Source != policy.DecisionSourceApprovalGrantedOnce || decision.Scope != policy.ApprovalScopeOnce {
		t.Fatalf("decision = %+v, want one-time approval", decision)
	}
	if handler.calls != 1 {
		t.Fatalf("approval calls = %d, want 1", handler.calls)
	}
}

func TestApprovalRequiredShellDenyApprovalBlocksCommand(t *testing.T) {
	handler := &countingShellApprovalHandler{scope: policy.ApprovalScopeDeny}
	engine := policy.NewEngine(policy.Config{
		ApprovalRequiredShell: []policy.ShellRule{{Command: "rm ", Match: policy.MatchPrefix}},
	}, handler)

	decision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{Command: "rm -rf /tmp/example"})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if decision.Allowed || decision.Source != policy.DecisionSourceApprovalDenied || decision.Scope != policy.ApprovalScopeDeny {
		t.Fatalf("decision = %+v, want approval denied", decision)
	}
	if handler.calls != 1 {
		t.Fatalf("approval calls = %d, want 1", handler.calls)
	}
}

func TestApprovalRequiredShellSessionApprovalIsHonored(t *testing.T) {
	handler := &countingShellApprovalHandler{scope: policy.ApprovalScopeSession}
	engine := policy.NewEngine(policy.Config{
		ApprovalRequiredShell: []policy.ShellRule{{Command: "rm ", Match: policy.MatchPrefix}},
	}, handler)

	first, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{Command: "rm -rf /tmp/example"})
	if err != nil {
		t.Fatalf("first EvaluateShell: %v", err)
	}
	if !first.Allowed || first.Source != policy.DecisionSourceApprovalGrantedSession {
		t.Fatalf("first decision = %+v, want granted session approval", first)
	}

	second, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{Command: "rm -rf /tmp/example"})
	if err != nil {
		t.Fatalf("second EvaluateShell: %v", err)
	}
	if !second.Allowed || second.Source != policy.DecisionSourceSessionApproval {
		t.Fatalf("second decision = %+v, want previous session approval", second)
	}
	if handler.calls != 1 {
		t.Fatalf("approval calls = %d, want 1", handler.calls)
	}
}

func TestApprovalRequiredShellProjectApprovalIsHonoredExactly(t *testing.T) {
	handler := &countingShellApprovalHandler{scope: policy.ApprovalScopeProject}
	engine := policy.NewEngine(policy.Config{
		ApprovalRequiredShell: []policy.ShellRule{{Command: "git clean ", Match: policy.MatchPrefix}},
	}, handler)

	first, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{Command: "git clean -fdx"})
	if err != nil {
		t.Fatalf("first EvaluateShell: %v", err)
	}
	if !first.Allowed || first.Source != policy.DecisionSourceApprovalGrantedProject {
		t.Fatalf("first decision = %+v, want granted project approval", first)
	}

	second, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{Command: "git clean -fdx"})
	if err != nil {
		t.Fatalf("second EvaluateShell: %v", err)
	}
	if !second.Allowed || second.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("second decision = %+v, want exact project approval", second)
	}
	if handler.calls != 1 {
		t.Fatalf("approval calls = %d, want 1", handler.calls)
	}
}

func TestApprovalRequiredShellOverridesBroadProjectApproval(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		ProjectApprovedShell:  []policy.ShellRule{{Command: "git ", Match: policy.MatchPrefix}},
		ApprovalRequiredShell: []policy.ShellRule{{Command: "git clean ", Match: policy.MatchPrefix}},
	}, nil)

	decision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{Command: "git clean -fdx"})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if decision.Allowed || decision.Source != policy.DecisionSourceMissingApprovalHandler {
		t.Fatalf("decision = %+v, want approval-required rule before broad project approval", decision)
	}
}

func TestBlockedNetworkRuleOverridesAllowedNetworkRule(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		BlockedNetwork: []policy.NetworkRule{{Host: "example.com", Match: policy.MatchSuffix}},
		AllowedNetwork: []policy.NetworkRule{{Host: "api.example.com", Match: policy.MatchExact}},
	}, nil)

	decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{Host: "api.example.com"})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected blocked rule to win")
	}
	if decision.Source != policy.DecisionSourceBlockedRule {
		t.Fatalf("decision source = %q, want %q", decision.Source, policy.DecisionSourceBlockedRule)
	}
}

func TestSessionScopedFileApprovalIsHonored(t *testing.T) {
	handler := stubApprovalHandler{fileScope: policy.ApprovalScopeSession}
	engine := policy.NewEngine(policy.Config{}, handler)

	first, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
		Path:             "/workspace/file.txt",
		Mode:             policy.FileAccessRead,
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("first EvaluateFile: %v", err)
	}
	if !first.Allowed || first.Source != policy.DecisionSourceApprovalGrantedSession {
		t.Fatalf("first decision = %+v", first)
	}

	second, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
		Path: "/workspace/file.txt",
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("second EvaluateFile: %v", err)
	}
	if !second.Allowed || second.Source != policy.DecisionSourceSessionApproval {
		t.Fatalf("second decision = %+v", second)
	}
	if len(engine.Config().ProjectApprovedFiles) != 0 {
		t.Fatalf("expected session approval to stay out of project config, got %#v", engine.Config().ProjectApprovedFiles)
	}
}

func TestSessionScopedShellApprovalIsHonored(t *testing.T) {
	handler := stubApprovalHandler{shellScope: policy.ApprovalScopeSession}
	engine := policy.NewEngine(policy.Config{}, handler)

	first, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command:          "custom-tool --flag",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("first EvaluateShell: %v", err)
	}
	if !first.Allowed || first.Source != policy.DecisionSourceApprovalGrantedSession {
		t.Fatalf("first decision = %+v", first)
	}

	second, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command: "custom-tool --flag",
	})
	if err != nil {
		t.Fatalf("second EvaluateShell: %v", err)
	}
	if !second.Allowed || second.Source != policy.DecisionSourceSessionApproval {
		t.Fatalf("second decision = %+v", second)
	}
	if len(engine.Config().ProjectApprovedShell) != 0 {
		t.Fatalf("expected session approval to stay out of project config, got %#v", engine.Config().ProjectApprovedShell)
	}
}

func TestSessionScopedNetworkApprovalIsHonored(t *testing.T) {
	handler := stubApprovalHandler{networkScope: policy.ApprovalScopeSession}
	engine := policy.NewEngine(policy.Config{}, handler)

	first, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host:             "api.example.net",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("first EvaluateNetwork: %v", err)
	}
	if !first.Allowed || first.Source != policy.DecisionSourceApprovalGrantedSession {
		t.Fatalf("first decision = %+v", first)
	}

	second, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host: "api.example.net",
	})
	if err != nil {
		t.Fatalf("second EvaluateNetwork: %v", err)
	}
	if !second.Allowed || second.Source != policy.DecisionSourceSessionApproval {
		t.Fatalf("second decision = %+v", second)
	}
	if len(engine.Config().ProjectApprovedNet) != 0 {
		t.Fatalf("expected session approval to stay out of project config, got %#v", engine.Config().ProjectApprovedNet)
	}
}

func TestProjectScopedFileApprovalIsHonored(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		ProjectApprovedFiles: []policy.FileRule{{
			Path:  "/workspace/file.txt",
			Match: policy.MatchExact,
			Modes: []policy.FileAccessMode{policy.FileAccessRead},
		}},
	}, nil)

	decision, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
		Path: "/workspace/file.txt",
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("EvaluateFile: %v", err)
	}
	if !decision.Allowed || decision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestProjectScopedShellApprovalIsHonored(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		ProjectApprovedShell: []policy.ShellRule{{
			Command: "custom-tool",
			Match:   policy.MatchPrefix,
		}},
	}, nil)

	decision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command: "custom-tool --do-thing",
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if !decision.Allowed || decision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestProjectScopedNetworkApprovalIsHonored(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		ProjectApprovedNet: []policy.NetworkRule{{
			Host:  ".example.net",
			Match: policy.MatchSuffix,
		}},
	}, nil)

	decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host: "api.example.net",
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if !decision.Allowed || decision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestStrictFileAllowlistDeniesUnmatchedAccess(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictFileAllowlist: true,
	}, nil)

	decision, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
		Path: "/workspace/unmatched.txt",
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("EvaluateFile: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected strict file allowlist deny")
	}
	if decision.Source != policy.DecisionSourceStrictAllowlistDeny {
		t.Fatalf("decision source = %q, want %q", decision.Source, policy.DecisionSourceStrictAllowlistDeny)
	}
}

func TestStrictCommandAllowlistDeniesUnmatchedShellAccess(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictCommandAllowlist: true,
	}, nil)

	decision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command: "unknown-command",
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected strict command allowlist deny")
	}
	if decision.Source != policy.DecisionSourceStrictAllowlistDeny {
		t.Fatalf("decision source = %q, want %q", decision.Source, policy.DecisionSourceStrictAllowlistDeny)
	}
}

func TestNetworkStrictAllowlistDeniesUnmatchedHost(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictNetworkAllowlist: true,
	}, nil)

	decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host: "unmatched.example.net",
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected strict network allowlist deny")
	}
	if decision.Source != policy.DecisionSourceStrictAllowlistDeny {
		t.Fatalf("decision source = %q, want %q", decision.Source, policy.DecisionSourceStrictAllowlistDeny)
	}
}

func TestNetworkStrictAllowlistAllowsAllowedHost(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictNetworkAllowlist: true,
		AllowedNetwork: []policy.NetworkRule{{
			Host:  "api.example.net",
			Match: policy.MatchExact,
		}},
	}, nil)

	decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host: "api.example.net",
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if !decision.Allowed || decision.Source != policy.DecisionSourceAllowedRule {
		t.Fatalf("decision = %+v, want allowed rule", decision)
	}
}

func TestNetworkStrictAllowlistHonorsAllowedPorts(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictNetworkAllowlist: true,
		AllowedNetwork: []policy.NetworkRule{{
			Host:  "github.com",
			Match: policy.MatchExact,
			Ports: []int{443},
		}},
	}, nil)

	allowed, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host: "github.com",
		Port: 443,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork github.com:443: %v", err)
	}
	if !allowed.Allowed || allowed.Source != policy.DecisionSourceAllowedRule {
		t.Fatalf("github.com:443 decision = %+v, want allowed rule", allowed)
	}

	denied, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host: "github.com",
		Port: 22,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork github.com:22: %v", err)
	}
	if denied.Allowed || denied.Source != policy.DecisionSourceStrictAllowlistDeny {
		t.Fatalf("github.com:22 decision = %+v, want strict allowlist deny", denied)
	}
}

func TestNetworkStrictAllowlistHonorsSuffixPorts(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictNetworkAllowlist: true,
		AllowedNetwork: []policy.NetworkRule{{
			Host:  ".github.com",
			Match: policy.MatchSuffix,
			Ports: []int{443},
		}},
	}, nil)

	tests := []struct {
		name    string
		host    string
		port    int
		allowed bool
	}{
		{name: "suffix port", host: "api.github.com", port: 443, allowed: true},
		{name: "suffix wrong port", host: "api.github.com", port: 80, allowed: false},
		{name: "evil suffix", host: "evilgithub.com", port: 443, allowed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
				Host: tt.host,
				Port: tt.port,
			})
			if err != nil {
				t.Fatalf("EvaluateNetwork: %v", err)
			}
			if decision.Allowed != tt.allowed {
				t.Fatalf("decision = %+v, allowed = %v", decision, tt.allowed)
			}
		})
	}
}

func TestStrictHostedNetworkAllowlist(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictNetworkAllowlist: true,
		AllowedNetwork: []policy.NetworkRule{
			{Host: "github.com", Match: policy.MatchExact, Ports: []int{443}},
			{Host: ".github.com", Match: policy.MatchSuffix, Ports: []int{443}},
			{Host: "registry.npmjs.org", Match: policy.MatchExact, Ports: []int{443}},
		},
	}, nil)

	tests := []struct {
		name    string
		host    string
		port    int
		allowed bool
	}{
		{name: "github https", host: "github.com", port: 443, allowed: true},
		{name: "github normalized fqdn", host: "GITHUB.COM.", port: 443, allowed: true},
		{name: "github api https", host: "api.github.com", port: 443, allowed: true},
		{name: "github api http", host: "api.github.com", port: 80, allowed: false},
		{name: "github root denied by suffix alone", host: "github.com", port: 443, allowed: true},
		{name: "objects not allowed", host: "objects.githubusercontent.com", port: 443, allowed: false},
		{name: "github ssh", host: "github.com", port: 22, allowed: false},
		{name: "evil suffix", host: "evilgithub.com", port: 443, allowed: false},
		{name: "npm registry https", host: "registry.npmjs.org", port: 443, allowed: true},
		{name: "npm registry http", host: "registry.npmjs.org", port: 80, allowed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
				Host: tt.host,
				Port: tt.port,
			})
			if err != nil {
				t.Fatalf("EvaluateNetwork: %v", err)
			}
			if decision.Allowed != tt.allowed {
				t.Fatalf("%s:%d decision = %+v, allowed = %v", tt.host, tt.port, decision, tt.allowed)
			}
		})
	}
}

func TestNetworkSuffixRuleDoesNotMatchRootOrLookalikeDomain(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictNetworkAllowlist: true,
		AllowedNetwork: []policy.NetworkRule{{
			Host:  ".github.com",
			Match: policy.MatchSuffix,
			Ports: []int{443},
		}},
	}, nil)

	tests := []struct {
		name    string
		host    string
		allowed bool
	}{
		{name: "subdomain", host: "api.github.com", allowed: true},
		{name: "root", host: "github.com", allowed: false},
		{name: "lookalike", host: "evilgithub.com", allowed: false},
		{name: "normalized subdomain", host: "API.GITHUB.COM.", allowed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
				Host: tt.host,
				Port: 443,
			})
			if err != nil {
				t.Fatalf("EvaluateNetwork: %v", err)
			}
			if decision.Allowed != tt.allowed {
				t.Fatalf("%s decision = %+v, allowed = %v", tt.host, decision, tt.allowed)
			}
		})
	}
}

func TestNetworkRulesWithoutPortsKeepMatchingAnyPort(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictNetworkAllowlist: true,
		AllowedNetwork: []policy.NetworkRule{{
			Host:  "github.com",
			Match: policy.MatchExact,
		}},
	}, nil)

	for _, port := range []int{0, 22, 443} {
		decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
			Host: "github.com",
			Port: port,
		})
		if err != nil {
			t.Fatalf("EvaluateNetwork port %d: %v", port, err)
		}
		if !decision.Allowed {
			t.Fatalf("port %d decision = %+v, want allowed", port, decision)
		}
	}
}

func TestNetworkRulesWithPortsDoNotMatchUnknownPort(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictNetworkAllowlist: true,
		AllowedNetwork: []policy.NetworkRule{{
			Host:  "github.com",
			Match: policy.MatchExact,
			Ports: []int{443},
		}},
	}, nil)

	decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host: "github.com",
		Port: 0,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("decision = %+v, want denied when request port is unknown", decision)
	}
}

func TestNetworkEvaluationDeniesEmptyHost(t *testing.T) {
	engine := policy.NewEngine(policy.Config{}, nil)

	decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host: " . ",
		Port: 443,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("decision = %+v, want empty host denied", decision)
	}
}

func TestNetworkStrictAllowlistHonorsProjectApproval(t *testing.T) {
	engine := policy.NewEngine(policy.Config{
		StrictNetworkAllowlist: true,
		ProjectApprovedNet: []policy.NetworkRule{{
			Host:  ".example.net",
			Match: policy.MatchSuffix,
		}},
	}, nil)

	decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host: "api.example.net",
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if !decision.Allowed || decision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("decision = %+v, want project approval", decision)
	}
}

func TestNetworkStrictAllowlistDeniesFreshApprovalRequest(t *testing.T) {
	handler := stubApprovalHandler{networkScope: policy.ApprovalScopeSession}
	engine := policy.NewEngine(policy.Config{StrictNetworkAllowlist: true}, handler)

	decision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host:             "api.example.net",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if decision.Allowed || decision.Source != policy.DecisionSourceStrictAllowlistDeny {
		t.Fatalf("decision = %+v, want strict allowlist deny before fresh approval", decision)
	}
}

func TestNonStrictModeAllowsUnmatchedAccess(t *testing.T) {
	engine := policy.NewEngine(policy.Config{}, nil)

	fileDecision, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
		Path: "/workspace/unmatched.txt",
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("EvaluateFile: %v", err)
	}
	if !fileDecision.Allowed || fileDecision.Source != policy.DecisionSourceDefaultAllow {
		t.Fatalf("file decision = %+v", fileDecision)
	}

	shellDecision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command: "echo hi",
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if !shellDecision.Allowed || shellDecision.Source != policy.DecisionSourceDefaultAllow {
		t.Fatalf("shell decision = %+v", shellDecision)
	}
}

func TestMissingApprovalHandlerDeniesByDefault(t *testing.T) {
	engine := policy.NewEngine(policy.Config{}, nil)

	fileDecision, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
		Path:             "/workspace/approval.txt",
		Mode:             policy.FileAccessRead,
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateFile: %v", err)
	}
	if fileDecision.Allowed || fileDecision.Source != policy.DecisionSourceMissingApprovalHandler {
		t.Fatalf("file decision = %+v", fileDecision)
	}

	shellDecision, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command:          "requires-approval",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if shellDecision.Allowed || shellDecision.Source != policy.DecisionSourceMissingApprovalHandler {
		t.Fatalf("shell decision = %+v", shellDecision)
	}

	networkDecision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host:             "approval.example.net",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if networkDecision.Allowed || networkDecision.Source != policy.DecisionSourceMissingApprovalHandler {
		t.Fatalf("network decision = %+v", networkDecision)
	}
}

func TestProjectApprovalsPersistThroughConfigRoundTrip(t *testing.T) {
	engine := policy.NewEngine(policy.Config{}, stubApprovalHandler{
		fileScope:    policy.ApprovalScopeProject,
		shellScope:   policy.ApprovalScopeProject,
		networkScope: policy.ApprovalScopeProject,
	})

	if _, err := engine.EvaluateFile(context.Background(), policy.FileRequest{
		Path:             "/workspace/file.txt",
		Mode:             policy.FileAccessRead,
		ApprovalRequired: true,
	}); err != nil {
		t.Fatalf("EvaluateFile: %v", err)
	}
	if _, err := engine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command:          "custom-tool --flag",
		ApprovalRequired: true,
	}); err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if _, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host:             "api.example.net",
		ApprovalRequired: true,
	}); err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}

	config := engine.Config()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded policy.Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if decoded.StrictFileAllowlist != config.StrictFileAllowlist ||
		decoded.StrictCommandAllowlist != config.StrictCommandAllowlist ||
		decoded.StrictNetworkAllowlist != config.StrictNetworkAllowlist {
		t.Fatalf("decoded strict flags = %+v, want %+v", decoded, config)
	}
	if !reflect.DeepEqual(decoded.ProjectApprovedFiles, config.ProjectApprovedFiles) {
		t.Fatalf("decoded project file approvals = %#v, want %#v", decoded.ProjectApprovedFiles, config.ProjectApprovedFiles)
	}
	if !reflect.DeepEqual(decoded.ProjectApprovedShell, config.ProjectApprovedShell) {
		t.Fatalf("decoded project shell approvals = %#v, want %#v", decoded.ProjectApprovedShell, config.ProjectApprovedShell)
	}
	if !reflect.DeepEqual(decoded.ProjectApprovedNet, config.ProjectApprovedNet) {
		t.Fatalf("decoded project network approvals = %#v, want %#v", decoded.ProjectApprovedNet, config.ProjectApprovedNet)
	}

	roundTrip := policy.NewEngine(decoded, nil)
	fileDecision, err := roundTrip.EvaluateFile(context.Background(), policy.FileRequest{
		Path: "/workspace/file.txt",
		Mode: policy.FileAccessRead,
	})
	if err != nil {
		t.Fatalf("round-trip EvaluateFile: %v", err)
	}
	if !fileDecision.Allowed || fileDecision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("file decision after round-trip = %+v", fileDecision)
	}

	shellDecision, err := roundTrip.EvaluateShell(context.Background(), policy.ShellRequest{
		Command: "custom-tool --flag",
	})
	if err != nil {
		t.Fatalf("round-trip EvaluateShell: %v", err)
	}
	if !shellDecision.Allowed || shellDecision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("shell decision after round-trip = %+v", shellDecision)
	}

	networkDecision, err := roundTrip.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host: "api.example.net",
	})
	if err != nil {
		t.Fatalf("round-trip EvaluateNetwork: %v", err)
	}
	if !networkDecision.Allowed || networkDecision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("network decision after round-trip = %+v", networkDecision)
	}
}

func TestApprovalHandlerCanReadEngineConfigWithoutDeadlock(t *testing.T) {
	handler := &reentrantApprovalHandler{scope: policy.ApprovalScopeSession}
	engine := policy.NewEngine(policy.Config{}, handler)
	handler.engine = engine

	tests := []struct {
		name     string
		evaluate func() (policy.Decision, error)
	}{
		{
			name: "file",
			evaluate: func() (policy.Decision, error) {
				return engine.EvaluateFile(context.Background(), policy.FileRequest{
					Path:             "/workspace/file.txt",
					Mode:             policy.FileAccessRead,
					ApprovalRequired: true,
				})
			},
		},
		{
			name: "shell",
			evaluate: func() (policy.Decision, error) {
				return engine.EvaluateShell(context.Background(), policy.ShellRequest{
					Command:          "custom-tool --flag",
					ApprovalRequired: true,
				})
			},
		},
		{
			name: "network",
			evaluate: func() (policy.Decision, error) {
				return engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
					Host:             "api.example.net",
					ApprovalRequired: true,
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			done := make(chan struct {
				decision policy.Decision
				err      error
			}, 1)
			go func() {
				decision, err := test.evaluate()
				done <- struct {
					decision policy.Decision
					err      error
				}{decision: decision, err: err}
			}()

			select {
			case got := <-done:
				if got.err != nil {
					t.Fatalf("evaluate: %v", got.err)
				}
				if !got.decision.Allowed || got.decision.Source != policy.DecisionSourceApprovalGrantedSession {
					t.Fatalf("decision = %+v, want session approval", got.decision)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("approval handler deadlocked while reading engine config")
			}
		})
	}
}

type stubApprovalHandler struct {
	fileScope    policy.ApprovalScope
	shellScope   policy.ApprovalScope
	networkScope policy.ApprovalScope
}

func (h stubApprovalHandler) ApproveFile(context.Context, policy.FileRequest) (policy.ApprovalScope, error) {
	return h.fileScope, nil
}

func (h stubApprovalHandler) ApproveShell(context.Context, policy.ShellRequest) (policy.ApprovalScope, error) {
	return h.shellScope, nil
}

func (h stubApprovalHandler) ApproveNetwork(context.Context, policy.NetworkRequest) (policy.ApprovalScope, error) {
	return h.networkScope, nil
}

type countingShellApprovalHandler struct {
	scope policy.ApprovalScope
	calls int
}

func (h *countingShellApprovalHandler) ApproveFile(context.Context, policy.FileRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeDeny, nil
}

func (h *countingShellApprovalHandler) ApproveShell(context.Context, policy.ShellRequest) (policy.ApprovalScope, error) {
	h.calls++
	return h.scope, nil
}

func (h *countingShellApprovalHandler) ApproveNetwork(context.Context, policy.NetworkRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeDeny, nil
}

type reentrantApprovalHandler struct {
	engine *policy.Engine
	scope  policy.ApprovalScope
}

func (h *reentrantApprovalHandler) ApproveFile(context.Context, policy.FileRequest) (policy.ApprovalScope, error) {
	_ = h.engine.Config()
	return h.scope, nil
}

func (h *reentrantApprovalHandler) ApproveShell(context.Context, policy.ShellRequest) (policy.ApprovalScope, error) {
	_ = h.engine.Config()
	return h.scope, nil
}

func (h *reentrantApprovalHandler) ApproveNetwork(context.Context, policy.NetworkRequest) (policy.ApprovalScope, error) {
	_ = h.engine.Config()
	return h.scope, nil
}
