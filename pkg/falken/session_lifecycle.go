package falken

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/policy"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/store"
	"github.com/smasonuk/falken-core/internal/workspace"
)

// lifecycleState represents the internal state machine for session startup/shutdown.
type lifecycleState int

const (
	lifecycleNew lifecycleState = iota
	lifecycleStarting
	lifecycleStarted
	lifecycleClosing
	lifecycleClosed
)

// topLevelRunner is the internal interface for executing top-level agent runs.
type topLevelRunner interface {
	Run(context.Context, RunRequest) (RunResult, error)
}

// noopRunner is a topLevelRunner that immediately completes successfully.
type noopRunner struct{}

// Run implements topLevelRunner for noopRunner.
func (noopRunner) Run(_ context.Context, _ RunRequest) (RunResult, error) {
	return RunResult{Completed: true}, nil
}

// sessionAgentRunnerFactory defines the signature for initializing an agent runner.
type sessionAgentRunnerFactory func(SessionConfig, conversationStores, *sessionToolHub, *agent.ModeState, *sessionRuntime) (*sessionAgentRunner, error)

// ownedResources groups session-owned runtime integrations.
type ownedResources struct {
	runner         topLevelRunner
	runtime        *sessionRuntime
	runtimeFactory sessionRuntimeFactory
	agentFactory   sessionAgentRunnerFactory
	toolHub        *sessionToolHub
	hookHub        *sessionHookHub
	agentMode      *agent.ModeState
	agentRunner    *agent.Runner
}

// conversationStores groups the internal persistence mechanisms for agent state.
type conversationStores struct {
	history         store.HistoryBackend
	memory          store.MemoryBackend
	todos           store.TodoBackend
	plan            store.PlanBackend
	commandEvidence store.CommandEvidenceBackend
}

// newConversationStores initializes the required persistence stores for a given layout.
func newConversationStores(layout state.Layout) conversationStores {
	return conversationStores{
		history:         store.NewHistoryStore(layout),
		memory:          store.NewMemoryStore(layout),
		todos:           store.NewTodoStore(layout),
		plan:            store.NewPlanStore(layout),
		commandEvidence: store.NewCommandEvidenceStore(layout),
	}
}

func newConversationStoresWithConfig(paths Paths, layout state.Layout, config SessionConfig) (conversationStores, error) {
	if config.StateBackendProvider == nil {
		return newConversationStores(layout), nil
	}
	backend, err := config.StateBackendProvider.NewStateBackend(StateBackendRequest{Paths: paths})
	if err != nil {
		return conversationStores{}, fmt.Errorf("initialize state backend: %w", err)
	}
	if backend == nil {
		return conversationStores{}, fmt.Errorf("initialize state backend: %w", ErrInvalidConfig)
	}
	return conversationStores{
		history:         store.NewBlobHistoryStore(backend),
		memory:          store.NewBlobMemoryStore(backend),
		todos:           store.NewBlobTodoStore(backend),
		plan:            store.NewBlobPlanStore(backend),
		commandEvidence: store.NewBlobCommandEvidenceStore(backend),
	}, nil
}

// Session owns the canonical path/state context and assembled v1 runtime resources.
type Session struct {
	Paths Paths

	mu        sync.Mutex
	layout    state.Layout
	metadata  state.Metadata
	state     lifecycleState
	runActive bool
	stores    conversationStores

	config    SessionConfig
	resources ownedResources
}

// NewSession resolves a canonical workspace/state session.
//
// Deprecated: use New with Config so required runtime dependencies are validated.
// NewSession is retained as a compatibility/testing helper and does not
// validate that an LLM is configured.
// func newSession(workspaceDir, stateDir string) (*Session, error) {
// 	return newSessionWithConfig(workspaceDir, stateDir, SessionConfig{})
// }

// New constructs a session from the public v1 config contract.
func New(config Config) (*Session, error) {
	return NewSessionFromConfig(config)
}

