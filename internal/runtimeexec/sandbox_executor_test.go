package runtimeexec_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/internal/workspace"
	falkenruntime "github.com/smasonuk/falken-core/pkg/falken/runtime"
)

func TestSandboxExecutorExecutesAllowedCommand(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	sandboxRuntime := &fakeSandboxRuntime{
		result: falkenruntime.SandboxCommandResult{Started: true, ExitCode: 0},
		stdout: "hello",
	}
	executor := runtimeexec.NewSandboxExecutor(newRuntimePolicy(policy.Config{}), sandboxRuntime)

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "printf hello",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded || !result.Executed {
		t.Fatalf("expected successful sandbox result, got %+v", result)
	}
	if result.Stdout != "hello" || result.CombinedOutput != "hello" {
		t.Fatalf("unexpected output: %+v", result)
	}
	if sandboxRuntime.request.Command != "printf hello" {
		t.Fatalf("sandbox command = %q, want printf hello", sandboxRuntime.request.Command)
	}
	if sandboxRuntime.request.HostWorkingDir != state.CurrentWorkingDir() {
		t.Fatalf("sandbox cwd = %q, want %q", sandboxRuntime.request.HostWorkingDir, state.CurrentWorkingDir())
	}
}

func TestSandboxExecutorNonZeroExitIsStructured(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := runtimeexec.NewSandboxExecutor(newRuntimePolicy(policy.Config{}), &fakeSandboxRuntime{
		result: falkenruntime.SandboxCommandResult{
			Started:   true,
			ExitCode:  7,
			ExitError: "exit status 7",
		},
	})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "exit 7",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusExitedNonZero || !result.Executed {
		t.Fatalf("expected exited-non-zero sandbox result, got %+v", result)
	}
	if result.ExitCode != 7 || result.ExitError == "" {
		t.Fatalf("unexpected exit fields: %+v", result)
	}
}

func TestSandboxExecutorCarriesCleanupErrorWithoutChangingStatus(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := runtimeexec.NewSandboxExecutor(newRuntimePolicy(policy.Config{}), &fakeSandboxRuntime{
		result: falkenruntime.SandboxCommandResult{
			Started:      true,
			ExitCode:     7,
			ExitError:    "exit status 7",
			CleanupError: "cleanup container: remove failed",
		},
	})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "exit 7",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusExitedNonZero || result.ExecutionError != "" {
		t.Fatalf("result = %+v, want exited_non_zero without execution error", result)
	}
	if result.CleanupError != "cleanup container: remove failed" {
		t.Fatalf("cleanup error = %q, want sandbox cleanup diagnostic", result.CleanupError)
	}
}

func TestSandboxExecutorLaunchErrorIsStructured(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := runtimeexec.NewSandboxExecutor(newRuntimePolicy(policy.Config{}), &fakeSandboxRuntime{
		result: falkenruntime.SandboxCommandResult{StartError: "container runtime missing", ExitCode: -1},
	})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "printf hello",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusFailedToStart || result.Executed {
		t.Fatalf("expected failed-to-start sandbox result, got %+v", result)
	}
	if result.StartError == "" {
		t.Fatalf("expected start error to be recorded, got %+v", result)
	}
}

func TestSandboxExecutorRuntimeErrorIsStructured(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := runtimeexec.NewSandboxExecutor(newRuntimePolicy(policy.Config{}), &fakeSandboxRuntime{
		err: errors.New("sandbox is not started"),
	})

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "printf hello",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusExecutionError {
		t.Fatalf("expected execution error result, got %+v", result)
	}
	if result.ExecutionError == "" {
		t.Fatalf("expected execution error to be recorded, got %+v", result)
	}
}

