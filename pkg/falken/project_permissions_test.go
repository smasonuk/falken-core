package falken

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/state"
)

func TestSessionStartCreatesDefaultProjectPermissions(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	session := newProjectPermissionsSession(t, workspace, "")
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	permissions, err := session.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions: %v", err)
	}
	if len(permissions.Files) == 0 {
		t.Fatal("default file permissions were not written")
	}
	if len(permissions.Shell) == 0 {
		t.Fatal("default shell permissions were not written")
	}
	if len(permissions.Network) == 0 {
		t.Fatal("default network permissions were not written")
	}
	if !hasFileRule(permissions.Files, policy.FileRule{
		Path:  workspace,
		Match: policy.MatchPrefix,
		Modes: []policy.FileAccessMode{policy.FileAccessRead, policy.FileAccessWrite, policy.FileAccessCreate},
	}) {
		t.Fatalf("file permissions = %+v, want workspace read/write/create prefix", permissions.Files)
	}
	if !hasShellRule(permissions.Shell, policy.ShellRule{Command: "git ", Match: policy.MatchPrefix}) {
		t.Fatalf("shell permissions missing git prefix: %+v", permissions.Shell)
	}
	if !hasNetworkRule(permissions.Network, policy.NetworkRule{Host: ".github.com", Match: policy.MatchSuffix}) {
		t.Fatalf("network permissions missing safe github suffix: %+v", permissions.Network)
	}
	if hasNetworkRule(permissions.Network, policy.NetworkRule{Host: "github.com", Match: policy.MatchSuffix}) {
		t.Fatalf("network permissions include unsafe github suffix: %+v", permissions.Network)
	}
	metadata, found, err := state.ReadMetadata(session.layout)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !found {
		t.Fatal("metadata missing after Start")
	}
	if !metadata.ProjectPermissionsInitialized || metadata.ProjectPermissionsVersion != state.ProjectPermissionsDefaultsVersion {
		t.Fatalf("metadata permissions init = (%t, %d), want initialized version %d", metadata.ProjectPermissionsInitialized, metadata.ProjectPermissionsVersion, state.ProjectPermissionsDefaultsVersion)
	}
}

func TestDefaultApprovalRequiredShellRulesGateRiskyCommands(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	session := newProjectPermissionsSession(t, workspace, "")
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	allowed, err := session.resources.runtime.executionPolicy.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command: "git status",
	})
	if err != nil {
		t.Fatalf("EvaluateShell git status: %v", err)
	}
	if !allowed.Allowed || allowed.AccessDecision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("git status result = %+v, want default project approval", allowed)
	}

	for _, command := range []string{"git clean -fdx", "rm -rf foo", "find . -delete"} {
		t.Run(command, func(t *testing.T) {
			result, err := session.resources.runtime.executionPolicy.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
				Command: command,
			})
			if err != nil {
				t.Fatalf("EvaluateShell: %v", err)
			}
			if result.Allowed || result.AccessDecision.Source != policy.DecisionSourceApprovalDenied {
				t.Fatalf("result = %+v, want approval-required denial from default handler", result)
			}
		})
	}

	permissions, err := session.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions: %v", err)
	}
	if hasShellRule(permissions.Shell, policy.ShellRule{Command: "rm ", Match: policy.MatchPrefix}) {
		t.Fatalf("approval-required rm rule was persisted as project approval: %+v", permissions.Shell)
	}
}

func TestSessionStartDoesNotDuplicateDefaultProjectPermissions(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	first := newProjectPermissionsSession(t, workspace, stateDir)
	if err := first.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	before, err := first.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions before close: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := newProjectPermissionsSession(t, workspace, stateDir)
	if err := second.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	after, err := second.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions after restart: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("permissions changed after restart:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestSessionStartPreservesDeletedProjectPermissions(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	first := newProjectPermissionsSession(t, workspace, stateDir)
	if err := first.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := first.WriteProjectPermissions(ProjectPermissions{}); err != nil {
		t.Fatalf("WriteProjectPermissions empty: %v", err)
	}
	written, err := first.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions after empty write: %v", err)
	}
	if len(written.Files) != 0 || len(written.Shell) != 0 || len(written.Network) != 0 {
		t.Fatalf("permissions after empty write = %+v, want empty", written)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := newProjectPermissionsSession(t, workspace, stateDir)
	if err := second.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	got, err := second.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions after restart: %v", err)
	}
	if len(got.Files) != 0 || len(got.Shell) != 0 || len(got.Network) != 0 {
		t.Fatalf("permissions after restart = %+v, want empty", got)
	}
}