// NewSessionFromConfig constructs a session from the public v1 config contract.
func NewSessionFromConfig(config Config) (*Session, error) {
	if config.LLM == nil {
		return nil, ErrLLMRequired
	}

	session, err := newSessionWithConfig(config.WorkspaceDir, config.StateDir, SessionConfig{
		ToolProviders:                   append([]ToolProvider(nil), config.ToolProviders...),
		BuiltinTools:                    cloneBuiltinToolsConfig(config.BuiltinTools),
		AllowWorkspaceToolsInRemoteMode: config.AllowWorkspaceToolsInRemoteMode,
		HookProviders:                   append([]HookProvider(nil), config.HookProviders...),
		Execution:                       config.ExecutionDetails,
		LLM:                             config.LLM,
		VerificationReviewerLLM:         config.VerificationReviewerLLM,
		BaseSystemPrompt:                config.BaseSystemPrompt,
		PlanRouting:                     config.PlanRouting,
		Events:                          config.Events,
		OnCompleted:                     config.OnCompleted,
		Policy:                          config.Policy,
		ApprovalHandler:                 config.ApprovalHandler,
		Runtime:                         config.Runtime,
		StateBackendProvider:            config.StateBackendProvider,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	return session, nil
}

// newSessionWithConfig resolves a canonical workspace/state session with extension discovery config.
//
// Deprecated: use New with Config so required runtime dependencies are validated.
// newSessionWithConfig is retained as a compatibility/testing helper and does
// not validate that an LLM is configured.
func newSessionWithConfig(workspaceDir, stateDir string, config SessionConfig) (*Session, error) {
	if strings.ContainsRune(workspaceDir, 0) {
		return nil, fmt.Errorf("%w: workspace dir contains NUL", ErrInvalidConfig)
	}
	if strings.ContainsRune(stateDir, 0) {
		return nil, fmt.Errorf("%w: state dir contains NUL", ErrInvalidConfig)
	}

	workspaceRoot, err := workspace.NormalizeRoot(workspaceDir)
	if err != nil {
		return nil, err
	}

	layout, err := state.ResolveLayout(workspaceRoot, stateDir)
	if err != nil {
		return nil, err
	}

	paths := newPaths(layout)
	stores, err := newConversationStoresWithConfig(paths, layout, config)
	if err != nil {
		return nil, err
	}

	return &Session{
		Paths:     paths,
		layout:    layout,
		state:     lifecycleNew,
		stores:    stores,
		config:    cloneSessionConfig(config),
		resources: ownedResources{runner: noopRunner{}},
	}, nil
}

func (s *Session) Start() error {
	return s.StartContext(context.Background())
}

// StartContext initializes the session's canonical state and metadata once.
// If startup fails after creating runtime resources, the created resources are
// cleaned up and the session remains not-started so Start may be retried.
func (s *Session) StartContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	switch s.state {
	case lifecycleClosed:
		s.mu.Unlock()
		return ErrSessionClosed
	case lifecycleStarted:
		s.mu.Unlock()
		return nil
	case lifecycleStarting:
		s.mu.Unlock()
		return ErrSessionStarting
	case lifecycleClosing:
		s.mu.Unlock()
		return ErrSessionClosing
	case lifecycleNew:
		s.state = lifecycleStarting
	default:
		state := s.state
		s.mu.Unlock()
		return fmt.Errorf("unknown session lifecycle state: %d", state)
	}
	layout := s.layout
	config := s.config
	stores := s.stores
	runtimeFactory := s.resources.runtimeFactory
	agentFactory := s.resources.agentFactory
	s.mu.Unlock()

	failStart := func(err error) error {
		s.mu.Lock()
		if s.state == lifecycleStarting {
			s.state = lifecycleNew
		}
		s.mu.Unlock()
		return err
	}

	if err := state.EnsureLayoutState(layout); err != nil {
		return failStart(fmt.Errorf("initialize session state: %w", err))
	}

	_, _, err := state.ReadMetadata(layout)
	if err != nil {
		return failStart(fmt.Errorf("read session metadata: %w", err))
	}

	metadata, err := state.TouchMetadata(layout)
	if err != nil {
		return failStart(fmt.Errorf("initialize session metadata: %w", err))
	}

	if !metadata.ProjectPermissionsInitialized {
		metadata, err = initializeProjectPermissionsDefaults(ctx, layout, metadata)
		if err != nil {
			return failStart(fmt.Errorf("initialize default project permissions: %w", err))
		}
	}

	if runtimeFactory == nil {
		if config.Runtime != nil {
			provider := config.Runtime
			runtimeFactory = func(ctx context.Context, l state.Layout, cfg SessionConfig) (*sessionRuntime, error) {
				return newAdapterSessionRuntime(ctx, l, cfg, provider)
			}
		} else {
			if config.Execution.Mode != ExecutionModeLocal {
				return failStart(fmt.Errorf("%w: runtime provider is required for sandbox execution", ErrInvalidConfig))
			}
			runtimeFactory = newLocalOnlySessionRuntime
		}
	}
	if err := ctx.Err(); err != nil {
		return failStart(err)
	}
	runtime, err := runtimeFactory(ctx, layout, config)
	if err != nil {
		return failStart(fmt.Errorf("initialize session runtime: %w", err))
	}
	if err := runtime.start(ctx); err != nil {
		if closeErr := runtime.close(ctx); closeErr != nil {
			return failStart(fmt.Errorf("start session runtime: %w", errors.Join(err, fmt.Errorf("cleanup failed: %w", closeErr))))
		}
		return failStart(fmt.Errorf("start session runtime: %w", err))
	}
	toolHub := newSessionToolHub(config.BuiltinTools, config.ToolProviders...)
	toolHub.remoteWorkspaceMode = runtime.isRemoteWorkspaceMode()
	toolHub.allowWorkspaceToolsInRemoteMode = config.AllowWorkspaceToolsInRemoteMode
	host := newSessionToolHost(layout, runtime, stores.commandEvidence, config.Events)
	if err := toolHub.start(ctx, host); err != nil {
		err = joinCleanupError(err, "tools", toolHub.close(ctx))
		err = joinCleanupError(err, "runtime", runtime.close(ctx))
		return failStart(fmt.Errorf("initialize session tools: %w", err))
	}
	hookHub := newSessionHookHub(config.HookProviders...)
	if err := hookHub.start(ctx, host); err != nil {
		err = joinCleanupError(err, "hooks", hookHub.close(ctx))
		err = joinCleanupError(err, "tools", toolHub.close(ctx))
		err = joinCleanupError(err, "runtime", runtime.close(ctx))
		return failStart(fmt.Errorf("initialize session hooks: %w", err))
	}
	agentMode := agent.NewModeState(stores.plan)
	var runner *sessionAgentRunner
	if agentFactory == nil {
		if config.LLM != nil {
			agentFactory = newSessionAgentRunner
		}
	}
	if agentFactory != nil {
		runner, err = agentFactory(config, stores, toolHub, agentMode, runtime)
		if err != nil {
			err = joinCleanupError(err, "hooks", hookHub.close(ctx))
			err = joinCleanupError(err, "tools", toolHub.close(ctx))
			err = joinCleanupError(err, "runtime", runtime.close(ctx))
			return failStart(fmt.Errorf("initialize agent runtime: %w", err))
		}
		if runner == nil {
			err := errors.New("runner is unavailable")
			err = joinCleanupError(err, "hooks", hookHub.close(ctx))
			err = joinCleanupError(err, "tools", toolHub.close(ctx))
			err = joinCleanupError(err, "runtime", runtime.close(ctx))
			return failStart(fmt.Errorf("initialize agent runtime: %w", err))
		}
	}
	if err := hookHub.run(ctx, HookSessionStart, nil); err != nil {
		err = joinCleanupError(err, "hooks", hookHub.close(ctx))
		err = joinCleanupError(err, "tools", toolHub.close(ctx))
		err = joinCleanupError(err, "runtime", runtime.close(ctx))
		return failStart(fmt.Errorf("run session start hooks: %w", err))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != lifecycleStarting {
		return fmt.Errorf("unknown session lifecycle state: %d", s.state)
	}
	s.metadata = metadata
	s.resources.runtime = runtime
	s.resources.toolHub = toolHub
	s.resources.hookHub = hookHub
	s.resources.agentMode = agentMode
	if runner != nil {
		s.resources.agentRunner = runner.runner
		s.resources.runner = runner
	}
	s.state = lifecycleStarted
	return nil
}

// joinCleanupError combines a primary error with a secondary resource cleanup error.
func joinCleanupError(err error, label string, cleanupErr error) error {
	if cleanupErr == nil {
		return err
	}
	return errors.Join(err, fmt.Errorf("%s cleanup failed: %w", label, cleanupErr))
}

// Run executes a single top-level request while enforcing at-most-one-active-run semantics.
func (s *Session) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	s.mu.Lock()
	switch s.state {
	case lifecycleClosed:
		s.mu.Unlock()
		return RunResult{Error: ErrSessionClosed.Error()}, ErrSessionClosed
	case lifecycleNew:
		s.mu.Unlock()
		return RunResult{Error: ErrSessionNotStarted.Error()}, ErrSessionNotStarted
	case lifecycleStarting:
		s.mu.Unlock()
		return RunResult{Error: ErrSessionStarting.Error()}, ErrSessionStarting
	case lifecycleClosing:
		s.mu.Unlock()
		return RunResult{Error: ErrSessionClosing.Error()}, ErrSessionClosing
	case lifecycleStarted:
		if s.runActive {
			s.mu.Unlock()
			return RunResult{Error: ErrTopLevelRunActive.Error()}, ErrTopLevelRunActive
		}

		runner := s.resources.runner
		s.runActive = true
		s.mu.Unlock()

		defer func() {
			s.mu.Lock()
			s.runActive = false
			s.mu.Unlock()
		}()

		return runner.Run(ctx, request)
	default:
		s.mu.Unlock()
		err := fmt.Errorf("unknown session lifecycle state: %d", s.state)
		return RunResult{Error: err.Error()}, err
	}
}

// ResetConversationState clears canonical conversation-scoped state while preserving durable state.
func (s *Session) ResetConversationState() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state == lifecycleClosed {
		return ErrSessionClosed
	}
	if s.state == lifecycleStarting {
		return ErrSessionStarting
	}
	if s.state == lifecycleClosing {
		return ErrSessionClosing
	}
	if s.runActive {
		return ErrTopLevelRunActive
	}

	if err := state.ResetConversationState(s.layout); err != nil {
		return err
	}
	if s.resources.agentMode != nil {
		s.resources.agentMode.Reset()
	}
	if s.resources.runtime != nil && s.resources.runtime.localFileOps && s.resources.runtime.executionPolicy != nil {
		sandboxMountPath := ""
		if s.resources.runtime.executionState != nil {
			sandboxMountPath = s.resources.runtime.executionState.SandboxMountPath()
		}
		fileOperations, err := newSessionFileOperations(s.layout, s.resources.runtime.executionPolicy, sandboxMountPath)
		if err != nil {
			return err
		}
		s.resources.runtime.fileOperations = fileOperations
	}
	return nil
}

