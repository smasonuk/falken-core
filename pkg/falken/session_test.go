package falken

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/store"
	falkenruntime "github.com/smasonuk/falken-core/pkg/falken/runtime"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

func TestSessionStartIsIdempotent(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}

	first, found, err := state.ReadMetadata(session.layout)
	if err != nil {
		t.Fatalf("ReadMetadata after first Start: %v", err)
	}
	if !found {
		t.Fatal("expected metadata after first Start")
	}

	time.Sleep(20 * time.Millisecond)

	if err := session.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	second, found, err := state.ReadMetadata(session.layout)
	if err != nil {
		t.Fatalf("ReadMetadata after second Start: %v", err)
	}
	if !found {
		t.Fatal("expected metadata after second Start")
	}
	if second != first {
		t.Fatalf("metadata changed on idempotent Start: first=%+v second=%+v", first, second)
	}

	for _, path := range []string{
		session.Paths.StateRoot,
		session.Paths.BackupRoot,
		session.Paths.CurrentConversationRoot,
		session.Paths.RecentTruncationRoot,
		session.Paths.RecentArtifactRoot,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %q to exist after Start: %v", path, err)
		}
	}
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestSessionCloseBeforeStartIsSafe(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Close(); err != nil {
		t.Fatalf("first Close before Start: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close before Start: %v", err)
	}

	if err := session.Start(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Start after Close error = %v, want ErrSessionClosed", err)
	}
	_, err := session.Run(context.Background(), RunRequest{Prompt: "closed"})
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Run after Close error = %v, want ErrSessionClosed", err)
	}
}

