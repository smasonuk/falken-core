package falken

import (
	"context"
	"errors"
	"fmt"

	"github.com/smasonuk/falken-core/internal/files"
	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

var errSessionRuntimeUnavailable = errors.New("session runtime is unavailable")

var _ workspacefiles.Operations = (*runtimefiles.Operations)(nil)

// ErrBackgroundProcessUnavailableInRemoteMode reports that background processes
// are intentionally disabled when the runner owns workspace execution.
var ErrBackgroundProcessUnavailableInRemoteMode = errors.New("background processes are unavailable in remote workspace mode")

// sessionRuntimeFactory defines the signature for initializing a session runtime.
type sessionRuntimeFactory func(context.Context, state.Layout, SessionConfig) (*sessionRuntime, error)

// sessionRuntime groups the internal execution, policy, and background services.
type sessionRuntime struct {
	executionState      *runtimeexec.ExecutionState
	policyEngine        *policy.Engine
	executionPolicy     *runtimepolicy.Evaluator
	commandExecutor     runtimeexec.CommandExecutor
	backgroundManager   *runtimeexec.BackgroundManager
	fileOperations      workspacefiles.Operations
	localFileOps        bool
	remoteWorkspaceMode bool
	sandboxRuntime      SandboxRuntimeHandle
	networkProxy        NetworkProxyHandle
	networkPolicy       NetworkPolicyHandle
}

// newLocalOnlySessionRuntime builds a session runtime using the local executor.
// It is the default when no RuntimeProvider is configured.
func newLocalOnlySessionRuntime(ctx context.Context, layout state.Layout, config SessionConfig) (*sessionRuntime, error) {
	_ = ctx
	executionState, err := runtimeexec.NewExecutionStateForLayout(layout)
	if err != nil {
		return nil, err
	}
	executionState.SetSandboxMountPath(sessionWorkspaceMountPath(config.Execution))
	policyEngine, err := newSessionPolicyEngine(layout, config)
	if err != nil {
		return nil, err
	}
	executionPolicy := runtimepolicy.NewEvaluator(policyEngine)
	shellPath := sessionShellPath(config.Execution)
	commandExecutor := runtimeexec.NewLocalExecutorWithShell(executionPolicy, shellPath)
	backgroundManager := runtimeexec.NewBackgroundManagerWithShell(executionPolicy, shellPath)
	fileOperations, err := newSessionFileOperations(layout, executionPolicy, executionState.SandboxMountPath())
	if err != nil {
		return nil, err
	}
	rt := newSessionRuntime(layout, executionState, policyEngine, executionPolicy, commandExecutor, backgroundManager, nil)
	rt.fileOperations = fileOperations
	rt.localFileOps = true
	rt.remoteWorkspaceMode = false
	return rt, nil
}

// newAdapterSessionRuntime initializes a session runtime backed by a custom RuntimeProvider.
func newAdapterSessionRuntime(ctx context.Context, layout state.Layout, config SessionConfig, provider RuntimeProvider) (*sessionRuntime, error) {
	policyEngine, err := newSessionPolicyEngine(layout, config)
	if err != nil {
		return nil, err
	}
	executionPolicy := runtimepolicy.NewEvaluator(policyEngine)
	networkPolicy := networkPolicyEvaluatorAdapter{evaluator: executionPolicy}

	adapters, err := provider.NewRuntimeAdapters(ctx, RuntimeAdapterRequest{
		WorkspaceRoot: layout.WorkspaceRoot,
		StateRoot:     layout.StateRoot,
		Execution:     config.Execution,
		Events:        config.Events,
		NetworkPolicy: networkPolicy,
	})
	if err != nil {
		return nil, err
	}
	cleanupAdapters := func(err error) (*sessionRuntime, error) {
		if closeErr := closeRuntimeAdapters(ctx, adapters); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("cleanup runtime adapters: %w", closeErr))
		}
		return nil, err
	}

	var executionState *runtimeexec.ExecutionState
	if adapters.WorkspaceFiles != nil {
		executionState, err = runtimeexec.NewVirtualExecutionStateForLayout(layout)
	} else {
		executionState, err = runtimeexec.NewExecutionStateForLayout(layout)
	}
	if err != nil {
		return cleanupAdapters(err)
	}
	executionState.SetSandboxMountPath(sessionWorkspaceMountPath(config.Execution))

	var commandExecutor runtimeexec.CommandExecutor
	switch config.Execution.Mode {
	case ExecutionModeLocal:
		commandExecutor = runtimeexec.NewLocalExecutorWithShell(executionPolicy, sessionShellPath(config.Execution))
	case "", ExecutionModeSandbox:
		if adapters.SandboxRuntime == nil {
			return cleanupAdapters(fmt.Errorf("%w: sandbox runtime provider returned nil sandbox runtime", ErrInvalidConfig))
		}
		commandExecutor = runtimeexec.NewSandboxExecutor(executionPolicy, adapters.SandboxRuntime)
	default:
		return cleanupAdapters(fmt.Errorf("%w: unsupported execution mode %q", ErrInvalidConfig, config.Execution.Mode))
	}

	backgroundManager := runtimeexec.NewBackgroundManagerWithShell(executionPolicy, sessionShellPath(config.Execution))
	fileOperations := adapters.WorkspaceFiles
	localFileOps := false
	remoteWorkspaceMode := adapters.WorkspaceFiles != nil
	if fileOperations == nil {
		fileOperations, err = newSessionFileOperations(layout, executionPolicy, executionState.SandboxMountPath())
		if err != nil {
			return cleanupAdapters(err)
		}
		localFileOps = true
	} else {
		fileOperations = newPolicyCheckingWorkspaceOperations(fileOperations, executionPolicy, executionState, config.Events)
	}
	rt := newSessionRuntime(layout, executionState, policyEngine, executionPolicy, commandExecutor, backgroundManager, adapters.SandboxRuntime)
	rt.fileOperations = fileOperations
	rt.localFileOps = localFileOps
	rt.remoteWorkspaceMode = remoteWorkspaceMode
	rt.networkProxy = adapters.NetworkProxy
	rt.networkPolicy = adapters.NetworkPolicy
	return rt, nil
}