func (s *Session) Close() error {
	return s.CloseContext(context.Background())
}

// CloseContext releases session-owned resources and prevents future starts or runs.
func (s *Session) CloseContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	switch s.state {
	case lifecycleClosed:
		s.mu.Unlock()
		return nil
	case lifecycleStarting:
		s.mu.Unlock()
		return ErrSessionStarting
	case lifecycleClosing:
		s.mu.Unlock()
		return ErrSessionClosing
	case lifecycleNew:
		s.state = lifecycleClosed
		s.resources = ownedResources{}
		s.metadata = state.Metadata{}
		s.mu.Unlock()
		return nil
	}
	if s.runActive {
		s.mu.Unlock()
		return ErrTopLevelRunActive
	}
	resources := s.resources
	s.state = lifecycleClosing
	s.mu.Unlock()

	if err := resources.close(ctx); err != nil {
		s.mu.Lock()
		s.state = lifecycleClosed
		s.resources = ownedResources{}
		s.metadata = state.Metadata{}
		s.mu.Unlock()
		return fmt.Errorf("close session resources: %w", err)
	}

	s.mu.Lock()
	s.state = lifecycleClosed
	s.resources = ownedResources{}
	s.metadata = state.Metadata{}
	s.mu.Unlock()
	return nil
}