func TestSessionStartInitializesDefaultsForExistingMetadataWithoutPermissions(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	session := newProjectPermissionsSession(t, workspace, stateDir)
	writeLegacyMetadata(t, session.layout)

	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	permissions, err := session.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions: %v", err)
	}
	if len(permissions.Files) == 0 || len(permissions.Shell) == 0 || len(permissions.Network) == 0 {
		t.Fatalf("permissions = %+v, want defaults for legacy metadata", permissions)
	}
	metadata, _, err := state.ReadMetadata(session.layout)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !metadata.ProjectPermissionsInitialized {
		t.Fatalf("metadata = %+v, want permissions initialized", metadata)
	}
}

func TestSessionStartPreservesExistingPermissionsWhenMarkingInitialized(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	session := newProjectPermissionsSession(t, workspace, stateDir)
	writeLegacyMetadata(t, session.layout)
	custom := policy.Config{
		ProjectApprovedShell: []policy.ShellRule{{Command: "custom-tool ", Match: policy.MatchPrefix}},
	}
	if err := writeProjectApprovals(session.layout, custom); err != nil {
		t.Fatalf("writeProjectApprovals: %v", err)
	}

	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	permissions, err := session.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions: %v", err)
	}
	if len(permissions.Files) != 0 || len(permissions.Network) != 0 ||
		len(permissions.Shell) != 1 || permissions.Shell[0].Command != "custom-tool " || permissions.Shell[0].Match != MatchPrefix {
		t.Fatalf("permissions = %+v, want custom preserved", permissions)
	}
	metadata, _, err := state.ReadMetadata(session.layout)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !metadata.ProjectPermissionsInitialized {
		t.Fatalf("metadata = %+v, want permissions initialized", metadata)
	}
}

func TestSessionStartDoesNotRecreateMissingPermissionsAfterInitialized(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	session := newProjectPermissionsSession(t, workspace, stateDir)
	writeInitializedMetadata(t, session.layout)
	if err := os.Remove(session.Paths.ProjectPermissionsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove project permissions: %v", err)
	}

	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	permissions, err := session.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions: %v", err)
	}
	if len(permissions.Files) != 0 || len(permissions.Shell) != 0 || len(permissions.Network) != 0 {
		t.Fatalf("permissions = %+v, want empty after initialized metadata", permissions)
	}
}

func TestProjectPermissionsRoundTrip(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	session := newProjectPermissionsSession(t, workspace, "")
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	want := ProjectPermissions{
		Files: []FileRule{{
			Path:  filepath.Join(workspace, "src"),
			Match: MatchPrefix,
			Modes: []FileAccessMode{FileAccessRead, FileAccessCreate},
		}},
		Shell: []ShellRule{{
			Command: "custom-tool ",
			Match:   MatchPrefix,
		}},
		Network: []NetworkRule{{
			Host:  ".example.test",
			Match: MatchSuffix,
		}},
	}
	if err := session.WriteProjectPermissions(want); err != nil {
		t.Fatalf("WriteProjectPermissions: %v", err)
	}
	got, err := session.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("permissions = %+v, want %+v", got, want)
	}
}