// closeRuntimeAdapters shuts down initialized runtime provider adapters.
func closeRuntimeAdapters(ctx context.Context, adapters RuntimeAdapters) error {
	var closeErr error
	if adapters.SandboxRuntime != nil {
		closeErr = errors.Join(closeErr, adapters.SandboxRuntime.Close(ctx))
	}
	if adapters.WorkspaceFiles != nil {
		closeErr = errors.Join(closeErr, closeWorkspaceFiles(ctx, adapters.WorkspaceFiles))
	}
	if adapters.NetworkPolicy != nil {
		closeErr = errors.Join(closeErr, adapters.NetworkPolicy.Close(ctx))
	}
	if adapters.NetworkProxy != nil {
		closeErr = errors.Join(closeErr, adapters.NetworkProxy.Close(ctx))
	}
	return closeErr
}

// sessionShellPath resolves the shell path to use for command execution.
func sessionShellPath(config ExecutionConfig) string {
	if config.ShellPath != "" {
		return config.ShellPath
	}
	return "/bin/sh"
}

func sessionWorkspaceMountPath(config ExecutionConfig) string {
	if config.WorkspaceMountPath != "" {
		return config.WorkspaceMountPath
	}
	return "/workspace"
}

// networkPolicyEvaluatorAdapter bridges the internal evaluator to the public network policy interface.
type networkPolicyEvaluatorAdapter struct {
	evaluator *runtimepolicy.Evaluator
}