// close shuts down all session-owned resources.
func (r ownedResources) close(ctx context.Context) error {
	var closeErr error
	if r.hookHub != nil {
		if err := r.hookHub.run(ctx, HookSessionClose, nil); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("run session close hooks: %w", err))
		}
	}
	if r.hookHub != nil {
		if err := r.hookHub.close(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close session hooks: %w", err))
		}
	}
	if r.toolHub != nil {
		if err := r.toolHub.close(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close session tools: %w", err))
		}
	}
	if r.runtime != nil {
		if err := r.runtime.close(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close session runtime: %w", err))
		}
	}
	return closeErr
}

// cloneSessionConfig creates a shallow copy of a SessionConfig.
func cloneSessionConfig(config SessionConfig) SessionConfig {
	return SessionConfig{
		ToolProviders:                   append([]ToolProvider(nil), config.ToolProviders...),
		BuiltinTools:                    cloneBuiltinToolsConfig(config.BuiltinTools),
		AllowWorkspaceToolsInRemoteMode: config.AllowWorkspaceToolsInRemoteMode,
		HookProviders:                   append([]HookProvider(nil), config.HookProviders...),
		Execution:                       config.Execution,
		LLM:                             config.LLM,
		VerificationReviewerLLM:         config.VerificationReviewerLLM,
		BaseSystemPrompt:                config.BaseSystemPrompt,
		PlanRouting:                     config.PlanRouting,
		Events:                          config.Events,
		OnCompleted:                     config.OnCompleted,
		Policy:                          config.Policy,
		ApprovalHandler:                 config.ApprovalHandler,
		Runtime:                         config.Runtime,
		StateBackendProvider:            config.StateBackendProvider,
	}
}