func TestMergeAndWriteProjectApprovalsPreservesConcurrentRuntimeApprovals(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	layout, err := projectPermissionsLayout(workspace, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("projectPermissionsLayout: %v", err)
	}
	if err := state.EnsureLayoutState(layout); err != nil {
		t.Fatalf("EnsureLayoutState: %v", err)
	}

	fileRule := policy.FileRule{
		Path:  filepath.Join(workspace, "notes.txt"),
		Match: policy.MatchExact,
		Modes: []policy.FileAccessMode{policy.FileAccessRead},
	}
	shellRule := policy.ShellRule{Command: "go test ./...", Match: policy.MatchExact}
	for i := 0; i < 25; i++ {
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs <- mergeAndWriteProjectApprovals(layout, policy.Config{
				ProjectApprovedFiles: []policy.FileRule{fileRule},
			})
		}()
		go func() {
			defer wg.Done()
			errs <- mergeAndWriteProjectApprovals(layout, policy.Config{
				ProjectApprovedShell: []policy.ShellRule{shellRule},
			})
		}()
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("mergeAndWriteProjectApprovals: %v", err)
			}
		}
	}

	approvals, err := readProjectApprovals(layout)
	if err != nil {
		t.Fatalf("readProjectApprovals: %v", err)
	}
	if !hasFileRule(approvals.Files, fileRule) {
		t.Fatalf("files = %+v, want merged file approval", approvals.Files)
	}
	if !hasShellRule(approvals.Shell, shellRule) {
		t.Fatalf("shell = %+v, want merged shell approval", approvals.Shell)
	}
	if len(approvals.Files) != 1 || len(approvals.Shell) != 1 || len(approvals.Network) != 0 {
		t.Fatalf("approvals = %+v, want no duplicates", approvals)
	}
}

func TestWriteProjectApprovalsStillReplacesExactly(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	layout, err := projectPermissionsLayout(workspace, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("projectPermissionsLayout: %v", err)
	}
	if err := state.EnsureLayoutState(layout); err != nil {
		t.Fatalf("EnsureLayoutState: %v", err)
	}
	if err := writeProjectApprovals(layout, policy.Config{
		ProjectApprovedFiles: []policy.FileRule{{
			Path:  filepath.Join(workspace, "notes.txt"),
			Match: policy.MatchExact,
			Modes: []policy.FileAccessMode{policy.FileAccessRead},
		}},
		ProjectApprovedShell: []policy.ShellRule{{Command: "go test ./...", Match: policy.MatchExact}},
	}); err != nil {
		t.Fatalf("writeProjectApprovals first: %v", err)
	}
	networkRule := policy.NetworkRule{Host: "example.com", Match: policy.MatchExact}
	if err := writeProjectApprovals(layout, policy.Config{
		ProjectApprovedNet: []policy.NetworkRule{networkRule},
	}); err != nil {
		t.Fatalf("writeProjectApprovals replacement: %v", err)
	}

	approvals, err := readProjectApprovals(layout)
	if err != nil {
		t.Fatalf("readProjectApprovals: %v", err)
	}
	if len(approvals.Files) != 0 || len(approvals.Shell) != 0 {
		t.Fatalf("approvals = %+v, want file and shell approvals replaced away", approvals)
	}
	if len(approvals.Network) != 1 || !hasNetworkRule(approvals.Network, networkRule) {
		t.Fatalf("network approvals = %+v, want exact replacement network rule", approvals.Network)
	}
}