// EvaluateNetworkPolicy checks network access against the session's execution policy.
func (a networkPolicyEvaluatorAdapter) EvaluateNetworkPolicy(ctx context.Context, request NetworkPolicyEvaluationRequest) (NetworkPolicyEvaluationResult, error) {
	result, err := a.evaluator.EvaluateNetwork(ctx, runtimepolicy.NetworkRequest{
		Host:             request.Host,
		Port:             request.Port,
		Method:           request.Method,
		Target:           request.Target,
		ApprovalRequired: request.ApprovalRequired,
	})
	return NetworkPolicyEvaluationResult{
		Allowed:     result.Allowed,
		Explanation: result.Explanation,
	}, err
}

// newSessionRuntime constructs a sessionRuntime from initialized core components.
func newSessionRuntime(
	layout state.Layout,
	executionState *runtimeexec.ExecutionState,
	policyEngine *policy.Engine,
	executionPolicy *runtimepolicy.Evaluator,
	commandExecutor runtimeexec.CommandExecutor,
	backgroundManager *runtimeexec.BackgroundManager,
	sandboxRuntime SandboxRuntimeHandle,
) *sessionRuntime {
	return &sessionRuntime{
		executionState:    executionState,
		policyEngine:      policyEngine,
		executionPolicy:   executionPolicy,
		commandExecutor:   commandExecutor,
		backgroundManager: backgroundManager,
		sandboxRuntime:    sandboxRuntime,
	}
}

// newSessionFileOperations initializes runtime file access constrained by policy.
func newSessionFileOperations(layout state.Layout, executionPolicy *runtimepolicy.Evaluator, sandboxMountPath string) (*runtimefiles.Operations, error) {
	service, err := files.NewServiceForLayout(layout, executionPolicy, "session")
	if err != nil {
		return nil, fmt.Errorf("initialize managed file service: %w", err)
	}
	service.SetSandboxMountPath(sandboxMountPath)
	operations, err := runtimefiles.NewOperations(service)
	if err != nil {
		return nil, fmt.Errorf("initialize runtime file operations: %w", err)
	}
	return operations, nil
}

// start initializes the runtime's network proxies and sandbox services.
func (r *sessionRuntime) start(ctx context.Context) error {
	if r == nil {
		return errSessionRuntimeUnavailable
	}
	if r.networkPolicy != nil {
		if err := r.networkPolicy.Start(ctx); err != nil {
			return fmt.Errorf("start network policy endpoint: %w", err)
		}
	}
	if r.networkProxy != nil {
		if err := r.networkProxy.Start(ctx); err != nil {
			return fmt.Errorf("start network proxy: %w", err)
		}
		r.configureSandboxProxy(r.networkProxy.Endpoint())
	}
	if err := startWorkspaceFiles(ctx, r.fileOperations); err != nil {
		return fmt.Errorf("start workspace file operations: %w", err)
	}
	if r.sandboxRuntime != nil {
		if err := r.sandboxRuntime.Start(ctx); err != nil {
			return fmt.Errorf("start sandbox runtime: %w", err)
		}
	}
	return nil
}

// close cleanly shuts down background tasks and sandbox services.
func (r *sessionRuntime) close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	var closeErr error
	if r.backgroundManager != nil {
		r.backgroundManager.StopAll()
	}
	if r.sandboxRuntime != nil {
		if err := r.sandboxRuntime.Close(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close sandbox runtime: %w", err))
		}
	}
	if err := closeWorkspaceFiles(ctx, r.fileOperations); err != nil {
		closeErr = errors.Join(closeErr, fmt.Errorf("close workspace file operations: %w", err))
	}
	if r.networkPolicy != nil {
		if err := r.networkPolicy.Close(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close network policy endpoint: %w", err))
		}
	}
	if r.networkProxy != nil {
		if err := r.networkProxy.Close(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close network proxy: %w", err))
		}
	}
	return closeErr
}

func (r *sessionRuntime) isRemoteWorkspaceMode() bool {
	return r != nil && r.remoteWorkspaceMode
}

