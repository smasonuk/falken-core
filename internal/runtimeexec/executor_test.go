package runtimeexec_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	statepkg "github.com/smasonuk/falken-core/internal/state"
)

func TestLocalExecutorBlockedShellCommandDoesNotExecute(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := newExecutor(policy.Config{
		BlockedShell: []policy.ShellRule{{
			Command: "touch marker",
			Match:   policy.MatchExact,
		}},
	})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "touch marker",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusBlocked || result.Executed {
		t.Fatalf("expected blocked non-executed result, got %+v", result)
	}
	if result.Policy.Outcome != runtimepolicy.ShellOutcomeDenied {
		t.Fatalf("expected denied shell policy outcome, got %+v", result.Policy)
	}
	if _, err := os.Stat(filepath.Join(root, "marker")); !os.IsNotExist(err) {
		t.Fatalf("blocked command should not create marker, stat err=%v", err)
	}
}

func TestLocalExecutorBlocksShellWriteBypass(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := newExecutor(policy.Config{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "echo hi > marker",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusBlocked || result.Executed {
		t.Fatalf("expected blocked shell-write result, got %+v", result)
	}
	if !result.Policy.BlockedByShellWriteBypass {
		t.Fatalf("expected shell-write bypass policy flag, got %+v", result.Policy)
	}
	if _, err := os.Stat(filepath.Join(root, "marker")); !os.IsNotExist(err) {
		t.Fatalf("shell-write bypass should not create marker, stat err=%v", err)
	}
}

func TestLocalExecutorSurfacesApprovalRequiredCommand(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	state := newExecutionState(t, root)
	executor := newExecutor(policy.Config{
		ApprovalRequiredShell: []policy.ShellRule{{Command: "rm ", Match: policy.MatchPrefix}},
	})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "rm -rf target",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusBlocked || result.Executed {
		t.Fatalf("expected blocked approval-required result, got %+v", result)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("approval-required command should not remove target: %v", err)
	}
}

func TestLocalExecutorRunsApprovalRequiredCommandWithApproval(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	state := newExecutionState(t, root)
	executor := newExecutorWithHandler(policy.Config{
		ApprovalRequiredShell: []policy.ShellRule{{Command: "rm ", Match: policy.MatchPrefix}},
	}, approveShellHandler{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "rm -rf target",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded || !result.Executed {
		t.Fatalf("expected approved command to run, got %+v", result)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target stat = %v, want removed", err)
	}
}

func TestLocalExecutorAllowedCommandExecutes(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := newExecutor(policy.Config{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "printf 'hello'",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded || !result.Executed {
		t.Fatalf("expected succeeded executed result, got %+v", result)
	}
	if result.Stdout != "hello" || result.CombinedOutput != "hello" {
		t.Fatalf("unexpected output: stdout=%q combined=%q", result.Stdout, result.CombinedOutput)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestLocalExecutorExitFailureIsStructured(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := newExecutor(policy.Config{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "exit 7",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusExitedNonZero || !result.Executed {
		t.Fatalf("expected exited-non-zero result, got %+v", result)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
	if result.ExitError == "" {
		t.Fatalf("expected exit error to be recorded, got %+v", result)
	}
}

func TestLocalExecutorLaunchFailureIsStructured(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{}, nil))
	executor := runtimeexec.NewLocalExecutorWithShell(evaluator, filepath.Join(root, "missing-shell"))

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "printf 'hello'",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusFailedToStart || result.Executed {
		t.Fatalf("expected failed-to-start result, got %+v", result)
	}
	if result.StartError == "" {
		t.Fatalf("expected start error to be recorded, got %+v", result)
	}
}

func TestLocalExecutorWorkingDirectoryIsHonored(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	state := newExecutionState(t, root)
	executor := newExecutor(policy.Config{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:    "pwd",
		WorkingDir: "subdir",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded {
		t.Fatalf("expected successful pwd command, got %+v", result)
	}
	wantSubdir := realPath(t, subdir)
	if got := filepath.Clean(strings.TrimSpace(result.Stdout)); got != wantSubdir {
		t.Fatalf("pwd output = %q, want %q", got, wantSubdir)
	}
	if result.WorkingDir != wantSubdir {
		t.Fatalf("result working dir = %q, want %q", result.WorkingDir, wantSubdir)
	}
	if result.HostWorkingDir != wantSubdir || result.ToolWorkingDir != "subdir" || result.SandboxWorkingDir != "/workspace/subdir" {
		t.Fatalf("path context = host %q tool %q sandbox %q", result.HostWorkingDir, result.ToolWorkingDir, result.SandboxWorkingDir)
	}
}

func TestLocalExecutorNormalizesSandboxMountWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	state := newExecutionState(t, root)
	executor := newExecutor(policy.Config{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:    "pwd",
		WorkingDir: "/workspace/subdir",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wantSubdir := realPath(t, subdir)
	if result.WorkingDir != wantSubdir || result.ToolWorkingDir != "subdir" || result.SandboxWorkingDir != "/workspace/subdir" {
		t.Fatalf("path context = working %q tool %q sandbox %q", result.WorkingDir, result.ToolWorkingDir, result.SandboxWorkingDir)
	}
}

func TestLocalExecutorEnvironmentOverridesAreHonored(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	state.SetEnv("FALKEN_EXEC_VALUE", "session")
	executor := newExecutor(policy.Config{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "printf '%s' \"$FALKEN_EXEC_VALUE\"",
		Env: map[string]string{
			"FALKEN_EXEC_VALUE": "request",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded {
		t.Fatalf("expected successful env command, got %+v", result)
	}
	if result.Stdout != "request" {
		t.Fatalf("stdout = %q, want request", result.Stdout)
	}
}

func TestLocalExecutorStreamsOutputChunks(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := newExecutor(policy.Config{})

	var chunks []runtimeexec.StreamChunk
	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "printf 'hello'",
		Stream: func(chunk runtimeexec.StreamChunk) {
			chunks = append(chunks, chunk)
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded {
		t.Fatalf("expected successful streamed command, got %+v", result)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one streamed chunk")
	}
	if chunks[0].Stream != runtimeexec.StreamStdout || string(chunks[0].Data) != "hello" {
		t.Fatalf("unexpected streamed chunk: %+v", chunks[0])
	}
}

func TestLocalExecutorStreamsMultipleChunksInOrder(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := newExecutor(policy.Config{})

	var streamed strings.Builder
	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "printf 'one'; sleep 0.05; printf 'two'",
		Stream: func(chunk runtimeexec.StreamChunk) {
			streamed.Write(chunk.Data)
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded {
		t.Fatalf("expected successful streamed command, got %+v", result)
	}
	if streamed.String() != "onetwo" {
		t.Fatalf("streamed output = %q, want onetwo", streamed.String())
	}
	if result.Stdout != "onetwo" {
		t.Fatalf("stdout = %q, want onetwo", result.Stdout)
	}
}

func TestLocalExecutorSmallOutputIsNotTruncated(t *testing.T) {
	root := t.TempDir()
	layout := newStateLayout(t, root)
	state := newExecutionStateForLayout(t, layout)
	executor := newExecutorWithHandler(policy.Config{}, approveShellHandler{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:         "printf 'small'",
		MaxInlineOutput: 10,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded {
		t.Fatalf("expected successful command, got %+v", result)
	}
	if result.Output.Truncated {
		t.Fatalf("expected output not to be truncated, got %+v", result.Output)
	}
	if result.Output.ArtifactPath != "" {
		t.Fatalf("expected no artifact path, got %+v", result.Output)
	}
	if _, err := os.Stat(layout.RecentArtifactRoot); !os.IsNotExist(err) {
		t.Fatalf("small output should not create artifact root, stat err=%v", err)
	}
}

func TestLocalExecutorTruncatesLargeOutputAndPersistsArtifact(t *testing.T) {
	root := t.TempDir()
	layout := newStateLayout(t, root)
	state := newExecutionStateForLayout(t, layout)
	executor := newExecutorWithHandler(policy.Config{}, approveShellHandler{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:         "printf 'abcdefghij'",
		MaxInlineOutput: 5,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded {
		t.Fatalf("expected successful command, got %+v", result)
	}
	if !result.Output.Truncated {
		t.Fatalf("expected output truncation, got %+v", result.Output)
	}
	if result.CombinedOutput != "abcde" || result.Stdout != "abcde" {
		t.Fatalf("expected truncated inline output, stdout=%q combined=%q", result.Stdout, result.CombinedOutput)
	}
	if result.Output.ArtifactPath == "" {
		t.Fatalf("expected artifact path, got %+v", result.Output)
	}
	if !strings.HasPrefix(result.Output.ArtifactPath, layout.RecentArtifactRoot+string(filepath.Separator)) {
		t.Fatalf("artifact path %q is not under canonical root %q", result.Output.ArtifactPath, layout.RecentArtifactRoot)
	}

	content, err := os.ReadFile(result.Output.ArtifactPath)
	if err != nil {
		t.Fatalf("ReadFile artifact: %v", err)
	}
	if string(content) != "abcdefghij" {
		t.Fatalf("artifact content = %q, want full output", string(content))
	}
}

func TestLocalExecutorSpoolsLargeStdoutAndStderr(t *testing.T) {
	root := t.TempDir()
	layout := newStateLayout(t, root)
	state := newExecutionStateForLayout(t, layout)
	executor := newExecutorWithHandler(policy.Config{}, approveShellHandler{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:         "yes stdout | head -c 1048576; yes stderr | head -c 1048576 >&2",
		MaxInlineOutput: 64,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded {
		t.Fatalf("expected successful command, got %+v", result)
	}
	if !result.Output.Truncated || result.Output.ArtifactPath == "" {
		t.Fatalf("expected spooled truncation, got %+v", result.Output)
	}
	if len(result.Stdout) != 64 || len(result.Stderr) != 64 || len(result.CombinedOutput) != 64 {
		t.Fatalf("inline lengths stdout=%d stderr=%d combined=%d, want all 64", len(result.Stdout), len(result.Stderr), len(result.CombinedOutput))
	}
	if result.Output.OriginalBytes < 2*1048576 {
		t.Fatalf("original bytes = %d, want large combined output", result.Output.OriginalBytes)
	}
	info, err := os.Stat(result.Output.ArtifactPath)
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	if info.Size() < 2*1048576 {
		t.Fatalf("artifact size = %d, want large combined output", info.Size())
	}
}

func TestLocalExecutorLargeOutputWithoutArtifactRootFailsBounded(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := newExecutor(policy.Config{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:         "printf 'abcdefghij'",
		MaxInlineOutput: 5,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusExecutionError || !strings.Contains(result.ExecutionError, "output artifact root") {
		t.Fatalf("result = %+v, want missing artifact root execution error", result)
	}
}

func TestLocalExecutorTruncationArtifactsDoNotCollide(t *testing.T) {
	root := t.TempDir()
	layout := newStateLayout(t, root)
	state := newExecutionStateForLayout(t, layout)
	executor := newExecutor(policy.Config{})

	first, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:         "printf 'abcdefghij'",
		MaxInlineOutput: 5,
	})
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	second, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:         "printf 'klmnopqrst'",
		MaxInlineOutput: 5,
	})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	if first.Output.ArtifactPath == "" || second.Output.ArtifactPath == "" {
		t.Fatalf("expected both executions to create artifacts: first=%+v second=%+v", first.Output, second.Output)
	}
	if first.Output.ArtifactPath == second.Output.ArtifactPath {
		t.Fatalf("expected distinct artifact paths, both were %q", first.Output.ArtifactPath)
	}
}

func TestLocalExecutorNonZeroExitStillReportsTruncation(t *testing.T) {
	root := t.TempDir()
	layout := newStateLayout(t, root)
	state := newExecutionStateForLayout(t, layout)
	executor := newExecutor(policy.Config{})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:         "printf 'abcdefghij'; exit 7",
		MaxInlineOutput: 5,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusExitedNonZero {
		t.Fatalf("expected exited-non-zero result, got %+v", result)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
	if !result.Output.Truncated {
		t.Fatalf("expected truncation to be reported for non-zero exit, got %+v", result.Output)
	}
	content, err := os.ReadFile(result.Output.ArtifactPath)
	if err != nil {
		t.Fatalf("ReadFile artifact: %v", err)
	}
	if string(content) != "abcdefghij" {
		t.Fatalf("artifact content = %q, want full output", string(content))
	}
}

func TestLocalExecutorLaunchFailureDoesNotCreateArtifact(t *testing.T) {
	root := t.TempDir()
	layout := newStateLayout(t, root)
	state := newExecutionStateForLayout(t, layout)
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{}, nil))
	executor := runtimeexec.NewLocalExecutorWithShell(evaluator, filepath.Join(root, "missing-shell"))

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:         "printf 'abcdefghij'",
		MaxInlineOutput: 5,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusFailedToStart {
		t.Fatalf("expected failed-to-start result, got %+v", result)
	}
	if result.Output.Truncated || result.Output.ArtifactPath != "" {
		t.Fatalf("launch failure should not create truncation artifact, got %+v", result.Output)
	}
	if _, err := os.Stat(layout.RecentArtifactRoot); !os.IsNotExist(err) {
		t.Fatalf("launch failure should not create artifact root, stat err=%v", err)
	}
}

func TestLocalExecutorPolicyDeniedCommandDoesNotCreateArtifact(t *testing.T) {
	root := t.TempDir()
	layout := newStateLayout(t, root)
	state := newExecutionStateForLayout(t, layout)
	executor := newExecutor(policy.Config{
		BlockedShell: []policy.ShellRule{{
			Command: "printf 'abcdefghij'",
			Match:   policy.MatchExact,
		}},
	})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:         "printf 'abcdefghij'",
		MaxInlineOutput: 5,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusBlocked || result.Executed {
		t.Fatalf("expected blocked result, got %+v", result)
	}
	if result.Output.Truncated || result.Output.ArtifactPath != "" {
		t.Fatalf("policy denial should not create truncation artifact, got %+v", result.Output)
	}
	if _, err := os.Stat(layout.RecentArtifactRoot); !os.IsNotExist(err) {
		t.Fatalf("policy denial should not create artifact root, stat err=%v", err)
	}
}

func newExecutionState(t *testing.T, root string) *runtimeexec.ExecutionState {
	t.Helper()

	state, err := runtimeexec.NewExecutionState(root)
	if err != nil {
		t.Fatalf("NewExecutionState: %v", err)
	}
	return state
}

func newExecutionStateForLayout(t *testing.T, layout statepkg.Layout) *runtimeexec.ExecutionState {
	t.Helper()

	state, err := runtimeexec.NewExecutionStateForLayout(layout)
	if err != nil {
		t.Fatalf("NewExecutionStateForLayout: %v", err)
	}
	return state
}

func newStateLayout(t *testing.T, workspaceRoot string) statepkg.Layout {
	t.Helper()

	layout, err := statepkg.ResolveLayout(workspaceRoot, t.TempDir())
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	return layout
}

func newExecutor(config policy.Config) *runtimeexec.LocalExecutor {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(config, nil))
	return runtimeexec.NewLocalExecutor(evaluator)
}

func newExecutorWithHandler(config policy.Config, handler policy.ApprovalHandler) *runtimeexec.LocalExecutor {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(config, handler))
	return runtimeexec.NewLocalExecutor(evaluator)
}

type approveShellHandler struct{}

func (approveShellHandler) ApproveFile(context.Context, policy.FileRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeDeny, nil
}

func (approveShellHandler) ApproveShell(context.Context, policy.ShellRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeOnce, nil
}

func (approveShellHandler) ApproveNetwork(context.Context, policy.NetworkRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeDeny, nil
}