func TestWorkspaceProjectPermissionsRoundTripWithoutStartedSession(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	want := ProjectPermissions{
		Files: []FileRule{{
			Path:  workspace,
			Match: MatchPrefix,
			Modes: []FileAccessMode{FileAccessRead},
		}},
		Shell:   []ShellRule{{Command: "setup-tool ", Match: MatchPrefix}},
		Network: []NetworkRule{{Host: "setup.example.test", Match: MatchExact}},
	}

	if err := WriteProjectPermissionsForWorkspace(workspace, stateDir, want); err != nil {
		t.Fatalf("WriteProjectPermissionsForWorkspace: %v", err)
	}
	got, err := ReadProjectPermissionsForWorkspace(workspace, stateDir)
	if err != nil {
		t.Fatalf("ReadProjectPermissionsForWorkspace: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("permissions = %+v, want %+v", got, want)
	}

	paths, err := NewPaths(workspace, stateDir)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	if _, err := os.Stat(paths.ProjectPermissionsPath); err != nil {
		t.Fatalf("project permissions file stat: %v", err)
	}
	layout, err := projectPermissionsLayout(workspace, stateDir)
	if err != nil {
		t.Fatalf("projectPermissionsLayout: %v", err)
	}
	metadata, found, err := state.ReadMetadata(layout)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !found || !metadata.ProjectPermissionsInitialized {
		t.Fatalf("metadata = %+v found=%t, want permissions initialized", metadata, found)
	}
}

func TestWorkspaceReadInitializesDefaultProjectPermissions(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	permissions, err := ReadProjectPermissionsForWorkspace(workspace, stateDir)
	if err != nil {
		t.Fatalf("ReadProjectPermissionsForWorkspace: %v", err)
	}
	if len(permissions.Files) == 0 || len(permissions.Shell) == 0 || len(permissions.Network) == 0 {
		t.Fatalf("permissions = %+v, want defaults", permissions)
	}
}

func TestSessionStartUsesWorkspaceWrittenProjectPermissions(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	permissions := ProjectPermissions{
		Shell: []ShellRule{{Command: "setup-tool ", Match: MatchPrefix}},
	}
	if err := WriteProjectPermissionsForWorkspace(workspace, stateDir, permissions); err != nil {
		t.Fatalf("WriteProjectPermissionsForWorkspace: %v", err)
	}

	session := newProjectPermissionsSessionWithConfig(t, workspace, stateDir, SessionConfig{
		Execution: ExecutionConfig{Mode: ExecutionModeLocal},
		Policy: PolicyConfig{
			StrictCommandAllowlist: true,
		},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	result, err := session.resources.runtime.executionPolicy.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command: "setup-tool run",
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if !result.Allowed || result.AccessDecision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("result = %+v, want setup project approval", result)
	}
}

func TestEnsureDefaultProjectPermissionsWritesOnlyWhenFileMissing(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	session := newProjectPermissionsSession(t, workspace, stateDir)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.WriteProjectPermissions(ProjectPermissions{}); err != nil {
		t.Fatalf("WriteProjectPermissions empty: %v", err)
	}
	wrote, err := session.EnsureDefaultProjectPermissions()
	if err != nil {
		t.Fatalf("EnsureDefaultProjectPermissions after empty write: %v", err)
	}
	if wrote {
		t.Fatal("EnsureDefaultProjectPermissions wrote defaults despite existing empty permissions file")
	}
	if got, err := session.ReadProjectPermissions(); err != nil {
		t.Fatalf("ReadProjectPermissions: %v", err)
	} else if len(got.Files) != 0 || len(got.Shell) != 0 || len(got.Network) != 0 {
		t.Fatalf("permissions = %+v, want empty", got)
	}
}

func TestProjectPermissionsLifecycleErrors(t *testing.T) {
	setStateHomeEnv(t)

	session := newProjectPermissionsSession(t, tempWorkspace(t), "")
	if _, err := session.ReadProjectPermissions(); !errors.Is(err, ErrSessionNotStarted) {
		t.Fatalf("ReadProjectPermissions before Start error = %v, want ErrSessionNotStarted", err)
	}
	if err := session.WriteProjectPermissions(ProjectPermissions{}); !errors.Is(err, ErrSessionNotStarted) {
		t.Fatalf("WriteProjectPermissions before Start error = %v, want ErrSessionNotStarted", err)
	}
	if _, err := session.EnsureDefaultProjectPermissions(); !errors.Is(err, ErrSessionNotStarted) {
		t.Fatalf("EnsureDefaultProjectPermissions before Start error = %v, want ErrSessionNotStarted", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := session.ReadProjectPermissions(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("ReadProjectPermissions after Close error = %v, want ErrSessionClosed", err)
	}
	if err := session.WriteProjectPermissions(ProjectPermissions{}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("WriteProjectPermissions after Close error = %v, want ErrSessionClosed", err)
	}
	if _, err := session.EnsureDefaultProjectPermissions(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("EnsureDefaultProjectPermissions after Close error = %v, want ErrSessionClosed", err)
	}
}

func TestPolicyEngineLoadsSavedProjectPermissions(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	first := newProjectPermissionsSession(t, workspace, stateDir)
	if err := first.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	permissions := ProjectPermissions{
		Files: []FileRule{{
			Path:  filepath.Join(workspace, "allowed"),
			Match: MatchPrefix,
			Modes: []FileAccessMode{FileAccessRead, FileAccessWrite, FileAccessCreate},
		}},
		Shell: []ShellRule{{Command: "custom-tool ", Match: MatchPrefix}},
		Network: []NetworkRule{
			{Host: "downloads.example.test", Match: MatchExact},
			{Host: ".packages.example.test", Match: MatchSuffix},
		},
	}
	if err := first.WriteProjectPermissions(permissions); err != nil {
		t.Fatalf("WriteProjectPermissions: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := newProjectPermissionsSessionWithConfig(t, workspace, stateDir, SessionConfig{
		Execution: ExecutionConfig{Mode: ExecutionModeLocal},
		Policy: PolicyConfig{
			StrictFileAllowlist:    true,
			StrictCommandAllowlist: true,
			StrictNetworkAllowlist: true,
			BlockedShell:           []ShellRule{{Command: "custom-tool blocked", Match: MatchExact}},
		},
	})
	if err := second.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	evaluator := second.resources.runtime.executionPolicy

	fileResult, err := evaluator.EvaluateFile(context.Background(), runtimepolicy.FileRequest{
		Path:             filepath.Join(workspace, "allowed", "file.txt"),
		Mode:             policy.FileAccessWrite,
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateFile: %v", err)
	}
	if !fileResult.Allowed || fileResult.Decision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("file result = %+v, want project approval", fileResult)
	}

	shellResult, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command: "custom-tool build",
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if !shellResult.Allowed || shellResult.AccessDecision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("shell result = %+v, want project approval", shellResult)
	}

	blockedShell, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command:          "custom-tool blocked",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell blocked: %v", err)
	}
	if blockedShell.Allowed || blockedShell.AccessDecision.Source != policy.DecisionSourceBlockedRule {
		t.Fatalf("blocked shell result = %+v, want blocked rule", blockedShell)
	}

	networkResult, err := evaluator.EvaluateNetwork(context.Background(), runtimepolicy.NetworkRequest{
		Host:             "api.packages.example.test",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if !networkResult.Allowed || networkResult.Decision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("network result = %+v, want project approval", networkResult)
	}
}

func TestWriteProjectPermissionsUpdatesRunningPolicyEngine(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	session := newProjectPermissionsSessionWithConfig(t, workspace, "", SessionConfig{
		Execution: ExecutionConfig{Mode: ExecutionModeLocal},
		Policy: PolicyConfig{
			StrictFileAllowlist:    true,
			StrictCommandAllowlist: true,
			StrictNetworkAllowlist: true,
			BlockedShell:           []ShellRule{{Command: "custom-tool blocked", Match: MatchExact}},
		},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	evaluator := session.resources.runtime.executionPolicy
	externalFile := filepath.Join(t.TempDir(), "external.txt")

	assertDenied := func(label string) {
		t.Helper()
		fileResult, err := evaluator.EvaluateFile(context.Background(), runtimepolicy.FileRequest{
			Path:             externalFile,
			Mode:             policy.FileAccessRead,
			ApprovalRequired: true,
		})
		if err != nil {
			t.Fatalf("%s EvaluateFile: %v", label, err)
		}
		if fileResult.Allowed {
			t.Fatalf("%s file result = %+v, want denied", label, fileResult)
		}
		shellResult, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
			Command:          "custom-tool run",
			ApprovalRequired: true,
		})
		if err != nil {
			t.Fatalf("%s EvaluateShell: %v", label, err)
		}
		if shellResult.Allowed {
			t.Fatalf("%s shell result = %+v, want denied", label, shellResult)
		}
		networkResult, err := evaluator.EvaluateNetwork(context.Background(), runtimepolicy.NetworkRequest{
			Host:             "downloads.example.test",
			ApprovalRequired: true,
		})
		if err != nil {
			t.Fatalf("%s EvaluateNetwork: %v", label, err)
		}
		if networkResult.Allowed {
			t.Fatalf("%s network result = %+v, want denied", label, networkResult)
		}
	}

	assertDenied("before write")
	if err := session.WriteProjectPermissions(ProjectPermissions{
		Files: []FileRule{{
			Path:  externalFile,
			Match: MatchExact,
			Modes: []FileAccessMode{FileAccessRead},
		}},
		Shell:   []ShellRule{{Command: "custom-tool ", Match: MatchPrefix}},
		Network: []NetworkRule{{Host: "downloads.example.test", Match: MatchExact}},
	}); err != nil {
		t.Fatalf("WriteProjectPermissions: %v", err)
	}

	fileResult, err := evaluator.EvaluateFile(context.Background(), runtimepolicy.FileRequest{
		Path:             externalFile,
		Mode:             policy.FileAccessRead,
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateFile after write: %v", err)
	}
	if !fileResult.Allowed || fileResult.Decision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("file result after write = %+v, want project approval", fileResult)
	}
	shellResult, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command: "custom-tool run",
	})
	if err != nil {
		t.Fatalf("EvaluateShell after write: %v", err)
	}
	if !shellResult.Allowed || shellResult.AccessDecision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("shell result after write = %+v, want project approval", shellResult)
	}
	blockedShell, err := evaluator.EvaluateShell(context.Background(), runtimepolicy.ShellRequest{
		Command:          "custom-tool blocked",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell blocked after write: %v", err)
	}
	if blockedShell.Allowed || blockedShell.AccessDecision.Source != policy.DecisionSourceBlockedRule {
		t.Fatalf("blocked shell result after write = %+v, want blocked rule", blockedShell)
	}
	networkResult, err := evaluator.EvaluateNetwork(context.Background(), runtimepolicy.NetworkRequest{
		Host:             "downloads.example.test",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork after write: %v", err)
	}
	if !networkResult.Allowed || networkResult.Decision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("network result after write = %+v, want project approval", networkResult)
	}

	if err := session.WriteProjectPermissions(ProjectPermissions{}); err != nil {
		t.Fatalf("WriteProjectPermissions empty: %v", err)
	}
	assertDenied("after removal")
}

func TestDefaultProjectNetworkRulesUseSafeSuffixes(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	session := newProjectPermissionsSession(t, workspace, "")
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	permissions, err := session.ReadProjectPermissions()
	if err != nil {
		t.Fatalf("ReadProjectPermissions: %v", err)
	}
	for _, rule := range permissions.Network {
		if rule.Match == MatchSuffix && (rule.Host == "" || rule.Host[0] != '.') {
			t.Fatalf("unsafe default suffix rule: %+v", rule)
		}
	}

	engine := policy.NewEngine(policy.Config{
		ProjectApprovedNet:     permissions.Network,
		StrictNetworkAllowlist: true,
	}, nil)
	apiDecision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host:             "api.github.com",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork api.github.com: %v", err)
	}
	if !apiDecision.Allowed || apiDecision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("api.github.com decision = %+v, want project approval", apiDecision)
	}
	evilDecision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host:             "evilgithub.com",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork evilgithub.com: %v", err)
	}
	if evilDecision.Allowed {
		t.Fatalf("evilgithub.com decision = %+v, want denied", evilDecision)
	}
	rootDecision, err := engine.EvaluateNetwork(context.Background(), policy.NetworkRequest{
		Host:             "github.com",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateNetwork github.com: %v", err)
	}
	if !rootDecision.Allowed || rootDecision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("github.com decision = %+v, want exact project approval", rootDecision)
	}
}