func cloneBuiltinToolsConfig(config BuiltinToolsConfig) BuiltinToolsConfig {
	return BuiltinToolsConfig{
		Mode:  config.Mode,
		Names: append([]string(nil), config.Names...),
	}
}

// toInternalPolicyConfig converts the public PolicyConfig to the internal policy.Config.
func toInternalPolicyConfig(config PolicyConfig) policy.Config {
	return policy.Config(config)
}

// approvalHandlerAdapter bridges the public ApprovalHandler to the internal policy engine.
type approvalHandlerAdapter struct {
	handler ApprovalHandler
}

// ApproveFile implements the internal policy approval for file operations.
func (a approvalHandlerAdapter) ApproveFile(ctx context.Context, request policy.FileRequest) (policy.ApprovalScope, error) {
	if a.handler == nil {
		return policy.ApprovalScopeDeny, nil
	}
	return a.handler.ApproveFile(ctx, request)
}

// ApproveShell implements the internal policy approval for shell commands.
func (a approvalHandlerAdapter) ApproveShell(ctx context.Context, request policy.ShellRequest) (policy.ApprovalScope, error) {
	if a.handler == nil {
		return policy.ApprovalScopeDeny, nil
	}
	return a.handler.ApproveShell(ctx, request)
}

// ApproveNetwork implements the internal policy approval for network connections.
func (a approvalHandlerAdapter) ApproveNetwork(ctx context.Context, request policy.NetworkRequest) (policy.ApprovalScope, error) {
	if a.handler == nil {
		return policy.ApprovalScopeDeny, nil
	}
	return a.handler.ApproveNetwork(ctx, request)
}