func TestSessionRequiresRuntimeProviderForDefaultSandboxMode(t *testing.T) {
	setStateHomeEnv(t)

	session, err := New(Config{
		WorkspaceDir: tempWorkspace(t),
		LLM:          noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "runtime provider is required for sandbox execution") {
		t.Fatalf("Start error = %v, want runtime provider config error", err)
	}
}

func TestSessionAllowsLocalModeWithoutRuntimeProvider(t *testing.T) {
	setStateHomeEnv(t)

	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func TestSessionUsesConfiguredRuntimeProviderForSandboxMode(t *testing.T) {
	setStateHomeEnv(t)

	provider := &countingRuntimeProvider{}
	session, err := New(Config{
		WorkspaceDir: tempWorkspace(t),
		Runtime:      provider,
		LLM:          noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestAdapterSessionRuntimeUsesSuppliedWorkspaceFilesWithoutLocalWorkspace(t *testing.T) {
	setStateHomeEnv(t)

	workspaceRoot := filepath.Join(t.TempDir(), "missing-workspace")
	layout, err := state.ResolveLayout(workspaceRoot, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	ops := stubWorkspaceOperations{}
	runtime, err := newAdapterSessionRuntime(context.Background(), layout, SessionConfig{}, runtimeAdaptersProvider{
		adapters: RuntimeAdapters{
			SandboxRuntime: &countingSandboxRuntime{},
			WorkspaceFiles: ops,
		},
	})
	if err != nil {
		t.Fatalf("newAdapterSessionRuntime: %v", err)
	}
	wrapped, ok := runtime.fileOperations.(*policyCheckingWorkspaceOperations)
	if !ok {
		t.Fatalf("fileOperations = %T, want policy-checking wrapper", runtime.fileOperations)
	}
	if wrapped.delegate != ops {
		t.Fatalf("wrapped delegate = %T, want supplied workspace operations", wrapped.delegate)
	}
	if runtime.localFileOps {
		t.Fatal("localFileOps = true, want false for supplied workspace operations")
	}
	if !runtime.executionState.VirtualWorkspace() {
		t.Fatal("execution state is not virtual for supplied workspace operations")
	}
}

func TestAdapterSessionRuntimeCreatesLocalWorkspaceFilesWhenProviderOmitsThem(t *testing.T) {
	setStateHomeEnv(t)

	workspaceRoot := tempWorkspace(t)
	layout, err := state.ResolveLayout(workspaceRoot, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	runtime, err := newAdapterSessionRuntime(context.Background(), layout, SessionConfig{}, runtimeAdaptersProvider{
		adapters: RuntimeAdapters{SandboxRuntime: &countingSandboxRuntime{}},
	})
	if err != nil {
		t.Fatalf("newAdapterSessionRuntime: %v", err)
	}
	if _, ok := runtime.fileOperations.(*runtimefiles.Operations); !ok {
		t.Fatalf("fileOperations = %T, want local runtimefiles operations", runtime.fileOperations)
	}
	if !runtime.localFileOps {
		t.Fatal("localFileOps = false, want true for provider without workspace operations")
	}
	if runtime.executionState.VirtualWorkspace() {
		t.Fatal("execution state is virtual, want local statting state")
	}
}

func TestResetConversationStatePreservesRemoteWorkspaceFiles(t *testing.T) {
	setStateHomeEnv(t)

	ops := stubWorkspaceOperations{}
	session, err := New(Config{
		WorkspaceDir: filepath.Join(t.TempDir(), "missing-workspace"),
		StateDir:     filepath.Join(t.TempDir(), "state"),
		Runtime: runtimeAdaptersProvider{adapters: RuntimeAdapters{
			SandboxRuntime: &countingSandboxRuntime{},
			WorkspaceFiles: ops,
		}},
		LLM: noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	assertPolicyWrappedWorkspaceOperations(t, session.resources.runtime.fileOperations, ops, "before reset")
	if err := session.ResetConversationState(); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}
	assertPolicyWrappedWorkspaceOperations(t, session.resources.runtime.fileOperations, ops, "after reset")
}

func TestRemoteWorkspaceCommandWorkingDirIsResolvedLexically(t *testing.T) {
	setStateHomeEnv(t)

	sandbox := &countingSandboxRuntime{}
	virtualWorkspace := filepath.Join(t.TempDir(), "missing-workspace")
	session, err := New(Config{
		WorkspaceDir: virtualWorkspace,
		StateDir:     filepath.Join(t.TempDir(), "state"),
		Runtime: runtimeAdaptersProvider{adapters: RuntimeAdapters{
			SandboxRuntime: sandbox,
			WorkspaceFiles: stubWorkspaceOperations{},
		}},
		LLM: noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.executeCommand(context.Background(), runtimeexec.CommandRequest{
		Command:    "pwd",
		WorkingDir: "nested",
	})
	if err != nil {
		t.Fatalf("executeCommand: %v", err)
	}
	wantWorkingDir := filepath.Join(filepath.Clean(virtualWorkspace), "nested")
	if sandbox.lastRequest.HostWorkingDir != wantWorkingDir {
		t.Fatalf("sandbox host working dir = %q, want %q", sandbox.lastRequest.HostWorkingDir, wantWorkingDir)
	}
	if result.ToolWorkingDir != "nested" {
		t.Fatalf("tool working dir = %q, want nested", result.ToolWorkingDir)
	}

	if _, err := session.executeCommand(context.Background(), runtimeexec.CommandRequest{
		Command:    "pwd",
		WorkingDir: "../outside",
	}); err == nil {
		t.Fatal("expected escaping working directory to fail")
	}
	if sandbox.executes != 1 {
		t.Fatalf("sandbox executes = %d, want only the first command to run", sandbox.executes)
	}
}

func TestRemoteWorkspaceModeRejectsBackgroundProcesses(t *testing.T) {
	setStateHomeEnv(t)

	session, err := New(Config{
		WorkspaceDir: filepath.Join(t.TempDir(), "missing-workspace"),
		StateDir:     filepath.Join(t.TempDir(), "state"),
		Runtime: runtimeAdaptersProvider{adapters: RuntimeAdapters{
			SandboxRuntime: &countingSandboxRuntime{},
			WorkspaceFiles: stubWorkspaceOperations{},
		}},
		LLM: noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()

	result, err := session.startBackgroundProcess(context.Background(), runtimeexec.BackgroundStartRequest{Command: "sleep 60"})
	if !errors.Is(err, ErrBackgroundProcessUnavailableInRemoteMode) {
		t.Fatalf("startBackgroundProcess error = %v, want remote-mode background rejection", err)
	}
	if result.Status != runtimeexec.BackgroundStartFailedToStart || result.StartError == "" {
		t.Fatalf("result = %+v, want explicit failed_to_start", result)
	}
}

func TestSessionStartContextReachesRuntimeProvider(t *testing.T) {
	setStateHomeEnv(t)

	ctx, cancel := context.WithCancel(context.Background())
	provider := &countingRuntimeProvider{
		useContextErr: true,
		cancel:        cancel,
	}
	session, err := New(Config{
		WorkspaceDir: tempWorkspace(t),
		Runtime:      provider,
		LLM:          noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.StartContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartContext error = %v, want context.Canceled", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestSessionExecutionConfigShellPathWiresLocalExecutorAndBackgroundManager(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	marker := filepath.Join(workspace, "shell-args.txt")
	shellPath := filepath.Join(workspace, "fake-shell")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + shellQuote(marker) + "\nexit 0\n"
	if err := os.WriteFile(shellPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}
	session, err := New(Config{
		WorkspaceDir: workspace,
		ExecutionDetails: ExecutionConfig{
			Mode:      ExecutionModeLocal,
			ShellPath: shellPath,
		},
		LLM: noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	command, err := session.executeCommand(context.Background(), runtimeexec.CommandRequest{Command: "printf command"})
	if err != nil {
		t.Fatalf("executeCommand: %v", err)
	}
	if command.Status != runtimeexec.CommandStatusSucceeded || !command.Executed {
		t.Fatalf("command result = %+v, want fake shell success", command)
	}
	background, err := session.startBackgroundProcess(context.Background(), runtimeexec.BackgroundStartRequest{Command: "printf background"})
	if err != nil {
		t.Fatalf("startBackgroundProcess: %v", err)
	}
	if background.Status != runtimeexec.BackgroundStartStarted {
		t.Fatalf("background result = %+v, want started", background)
	}
	waitForBackgroundStatus(t, session.resources.runtime.backgroundManager, background.ProcessID, runtimeexec.BackgroundProcessExited)

	got := readFileInTest(t, marker)
	if !strings.Contains(got, "printf command") || !strings.Contains(got, "printf background") {
		t.Fatalf("fake shell marker = %q, want command and background invocations", got)
	}
}

func TestSessionClosesRuntimeAdaptersWhenProviderReturnsInvalidSandboxAdapters(t *testing.T) {
	setStateHomeEnv(t)

	policy := &countingNetworkPolicy{}
	proxy := &countingNetworkProxy{}
	provider := runtimeAdaptersProvider{adapters: RuntimeAdapters{
		NetworkPolicy: policy,
		NetworkProxy:  proxy,
	}}
	session, err := New(Config{
		WorkspaceDir: tempWorkspace(t),
		Runtime:      provider,
		LLM:          noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "nil sandbox runtime") {
		t.Fatalf("Start error = %v, want invalid sandbox adapter error", err)
	}
	if policy.closes != 1 {
		t.Fatalf("network policy closes = %d, want 1", policy.closes)
	}
	if proxy.closes != 1 {
		t.Fatalf("network proxy closes = %d, want 1", proxy.closes)
	}
}

func TestSessionReportsRuntimeAdapterCleanupError(t *testing.T) {
	setStateHomeEnv(t)

	policy := &countingNetworkPolicy{closeErr: errors.New("close policy failed")}
	provider := runtimeAdaptersProvider{adapters: RuntimeAdapters{NetworkPolicy: policy}}
	session, err := New(Config{
		WorkspaceDir: tempWorkspace(t),
		Runtime:      provider,
		LLM:          noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "cleanup runtime adapters") || !strings.Contains(err.Error(), "close policy failed") {
		t.Fatalf("Start error = %v, want original and cleanup errors", err)
	}
}

func TestSessionStartInitializesRuntimeOwnershipOnce(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	sandboxRuntime := &countingSandboxRuntime{}
	factoryCalls := 0
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, _ SessionConfig) (*sessionRuntime, error) {
		factoryCalls++
		return newTestSessionRuntime(t, layout, sandboxRuntime), nil
	}

	if err := session.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	if factoryCalls != 1 {
		t.Fatalf("runtime factory calls = %d, want 1", factoryCalls)
	}
	if sandboxRuntime.starts != 1 {
		t.Fatalf("sandbox starts = %d, want 1", sandboxRuntime.starts)
	}
	if session.resources.runtime == nil {
		t.Fatal("expected session runtime to be owned after Start")
	}
	if session.resources.runtime.executionState == nil {
		t.Fatal("expected execution state to be owned by session runtime")
	}
	if session.resources.runtime.commandExecutor == nil {
		t.Fatal("expected command executor to be owned by session runtime")
	}
	if session.resources.runtime.backgroundManager == nil {
		t.Fatal("expected background manager to be owned by session runtime")
	}
	if session.resources.runtime.sandboxRuntime != sandboxRuntime {
		t.Fatal("expected sandbox runtime to be owned by session runtime")
	}
}

func TestSessionStartFailureCleansRuntimeResources(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	failingSandbox := &countingSandboxRuntime{startErr: errors.New("sandbox boom")}
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, _ SessionConfig) (*sessionRuntime, error) {
		return newTestSessionRuntime(t, layout, failingSandbox), nil
	}

	if err := session.Start(); err == nil || !strings.Contains(err.Error(), "start session runtime") {
		t.Fatalf("Start error = %v, want runtime start failure", err)
	}
	if failingSandbox.starts != 1 {
		t.Fatalf("sandbox starts = %d, want 1", failingSandbox.starts)
	}
	if failingSandbox.closes != 1 {
		t.Fatalf("sandbox closes after failed start = %d, want 1", failingSandbox.closes)
	}
	_, err := session.Run(context.Background(), RunRequest{Prompt: "not-started"})
	if !errors.Is(err, ErrSessionNotStarted) {
		t.Fatalf("Run after failed Start error = %v, want ErrSessionNotStarted", err)
	}

	successSandbox := &countingSandboxRuntime{}
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, _ SessionConfig) (*sessionRuntime, error) {
		return newTestSessionRuntime(t, layout, successSandbox), nil
	}
	if err := session.Start(); err != nil {
		t.Fatalf("retry Start after cleaned failure: %v", err)
	}
	if successSandbox.starts != 1 {
		t.Fatalf("success sandbox starts = %d, want 1", successSandbox.starts)
	}
}

func TestSessionStartAgentFailureJoinsRuntimeCleanupErrors(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	agentErr := errors.New("agent boom")
	runtimeCloseErr := errors.New("runtime close boom")
	sandboxRuntime := &countingSandboxRuntime{closeErr: runtimeCloseErr}
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, _ SessionConfig) (*sessionRuntime, error) {
		return newTestSessionRuntime(t, layout, sandboxRuntime), nil
	}
	session.resources.agentFactory = func(SessionConfig, conversationStores, *sessionToolHub, *agent.ModeState, *sessionRuntime) (*sessionAgentRunner, error) {
		return nil, agentErr
	}

	err := session.Start()
	if !errors.Is(err, agentErr) || !errors.Is(err, runtimeCloseErr) {
		t.Fatalf("Start error = %v, want agent and runtime cleanup errors", err)
	}
	if !strings.Contains(err.Error(), "initialize agent runtime") ||
		!strings.Contains(err.Error(), "runtime cleanup failed") {
		t.Fatalf("Start error = %v, want startup and cleanup context", err)
	}
}

func TestSessionCloseTwiceAfterFailedStartIsSafe(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	sandboxRuntime := &countingSandboxRuntime{startErr: errors.New("sandbox boom")}
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, _ SessionConfig) (*sessionRuntime, error) {
		return newTestSessionRuntime(t, layout, sandboxRuntime), nil
	}

	if err := session.Start(); err == nil {
		t.Fatal("Start succeeded, want failure")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("first Close after failed Start: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close after failed Start: %v", err)
	}
	if sandboxRuntime.closes != 1 {
		t.Fatalf("sandbox closes = %d, want only cleanup close from failed Start", sandboxRuntime.closes)
	}
}

func TestSessionCloseShutsDownRuntimeOwnership(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	sandboxRuntime := &countingSandboxRuntime{}
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, _ SessionConfig) (*sessionRuntime, error) {
		return newTestSessionRuntime(t, layout, sandboxRuntime), nil
	}

	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if sandboxRuntime.closes != 1 {
		t.Fatalf("sandbox closes = %d, want 1", sandboxRuntime.closes)
	}
	if session.resources.runtime != nil {
		t.Fatal("expected session runtime ownership to be released after Close")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if sandboxRuntime.closes != 1 {
		t.Fatalf("sandbox closes after idempotent Close = %d, want 1", sandboxRuntime.closes)
	}
}

func TestSessionCloseStopsBackgroundProcesses(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ownedRuntime := session.resources.runtime
	result, err := session.startBackgroundProcess(context.Background(), runtimeexec.BackgroundStartRequest{
		Command: "sleep 30",
	})
	if err != nil {
		t.Fatalf("startBackgroundProcess: %v", err)
	}
	if result.Status != runtimeexec.BackgroundStartStarted {
		t.Fatalf("background start status = %q, want %q; result=%+v", result.Status, runtimeexec.BackgroundStartStarted, result)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	snapshot, found := ownedRuntime.backgroundManager.Get(result.ProcessID)
	if !found {
		t.Fatalf("expected background process %q to remain inspectable after cleanup", result.ProcessID)
	}
	if snapshot.Status != runtimeexec.BackgroundProcessStopped {
		t.Fatalf("background status after Close = %q, want %q", snapshot.Status, runtimeexec.BackgroundProcessStopped)
	}
}

func TestSessionCloseHandlesAlreadyExitedBackgroundProcesses(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ownedRuntime := session.resources.runtime
	result, err := session.startBackgroundProcess(context.Background(), runtimeexec.BackgroundStartRequest{
		Command: "printf done",
	})
	if err != nil {
		t.Fatalf("startBackgroundProcess: %v", err)
	}
	if result.Status != runtimeexec.BackgroundStartStarted {
		t.Fatalf("background start status = %q, want %q; result=%+v", result.Status, runtimeexec.BackgroundStartStarted, result)
	}

	waitForBackgroundStatus(t, ownedRuntime.backgroundManager, result.ProcessID, runtimeexec.BackgroundProcessExited)

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	snapshot, found := ownedRuntime.backgroundManager.Get(result.ProcessID)
	if !found {
		t.Fatalf("expected background process %q to remain inspectable after Close", result.ProcessID)
	}
	if snapshot.Status != runtimeexec.BackgroundProcessExited {
		t.Fatalf("background status after Close = %q, want %q", snapshot.Status, runtimeexec.BackgroundProcessExited)
	}
}

func TestSessionRuntimeIntegrationPaths(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, _ SessionConfig) (*sessionRuntime, error) {
		return newTestSessionRuntime(t, layout, &countingSandboxRuntime{}), nil
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	commandResult, err := session.executeCommand(context.Background(), runtimeexec.CommandRequest{
		Command: "printf hello",
	})
	if err != nil {
		t.Fatalf("executeCommand: %v", err)
	}
	if commandResult.Status != runtimeexec.CommandStatusSucceeded {
		t.Fatalf("command status = %q, want %q; result=%+v", commandResult.Status, runtimeexec.CommandStatusSucceeded, commandResult)
	}
	if commandResult.Stdout != "hello" {
		t.Fatalf("command stdout = %q, want %q", commandResult.Stdout, "hello")
	}

	backgroundResult, err := session.startBackgroundProcess(context.Background(), runtimeexec.BackgroundStartRequest{
		Command: "printf background",
	})
	if err != nil {
		t.Fatalf("startBackgroundProcess: %v", err)
	}
	if backgroundResult.Status != runtimeexec.BackgroundStartStarted {
		t.Fatalf("background start status = %q, want %q; result=%+v", backgroundResult.Status, runtimeexec.BackgroundStartStarted, backgroundResult)
	}
	waitForBackgroundStatus(t, session.resources.runtime.backgroundManager, backgroundResult.ProcessID, runtimeexec.BackgroundProcessExited)
	logs, err := session.readBackgroundLogs(backgroundResult.ProcessID)
	if err != nil {
		t.Fatalf("readBackgroundLogs: %v", err)
	}
	if logs.CombinedOutput != "background" {
		t.Fatalf("background output = %q, want %q", logs.CombinedOutput, "background")
	}

}

func TestSessionRuntimeStartsAndClosesNetworkProxy(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	runtime := newTestSessionRuntime(t, session.layout, nil)
	proxy := &countingNetworkProxy{}
	runtime.networkProxy = proxy

	if err := runtime.start(context.Background()); err != nil {
		t.Fatalf("runtime.start: %v", err)
	}
	if proxy.starts != 1 {
		t.Fatalf("proxy starts = %d, want 1", proxy.starts)
	}
	if err := runtime.close(context.Background()); err != nil {
		t.Fatalf("runtime.close: %v", err)
	}
	if proxy.closes != 1 {
		t.Fatalf("proxy closes = %d, want 1", proxy.closes)
	}
}

func TestSessionRuntimeInjectsNetworkProxyEndpointIntoSandbox(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	sandboxRuntime := &countingSandboxRuntime{}
	runtime := newTestSessionRuntime(t, session.layout, sandboxRuntime)
	runtime.networkProxy = &countingNetworkProxy{
		endpoint: ProxyEndpoint{
			HTTPProxyURL:  "http://127.0.0.1:8181",
			HTTPSProxyURL: "http://127.0.0.1:8181",
			NoProxy:       "localhost,127.0.0.1",
		},
	}

	if err := runtime.start(context.Background()); err != nil {
		t.Fatalf("runtime.start: %v", err)
	}
	if sandboxRuntime.proxyHTTP != "http://127.0.0.1:8181" ||
		sandboxRuntime.proxyHTTPS != "http://127.0.0.1:8181" ||
		sandboxRuntime.proxyNoProxy != "localhost,127.0.0.1" {
		t.Fatalf("sandbox proxy = {%q %q %q}, want endpoint injected", sandboxRuntime.proxyHTTP, sandboxRuntime.proxyHTTPS, sandboxRuntime.proxyNoProxy)
	}
}

func TestSandboxCommandExecutionThroughSession(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	sandboxRuntime := &countingSandboxRuntime{stdout: "sandboxed"}
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, config SessionConfig) (*sessionRuntime, error) {
		return newSandboxTestSessionRuntime(t, layout, config, sandboxRuntime), nil
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.executeCommand(context.Background(), runtimeexec.CommandRequest{Command: "printf sandboxed"})
	if err != nil {
		t.Fatalf("executeCommand: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded || result.Stdout != "sandboxed" {
		t.Fatalf("result = %+v, want sandboxed success", result)
	}
	if sandboxRuntime.executes != 1 {
		t.Fatalf("sandbox executes = %d, want 1", sandboxRuntime.executes)
	}
}

func TestSandboxStartupFailureFailsStartClearly(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	sandboxRuntime := &countingSandboxRuntime{startErr: errors.New("sandbox unavailable")}
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, config SessionConfig) (*sessionRuntime, error) {
		return newSandboxTestSessionRuntime(t, layout, config, sandboxRuntime), nil
	}

	if err := session.Start(); err == nil || !strings.Contains(err.Error(), "start sandbox runtime") {
		t.Fatalf("Start error = %v, want sandbox startup failure", err)
	}
}

func TestLocalExecutorCanStillBeInjectedForTests(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, _ SessionConfig) (*sessionRuntime, error) {
		return newTestSessionRuntime(t, layout, &countingSandboxRuntime{}), nil
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.executeCommand(context.Background(), runtimeexec.CommandRequest{Command: "printf local"})
	if err != nil {
		t.Fatalf("executeCommand: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusSucceeded || result.Stdout != "local" {
		t.Fatalf("result = %+v, want local executor output", result)
	}
}

func TestPolicyDeniedCommandDoesNotReachSandbox(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	session.config.Policy.StrictCommandAllowlist = true
	sandboxRuntime := &countingSandboxRuntime{stdout: "should not run"}
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, config SessionConfig) (*sessionRuntime, error) {
		return newSandboxTestSessionRuntime(t, layout, config, sandboxRuntime), nil
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.executeCommand(context.Background(), runtimeexec.CommandRequest{Command: "printf denied"})
	if err != nil {
		t.Fatalf("executeCommand: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusBlocked || result.Executed {
		t.Fatalf("result = %+v, want blocked", result)
	}
	if sandboxRuntime.executes != 0 {
		t.Fatalf("sandbox executes = %d, want 0", sandboxRuntime.executes)
	}
}

func TestSessionOwnsConversationStores(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if path := optionalStorePath(session.stores.history); path != session.Paths.HistoryPath {
		t.Fatalf("history store path = %q, want %q", path, session.Paths.HistoryPath)
	}
	if path := optionalStorePath(session.stores.memory); path != session.Paths.MemoryPath {
		t.Fatalf("memory store path = %q, want %q", path, session.Paths.MemoryPath)
	}
	if path := optionalStorePath(session.stores.todos); path != session.Paths.TodosPath {
		t.Fatalf("todo store path = %q, want %q", path, session.Paths.TodosPath)
	}
	if path := optionalStorePath(session.stores.plan); path != session.Paths.PlanPath {
		t.Fatalf("plan store path = %q, want %q", path, session.Paths.PlanPath)
	}
	if path := optionalStorePath(session.stores.commandEvidence); path != session.Paths.CommandEvidencePath {
		t.Fatalf("command evidence store path = %q, want %q", path, session.Paths.CommandEvidencePath)
	}

	history, err := session.stores.history.Read()
	if err != nil {
		t.Fatalf("history.Read: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history length = %d, want 0", len(history))
	}

	memory, err := session.stores.memory.Read()
	if err != nil {
		t.Fatalf("memory.Read: %v", err)
	}
	if len(memory.Entries) != 0 {
		t.Fatalf("memory entries length = %d, want 0", len(memory.Entries))
	}

	todos, err := session.stores.todos.Read()
	if err != nil {
		t.Fatalf("todos.Read: %v", err)
	}
	if len(todos.Items) != 0 {
		t.Fatalf("todo items length = %d, want 0", len(todos.Items))
	}

	plan, err := session.stores.plan.Read()
	if err != nil {
		t.Fatalf("plan.Read: %v", err)
	}
	if plan != "" {
		t.Fatalf("plan = %q, want empty string", plan)
	}
}

func optionalStorePath(backend any) string {
	if provider, ok := backend.(store.OptionalPathProvider); ok {
		return provider.Path()
	}
	return ""
}

func TestSessionCloseWhileRunActiveIsRejected(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	blocker := newBlockingRunner()
	session.resources.runner = blocker

	done := make(chan error, 1)
	go func() {
		_, err := session.Run(context.Background(), RunRequest{Prompt: "first"})
		done <- err
	}()

	<-blocker.started

	if err := session.Close(); !errors.Is(err, ErrTopLevelRunActive) {
		t.Fatalf("expected ErrTopLevelRunActive, got %v", err)
	}

	close(blocker.release)
	if err := <-done; err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close after run completion: %v", err)
	}
}

func TestSessionStartAfterCloseIsRejected(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := session.Start(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("expected ErrSessionClosed, got %v", err)
	}
}

func TestSessionRunAfterCloseIsRejected(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := session.Run(context.Background(), RunRequest{Prompt: "test"})
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("expected ErrSessionClosed, got %v", err)
	}
}

func TestSessionRunBeforeStartIsRejected(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	_, err := session.Run(context.Background(), RunRequest{Prompt: "test"})
	if !errors.Is(err, ErrSessionNotStarted) {
		t.Fatalf("expected ErrSessionNotStarted, got %v", err)
	}
}

func TestSessionRunConcurrentRejected(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	blocker := newBlockingRunner()
	session.resources.runner = blocker

	done := make(chan error, 1)
	go func() {
		_, err := session.Run(context.Background(), RunRequest{Prompt: "first"})
		done <- err
	}()

	<-blocker.started

	_, err := session.Run(context.Background(), RunRequest{Prompt: "second"})
	if !errors.Is(err, ErrTopLevelRunActive) {
		t.Fatalf("expected ErrTopLevelRunActive, got %v", err)
	}

	close(blocker.release)
	if err := <-done; err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
}

func TestSessionRunGuardClearsAfterRunnerError(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	session.resources.runner = runnerFunc(func(context.Context, RunRequest) (RunResult, error) {
		return RunResult{Error: "boom"}, errors.New("boom")
	})

	_, err := session.Run(context.Background(), RunRequest{Prompt: "first"})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected runner error boom, got %v", err)
	}

	session.resources.runner = runnerFunc(func(context.Context, RunRequest) (RunResult, error) {
		return RunResult{Completed: true}, nil
	})

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "second"}); err != nil {
		t.Fatalf("second Run after error: %v", err)
	}
}

func TestSessionLifecycleStartRunClose(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "hello"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSessionResetConversationStateStillUsable(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := session.stores.history.Write([]string{"history"}); err != nil {
		t.Fatalf("history.Write before reset: %v", err)
	}
	if err := session.stores.memory.Write(store.MemoryState{Entries: []string{"memory"}}); err != nil {
		t.Fatalf("memory.Write before reset: %v", err)
	}
	if err := session.stores.todos.Write(store.TodoState{Items: []store.TodoItem{{Text: "todo", Done: false}}}); err != nil {
		t.Fatalf("todos.Write before reset: %v", err)
	}
	if err := session.stores.plan.Write("plan"); err != nil {
		t.Fatalf("plan.Write before reset: %v", err)
	}

	metadataBefore, found, err := state.ReadMetadata(session.layout)
	if err != nil {
		t.Fatalf("ReadMetadata before reset: %v", err)
	}
	if !found {
		t.Fatal("expected metadata before reset")
	}

	if err := session.ResetConversationState(); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}

	history, err := session.stores.history.Read()
	if err != nil {
		t.Fatalf("history.Read after reset: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history after reset = %#v, want empty", history)
	}

	memory, err := session.stores.memory.Read()
	if err != nil {
		t.Fatalf("memory.Read after reset: %v", err)
	}
	if len(memory.Entries) != 0 {
		t.Fatalf("memory after reset = %#v, want empty", memory)
	}

	todos, err := session.stores.todos.Read()
	if err != nil {
		t.Fatalf("todos.Read after reset: %v", err)
	}
	if len(todos.Items) != 0 {
		t.Fatalf("todos after reset = %#v, want empty", todos)
	}

	plan, err := session.stores.plan.Read()
	if err != nil {
		t.Fatalf("plan.Read after reset: %v", err)
	}
	if plan != "" {
		t.Fatalf("plan after reset = %q, want empty string", plan)
	}

	metadataAfter, found, err := state.ReadMetadata(session.layout)
	if err != nil {
		t.Fatalf("ReadMetadata after reset: %v", err)
	}
	if !found {
		t.Fatal("expected metadata after reset")
	}
	if metadataAfter != metadataBefore {
		t.Fatalf("metadata changed across reset: before=%+v after=%+v", metadataBefore, metadataAfter)
	}

	newHistory, err := json.Marshal(agent.UserMessage("new-history"))
	if err != nil {
		t.Fatalf("Marshal new history: %v", err)
	}
	if err := session.stores.history.Append(string(newHistory)); err != nil {
		t.Fatalf("history.Append after reset: %v", err)
	}
	history, err = session.stores.history.Read()
	if err != nil {
		t.Fatalf("history.Read after append: %v", err)
	}
	if !reflect.DeepEqual(history, []string{string(newHistory)}) {
		t.Fatalf("history after append = %#v, want %#v", history, []string{string(newHistory)})
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "hello"}); err != nil {
		t.Fatalf("Run after reset: %v", err)
	}
}

func TestSessionResetConversationStateClearsInMemoryModeAndReadTokens(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	path := filepath.Join(workspace, "notes.txt")
	if err := os.WriteFile(path, []byte("notes"), 0o600); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	session, err := newSessionWithConfig(workspace, "", SessionConfig{
		Execution: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:       noopLLM{},
	})
	if err != nil {
		t.Fatalf("NewSessionWithConfig: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}
	read, err := session.resources.runtime.fileOperations.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "notes.txt"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !read.HasToken {
		t.Fatal("expected managed read token before reset")
	}

	if err := session.ResetConversationState(); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}

	mode, err := session.CurrentMode()
	if err != nil {
		t.Fatalf("CurrentMode: %v", err)
	}
	if mode != ModeDefault {
		t.Fatalf("mode after reset = %q, want default", mode)
	}
	managedOps, ok := session.resources.runtime.fileOperations.(*runtimefiles.Operations)
	if !ok {
		t.Fatalf("fileOperations = %T, want *runtimefiles.Operations", session.resources.runtime.fileOperations)
	}
	if _, found := managedOps.ManagedService().Tokens().Lookup(read.ResolvedPath); found {
		t.Fatal("read token survived conversation reset")
	}
}

func TestSessionResetConversationStateWhileRunActiveIsRejected(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	blocker := newBlockingRunner()
	session.resources.runner = blocker

	done := make(chan error, 1)
	go func() {
		_, err := session.Run(context.Background(), RunRequest{Prompt: "first"})
		done <- err
	}()

	<-blocker.started

	if err := session.ResetConversationState(); !errors.Is(err, ErrTopLevelRunActive) {
		t.Fatalf("expected ErrTopLevelRunActive, got %v", err)
	}

	close(blocker.release)
	if err := <-done; err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
}

func TestSessionResetConversationStateDelegatesToCanonicalStateReset(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := session.stores.history.Write([]string{"history"}); err != nil {
		t.Fatalf("history.Write before reset: %v", err)
	}
	if err := session.stores.memory.Write(store.MemoryState{Entries: []string{"memory"}}); err != nil {
		t.Fatalf("memory.Write before reset: %v", err)
	}
	if err := session.stores.todos.Write(store.TodoState{Items: []store.TodoItem{{Text: "todo", Done: false}}}); err != nil {
		t.Fatalf("todos.Write before reset: %v", err)
	}
	if err := session.stores.plan.Write("plan"); err != nil {
		t.Fatalf("plan.Write before reset: %v", err)
	}
	writeFileInTest(t, session.Paths.BackupRoot+"/keep.txt", "backup")

	metadataBefore, found, err := state.ReadMetadata(session.layout)
	if err != nil {
		t.Fatalf("ReadMetadata before reset: %v", err)
	}
	if !found {
		t.Fatal("expected metadata before reset")
	}

	if err := session.ResetConversationState(); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}

	history, err := session.stores.history.Read()
	if err != nil {
		t.Fatalf("history.Read after reset: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history after reset = %#v, want empty", history)
	}

	memory, err := session.stores.memory.Read()
	if err != nil {
		t.Fatalf("memory.Read after reset: %v", err)
	}
	if len(memory.Entries) != 0 {
		t.Fatalf("memory after reset = %#v, want empty", memory)
	}

	todos, err := session.stores.todos.Read()
	if err != nil {
		t.Fatalf("todos.Read after reset: %v", err)
	}
	if len(todos.Items) != 0 {
		t.Fatalf("todos after reset = %#v, want empty", todos)
	}

	plan, err := session.stores.plan.Read()
	if err != nil {
		t.Fatalf("plan.Read after reset: %v", err)
	}
	if plan != "" {
		t.Fatalf("plan after reset = %q, want empty string", plan)
	}
	if _, err := os.Stat(session.Paths.BackupRoot + "/keep.txt"); err != nil {
		t.Fatalf("expected backup to remain: %v", err)
	}

	metadataAfter, found, err := state.ReadMetadata(session.layout)
	if err != nil {
		t.Fatalf("ReadMetadata after reset: %v", err)
	}
	if !found {
		t.Fatal("expected metadata after reset")
	}
	if metadataAfter != metadataBefore {
		t.Fatalf("metadata changed across reset: before=%+v after=%+v", metadataBefore, metadataAfter)
	}
}

type runnerFunc func(context.Context, RunRequest) (RunResult, error)

func (f runnerFunc) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	return f(ctx, request)
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockingRunner) Run(_ context.Context, _ RunRequest) (RunResult, error) {
	close(r.started)
	<-r.release
	return RunResult{Completed: true}, nil
}

type countingSandboxRuntime struct {
	starts       int
	closes       int
	executes     int
	stdout       string
	stderr       string
	startErr     error
	closeErr     error
	lastRequest  falkenruntime.SandboxCommandRequest
	proxyHTTP    string
	proxyHTTPS   string
	proxyNoProxy string
}

func (r *countingSandboxRuntime) Start(context.Context) error {
	r.starts++
	return r.startErr
}

func (r *countingSandboxRuntime) Execute(_ context.Context, request falkenruntime.SandboxCommandRequest) (falkenruntime.SandboxCommandResult, error) {
	r.executes++
	r.lastRequest = request
	if request.Stdout != nil && r.stdout != "" {
		_, _ = request.Stdout.Write([]byte(r.stdout))
	}
	if request.Stderr != nil && r.stderr != "" {
		_, _ = request.Stderr.Write([]byte(r.stderr))
	}
	return falkenruntime.SandboxCommandResult{
		Started:  true,
		ExitCode: 0,
	}, nil
}

func (r *countingSandboxRuntime) Close(context.Context) error {
	r.closes++
	return r.closeErr
}

func (r *countingSandboxRuntime) SetProxy(httpProxy, httpsProxy, noProxy string) {
	r.proxyHTTP = httpProxy
	r.proxyHTTPS = httpsProxy
	r.proxyNoProxy = noProxy
}

type countingNetworkProxy struct {
	starts   int
	closes   int
	endpoint ProxyEndpoint
	closeErr error
}

func (p *countingNetworkProxy) Start(context.Context) error {
	p.starts++
	return nil
}

func (p *countingNetworkProxy) Close(context.Context) error {
	p.closes++
	return p.closeErr
}

func (p *countingNetworkProxy) Endpoint() ProxyEndpoint {
	return p.endpoint
}

type countingNetworkPolicy struct {
	starts   int
	closes   int
	closeErr error
}

func (p *countingNetworkPolicy) Start(context.Context) error {
	p.starts++
	return nil
}

func (p *countingNetworkPolicy) Close(context.Context) error {
	p.closes++
	return p.closeErr
}

type runtimeAdaptersProvider struct {
	adapters RuntimeAdapters
	err      error
}

func (p runtimeAdaptersProvider) NewRuntimeAdapters(context.Context, RuntimeAdapterRequest) (RuntimeAdapters, error) {
	return p.adapters, p.err
}

func assertPolicyWrappedWorkspaceOperations(t *testing.T, got workspacefiles.Operations, want workspacefiles.Operations, label string) {
	t.Helper()
	wrapped, ok := got.(*policyCheckingWorkspaceOperations)
	if !ok {
		t.Fatalf("fileOperations %s = %T, want policy-checking wrapper", label, got)
	}
	if wrapped.delegate != want {
		t.Fatalf("fileOperations delegate %s = %T, want supplied remote ops", label, wrapped.delegate)
	}
}

type stubWorkspaceOperations struct{}

func (stubWorkspaceOperations) ReadFile(context.Context, workspacefiles.ReadFileRequest) (workspacefiles.ReadFileResult, error) {
	return workspacefiles.ReadFileResult{}, nil
}

func (stubWorkspaceOperations) ReadFiles(context.Context, workspacefiles.ReadFilesRequest) (workspacefiles.ReadFilesResult, error) {
	return workspacefiles.ReadFilesResult{}, nil
}

func (stubWorkspaceOperations) Glob(context.Context, workspacefiles.GlobRequest) (workspacefiles.GlobResult, error) {
	return workspacefiles.GlobResult{}, nil
}

func (stubWorkspaceOperations) Grep(context.Context, workspacefiles.GrepRequest) (workspacefiles.GrepResult, error) {
	return workspacefiles.GrepResult{}, nil
}

func (stubWorkspaceOperations) WriteFile(context.Context, workspacefiles.WriteFileRequest) (workspacefiles.WriteFileResult, error) {
	return workspacefiles.WriteFileResult{}, nil
}

func (stubWorkspaceOperations) EditFile(context.Context, workspacefiles.EditFileRequest) (workspacefiles.EditFileResult, error) {
	return workspacefiles.EditFileResult{}, nil
}

func (stubWorkspaceOperations) MultiEdit(context.Context, workspacefiles.MultiEditRequest) (workspacefiles.MultiEditResult, error) {
	return workspacefiles.MultiEditResult{}, nil
}

func (stubWorkspaceOperations) ApplyPatch(context.Context, workspacefiles.ApplyPatchRequest) (workspacefiles.ApplyPatchResult, error) {
	return workspacefiles.ApplyPatchResult{}, nil
}

func (stubWorkspaceOperations) DeleteFile(context.Context, workspacefiles.DeleteFileRequest) (workspacefiles.DeleteFileResult, error) {
	return workspacefiles.DeleteFileResult{}, nil
}

type countingRuntimeProvider struct {
	calls         int
	useContextErr bool
	cancel        context.CancelFunc
}

func (p *countingRuntimeProvider) NewRuntimeAdapters(ctx context.Context, _ RuntimeAdapterRequest) (RuntimeAdapters, error) {
	p.calls++
	if p.cancel != nil {
		p.cancel()
	}
	if p.useContextErr {
		return RuntimeAdapters{}, ctx.Err()
	}
	return RuntimeAdapters{SandboxRuntime: &countingSandboxRuntime{}}, nil
}

func newTestSessionRuntime(t *testing.T, layout state.Layout, sandboxRuntime SandboxRuntimeHandle) *sessionRuntime {
	t.Helper()

	executionState, err := runtimeexec.NewExecutionStateForLayout(layout)
	if err != nil {
		t.Fatalf("NewExecutionStateForLayout: %v", err)
	}

	policyEngine := policy.NewEngine(policy.Config{}, nil)
	executionPolicy := runtimepolicy.NewEvaluator(policyEngine)
	commandExecutor := runtimeexec.NewLocalExecutor(executionPolicy)
	backgroundManager := runtimeexec.NewBackgroundManager(executionPolicy)

	return newSessionRuntime(
		layout,
		executionState,
		policyEngine,
		executionPolicy,
		commandExecutor,
		backgroundManager,
		sandboxRuntime,
	)
}

func newSandboxTestSessionRuntime(t *testing.T, layout state.Layout, config SessionConfig, sandboxRuntime falkenruntime.SandboxRuntime) *sessionRuntime {
	t.Helper()

	executionState, err := runtimeexec.NewExecutionStateForLayout(layout)
	if err != nil {
		t.Fatalf("NewExecutionStateForLayout: %v", err)
	}

	policyEngine := policy.NewEngine(toInternalPolicyConfig(config.Policy), approvalHandlerAdapter{handler: config.ApprovalHandler})
	executionPolicy := runtimepolicy.NewEvaluator(policyEngine)
	commandExecutor := runtimeexec.NewSandboxExecutor(executionPolicy, sandboxRuntime)
	backgroundManager := runtimeexec.NewBackgroundManager(executionPolicy)
	runtime := newSessionRuntime(
		layout,
		executionState,
		policyEngine,
		executionPolicy,
		commandExecutor,
		backgroundManager,
		sandboxRuntime,
	)
	fileOps, err := newSessionFileOperations(layout, executionPolicy, executionState.SandboxMountPath())
	if err != nil {
		t.Fatalf("newSessionFileOperations: %v", err)
	}
	runtime.fileOperations = fileOps
	return runtime
}

func waitForBackgroundStatus(t *testing.T, manager *runtimeexec.BackgroundManager, id string, want runtimeexec.BackgroundProcessStatus) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, found := manager.Get(id)
		if !found {
			t.Fatalf("background process %q not found", id)
		}
		if snapshot.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	snapshot, found := manager.Get(id)
	if !found {
		t.Fatalf("background process %q not found", id)
	}
	t.Fatalf("background process %q status = %q, want %q", id, snapshot.Status, want)
}

func newSession(t *testing.T) *Session {
	t.Helper()

	session, err := newSessionWithConfig(tempWorkspace(t), "", SessionConfig{})

	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.config.Execution.Mode = ExecutionModeLocal

	return session
}

func tempWorkspace(t *testing.T) string {
	t.Helper()

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	return workspace
}

func writeFileInTest(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func setStateHomeEnv(t *testing.T) {
	t.Helper()

	stateHome := filepath.Join(t.TempDir(), "state-home")
	t.Setenv("XDG_STATE_HOME", stateHome)
}