// configureSandboxProxy propagates proxy settings to the underlying sandbox.
func (r *sessionRuntime) configureSandboxProxy(endpoint ProxyEndpoint) {
	if endpoint.HTTPProxyURL == "" && endpoint.HTTPSProxyURL == "" && endpoint.NoProxy == "" {
		return
	}
	if setter, ok := r.sandboxRuntime.(interface{ SetProxy(string, string, string) }); ok {
		setter.SetProxy(endpoint.HTTPProxyURL, endpoint.HTTPSProxyURL, endpoint.NoProxy)
	}
}

type workspaceFilesStarter interface {
	Start(context.Context) error
}

type workspaceFilesCloser interface {
	Close(context.Context) error
}

func startWorkspaceFiles(ctx context.Context, operations workspacefiles.Operations) error {
	if starter, ok := operations.(workspaceFilesStarter); ok {
		return starter.Start(ctx)
	}
	return nil
}

func closeWorkspaceFiles(ctx context.Context, operations workspacefiles.Operations) error {
	if closer, ok := operations.(workspaceFilesCloser); ok {
		return closer.Close(ctx)
	}
	return nil
}

// currentRuntimeLocked retrieves the active sessionRuntime under a session lock.
func (s *Session) currentRuntimeLocked() (*sessionRuntime, error) {
	switch s.state {
	case lifecycleClosed:
		return nil, ErrSessionClosed
	case lifecycleNew:
		return nil, ErrSessionNotStarted
	case lifecycleStarting:
		return nil, ErrSessionStarting
	case lifecycleClosing:
		return nil, ErrSessionClosing
	}
	if s.resources.runtime == nil {
		return nil, errSessionRuntimeUnavailable
	}
	return s.resources.runtime, nil
}

// RemoteWorkspaceMode reports whether the active session uses adapter-supplied
// workspace file operations instead of local workspace file operations.
func (s *Session) RemoteWorkspaceMode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resources.runtime == nil {
		return false
	}
	return s.resources.runtime.isRemoteWorkspaceMode()
}

// executeCommand runs a shell command through the session's runtime executor.
func (s *Session) executeCommand(ctx context.Context, request runtimeexec.CommandRequest) (runtimeexec.CommandResult, error) {
	s.mu.Lock()
	runtime, err := s.currentRuntimeLocked()
	s.mu.Unlock()
	if err != nil {
		return runtimeexec.CommandResult{}, err
	}

	return runtime.commandExecutor.Execute(ctx, runtime.executionState, request)
}

// startBackgroundProcess launches an asynchronous command through the background manager.
func (s *Session) startBackgroundProcess(ctx context.Context, request runtimeexec.BackgroundStartRequest) (runtimeexec.BackgroundStartResult, error) {
	s.mu.Lock()
	runtime, err := s.currentRuntimeLocked()
	s.mu.Unlock()
	if err != nil {
		return runtimeexec.BackgroundStartResult{}, err
	}
	if runtime.isRemoteWorkspaceMode() {
		return runtimeexec.BackgroundStartResult{
			Status:     runtimeexec.BackgroundStartFailedToStart,
			Command:    request.Command,
			StartError: ErrBackgroundProcessUnavailableInRemoteMode.Error(),
		}, ErrBackgroundProcessUnavailableInRemoteMode
	}

	return runtime.backgroundManager.Start(ctx, runtime.executionState, request)
}

// readBackgroundLogs retrieves output from a previously started background process.
func (s *Session) readBackgroundLogs(id string) (runtimeexec.BackgroundLogsResult, error) {
	s.mu.Lock()
	runtime, err := s.currentRuntimeLocked()
	s.mu.Unlock()
	if err != nil {
		return runtimeexec.BackgroundLogsResult{}, err
	}

	return runtime.backgroundManager.ReadLogs(id), nil
}