func TestSandboxExecutorWorkingDirectoryOverrideIsHonored(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	state := newExecutionState(t, root)
	sandboxRuntime := &fakeSandboxRuntime{
		result: falkenruntime.SandboxCommandResult{Started: true, ExitCode: 0},
		stdout: "ok",
	}
	executor := runtimeexec.NewSandboxExecutor(newRuntimePolicy(policy.Config{}), sandboxRuntime)

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:    "pwd",
		WorkingDir: "subdir",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := realPath(t, subdir)
	if result.WorkingDir != want {
		t.Fatalf("result cwd = %q, want %q", result.WorkingDir, want)
	}
	if sandboxRuntime.request.HostWorkingDir != want {
		t.Fatalf("sandbox cwd = %q, want %q", sandboxRuntime.request.HostWorkingDir, want)
	}
}

func TestSandboxExecutorRejectsOutsideWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := runtimeexec.NewSandboxExecutor(newRuntimePolicy(policy.Config{}), &fakeSandboxRuntime{})

	_, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command:    "pwd",
		WorkingDir: "../..",
	})
	if err == nil || !strings.Contains(err.Error(), workspace.ErrPathOutsideWorkspace.Error()) {
		t.Fatalf("expected outside workspace cwd error, got %v", err)
	}
}

func TestSandboxExecutorEnvironmentOverridesArePassed(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FALKEN_HOST_SECRET", "do-not-leak")
	state := newExecutionState(t, root)
	state.SetEnv("FALKEN_SHARED", "session")
	sandboxRuntime := &fakeSandboxRuntime{
		result: falkenruntime.SandboxCommandResult{Started: true, ExitCode: 0},
	}
	executor := runtimeexec.NewSandboxExecutor(newRuntimePolicy(policy.Config{}), sandboxRuntime)

	_, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "env",
		Env: map[string]string{
			"FALKEN_SHARED": "request",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	env := envMap(sandboxRuntime.request.Env)
	if env["FALKEN_SHARED"] != "request" {
		t.Fatalf("sandbox env FALKEN_SHARED = %q, want request", env["FALKEN_SHARED"])
	}
	if os.Getenv("FALKEN_SHARED") == "request" {
		t.Fatal("sandbox execution mutated host process environment")
	}
	if _, ok := env["FALKEN_HOST_SECRET"]; ok {
		t.Fatalf("sandbox env leaked host secret: %+v", env)
	}
	if env["PATH"] == "" || env["HOME"] == "" {
		t.Fatalf("sandbox env missing minimal PATH/HOME: %+v", env)
	}
}

func TestSandboxExecutorPolicyDeniedBeforeRuntimeExecution(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	sandboxRuntime := &fakeSandboxRuntime{}
	executor := runtimeexec.NewSandboxExecutor(newRuntimePolicy(policy.Config{
		BlockedShell: []policy.ShellRule{{
			Command: "printf hello",
			Match:   policy.MatchExact,
		}},
	}), sandboxRuntime)

	result, err := executor.Execute(context.Background(), state, runtimeexec.CommandRequest{
		Command: "printf hello",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusBlocked || result.Executed {
		t.Fatalf("expected blocked result, got %+v", result)
	}
	if sandboxRuntime.called {
		t.Fatal("policy-denied command should not reach sandbox runtime")
	}
	if result.Policy.Outcome != runtimepolicy.ShellOutcomeDenied {
		t.Fatalf("expected denied policy outcome, got %+v", result.Policy)
	}
}

type fakeSandboxRuntime struct {
	called  bool
	request falkenruntime.SandboxCommandRequest
	result  falkenruntime.SandboxCommandResult
	stdout  string
	stderr  string
	err     error
}

func (r *fakeSandboxRuntime) Start(context.Context) error {
	return nil
}

func (r *fakeSandboxRuntime) Execute(_ context.Context, request falkenruntime.SandboxCommandRequest) (falkenruntime.SandboxCommandResult, error) {
	r.called = true
	r.request = request
	if r.stdout != "" {
		_, _ = io.WriteString(request.Stdout, r.stdout)
	}
	if r.stderr != "" {
		_, _ = io.WriteString(request.Stderr, r.stderr)
	}
	return r.result, r.err
}

func (r *fakeSandboxRuntime) Close(context.Context) error {
	return nil
}

func newRuntimePolicy(config policy.Config) *runtimepolicy.Evaluator {
	return runtimepolicy.NewEvaluator(policy.NewEngine(config, nil))
}