func newProjectPermissionsSession(t *testing.T, workspace, stateDir string) *Session {
	t.Helper()
	return newProjectPermissionsSessionWithConfig(t, workspace, stateDir, SessionConfig{
		Execution: ExecutionConfig{Mode: ExecutionModeLocal},
	})
}

func writeLegacyMetadata(t *testing.T, layout state.Layout) {
	t.Helper()
	writeMetadata(t, layout, false)
}

func writeInitializedMetadata(t *testing.T, layout state.Layout) {
	t.Helper()
	writeMetadata(t, layout, true)
}

func writeMetadata(t *testing.T, layout state.Layout, permissionsInitialized bool) {
	t.Helper()
	if err := state.EnsureLayoutState(layout); err != nil {
		t.Fatalf("EnsureLayoutState: %v", err)
	}
	metadata := state.Metadata{
		WorkspaceRoot:                 layout.WorkspaceRoot,
		LayoutVersion:                 layout.LayoutVersion,
		CreatedAt:                     time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		LastUsedAt:                    time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		ProjectPermissionsInitialized: permissionsInitialized,
	}
	if permissionsInitialized {
		metadata.ProjectPermissionsVersion = state.ProjectPermissionsDefaultsVersion
	}
	if err := state.WriteMetadata(layout, metadata); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
}

func newProjectPermissionsSessionWithConfig(t *testing.T, workspace, stateDir string, config SessionConfig) *Session {
	t.Helper()
	session, err := newSessionWithConfig(workspace, stateDir, config)
	if err != nil {
		t.Fatalf("newSessionWithConfig: %v", err)
	}
	return session
}
