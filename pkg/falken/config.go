package falken

import (
	"context"
	"time"

	"github.com/smasonuk/falken-core/internal/policy"
)

// Mode identifies the public v1 runtime mode.
type Mode string

const (
	ModeDefault Mode = "default"
	ModePlan    Mode = "plan"
)

// PlanRoutingMode selects how default-mode prompts are routed into plan mode.
type PlanRoutingMode string

const (
	// PlanRoutingHeuristic uses a deterministic local router and avoids an extra LLM call.
	PlanRoutingHeuristic PlanRoutingMode = "heuristic"
	// PlanRoutingLLM uses the configured LLM to classify whether plan mode is needed.
	PlanRoutingLLM PlanRoutingMode = "llm"
	// PlanRoutingDisabled disables automatic plan-mode routing.
	PlanRoutingDisabled PlanRoutingMode = "disabled"
)

// BuiltinToolsMode selects which Falken built-in tools a session exposes.
type BuiltinToolsMode string

const (
	// BuiltinToolsDefault preserves the normal built-in tool set.
	BuiltinToolsDefault BuiltinToolsMode = ""
	// BuiltinToolsAll exposes the normal built-in tool set.
	BuiltinToolsAll BuiltinToolsMode = "all"
	// BuiltinToolsNone exposes no Falken built-in tools.
	BuiltinToolsNone BuiltinToolsMode = "none"
	// BuiltinToolsSelected exposes only the built-in tools named in BuiltinToolsConfig.Names.
	BuiltinToolsSelected BuiltinToolsMode = "selected"
)

// BuiltinToolsConfig controls the Falken built-in tools available to a session.
type BuiltinToolsConfig struct {
	Mode  BuiltinToolsMode
	Names []string
}

// BuiltinReadOnlyFileTools names the read-only workspace file built-ins.
var BuiltinReadOnlyFileTools = []string{
	"read_file",
	"read_files",
	"glob",
	"grep",
}

// PolicyConfig configures runtime file/shell/network access decisions.
//
// Policy ownership lives in internal/policy so the runtime engine, persistence,
// and public Falken API all share one serializable model. These public aliases
// preserve the pkg/falken names while avoiding duplicate structs and converters.
type PolicyConfig = policy.Config

// ApprovalHandler is the optional host approval contract for approval-required requests.
type ApprovalHandler interface {
	ApproveFile(context.Context, FileRequest) (ApprovalScope, error)
	ApproveShell(context.Context, ShellRequest) (ApprovalScope, error)
	ApproveNetwork(context.Context, NetworkRequest) (ApprovalScope, error)
}

// FileRule configures file access matching.
type FileRule = policy.FileRule

// ShellRule configures shell access matching.
type ShellRule = policy.ShellRule

// NetworkRule configures network access matching.
type NetworkRule = policy.NetworkRule

// MatchKind controls how a rule compares its target to a request target.
type MatchKind = policy.MatchKind

// ApprovalScope describes how an approval decision should be applied.
type ApprovalScope = policy.ApprovalScope

// FileAccessMode captures the requested file access operation.
type FileAccessMode = policy.FileAccessMode

// ExecutionMode selects the command execution backend for a session.
type ExecutionMode string

// RuntimeProfile is an opaque runtime-provider-owned profile selector. Core
// does not interpret provider-specific values beyond passing them to the
// configured RuntimeProvider.
type RuntimeProfile string

// NetworkProxyMode selects whether Falken starts a session-owned network proxy.
type NetworkProxyMode string

// NetworkEgressMode selects how sandbox container egress is constrained.
type NetworkEgressMode string

// FileRequest is a host approval request for file access.
type FileRequest = policy.FileRequest

// ShellRequest is a host approval request for shell access.
type ShellRequest = policy.ShellRequest

// NetworkRequest is a host approval request for network access.
type NetworkRequest = policy.NetworkRequest

// StateBackend is a generic byte-oriented conversation state backend.
type StateBackend interface {
	Read(key string) ([]byte, bool, error)
	Write(key string, data []byte) error
	Delete(key string) error
}

// StateBackendProvider creates a session-scoped state backend.
type StateBackendProvider interface {
	NewStateBackend(StateBackendRequest) (StateBackend, error)
}

// StateBackendRequest describes the session paths available to a state backend provider.
type StateBackendRequest struct {
	Paths Paths
}

const (
	MatchExact  MatchKind = policy.MatchExact
	MatchPrefix MatchKind = policy.MatchPrefix
	MatchSuffix MatchKind = policy.MatchSuffix

	ApprovalScopeOnce    ApprovalScope = policy.ApprovalScopeOnce
	ApprovalScopeSession ApprovalScope = policy.ApprovalScopeSession
	ApprovalScopeProject ApprovalScope = policy.ApprovalScopeProject
	ApprovalScopeDeny    ApprovalScope = policy.ApprovalScopeDeny

	FileAccessRead   FileAccessMode = policy.FileAccessRead
	FileAccessWrite  FileAccessMode = policy.FileAccessWrite
	FileAccessCreate FileAccessMode = policy.FileAccessCreate

	// ExecutionModeSandbox runs shell commands through the configured runtime provider. It is the default.
	ExecutionModeSandbox ExecutionMode = "sandbox"
	// ExecutionModeLocal runs shell commands on the host and is intended for development/testing.
	ExecutionModeLocal ExecutionMode = "local"

	// NetworkProxyDisabled disables the session-owned network proxy. It is the default.
	NetworkProxyDisabled NetworkProxyMode = "disabled"
	// NetworkProxyHTTP enables an HTTP/CONNECT proxy exposed by the runtime provider.
	NetworkProxyHTTP NetworkProxyMode = "http"

	// NetworkEgressDefault leaves network egress at the runtime provider default.
	NetworkEgressDefault NetworkEgressMode = "default"
	// NetworkEgressProxyOnly asks the runtime provider to route network access
	// through the configured proxy/policy endpoint.
	NetworkEgressProxyOnly NetworkEgressMode = "proxy_only"
)

// Config is the public v1 session assembly contract.
type Config struct {
	// WorkspaceDir is the required workspace root for all workspace-scoped runtime operations.
	WorkspaceDir string
	// StateDir optionally overrides the canonical state root. Empty uses the workspace default.
	StateDir string
	// ToolProviders optionally contribute host-owned tools. Providers are
	// started and used by the session after construction.
	ToolProviders []ToolProvider
	// BuiltinTools optionally selects which Falken built-in tools are exposed.
	// The zero value preserves the default built-in tool set.
	BuiltinTools BuiltinToolsConfig
	// AllowWorkspaceToolsInRemoteMode permits custom provider tools that declare
	// workspace read or mutation capabilities when workspace files are supplied
	// by a remote runtime adapter. The default false prevents tools from
	// accidentally using the agent pod's local filesystem as the workspace.
	AllowWorkspaceToolsInRemoteMode bool
	// HookProviders optionally contribute host-owned session lifecycle hooks.
	HookProviders []HookProvider
	// ExecutionDetails optionally customizes the sandbox command execution backend.
	ExecutionDetails ExecutionConfig
	// LLM is required and provides model completions for the agent runtime.
	LLM LLM
	// VerificationReviewerLLM optionally provides a separate model for command evidence review.
	// Nil defaults to LLM.
	VerificationReviewerLLM LLM
	// BaseSystemPrompt is prepended to Falken's runtime context. Empty is allowed.
	BaseSystemPrompt string
	// PlanRouting controls automatic plan-mode routing. Empty defaults to heuristic routing.
	PlanRouting PlanRoutingMode
	// Events receives session-level runtime events. Nil is a no-op.
	Events EventSink
	// OnCompleted is invoked after normal completion before any request callback. Nil is a no-op.
	OnCompleted CompletionCallback
	// Policy configures file, shell, and network access decisions.
	Policy PolicyConfig
	// ApprovalHandler handles approval-required requests. Nil denies those requests.
	ApprovalHandler ApprovalHandler
	// Runtime optionally provides a custom sandbox/proxy runtime backend.
	// When nil, only explicit local execution mode is allowed. Callers that need
	// sandbox or proxy adapters should inject an external RuntimeProvider.
	Runtime RuntimeProvider
	// StateBackendProvider optionally provides a non-file conversation state backend.
	// Nil uses Falken's default file-backed stores under StateDir.
	StateBackendProvider StateBackendProvider
}

// ExecutionConfig customizes the v1 command executor.
//
// Empty fields use deterministic defaults: SandboxImage is
// "falken-core-runtime:latest", RuntimeBinary is "docker",
// WorkspaceMountPath is "/workspace", ShellPath is "/bin/sh", RuntimeProfile
// is provider-defined, NetworkProxyMode is NetworkProxyDisabled, and
// NetworkEgressMode is NetworkEgressDefault, and Mode is ExecutionModeSandbox.
// NetworkProxyHTTP with NetworkEgressProxyOnly is supported only by
// compatible runtime providers.
// ExecutionModeLocal is intended for development and tests, not as the secure
// public default.
type ExecutionConfig struct {
	Mode ExecutionMode
	// RuntimeProfile selects a runtime-provider-owned profile. Core treats the
	// value as opaque and passes it to the configured RuntimeProvider.
	RuntimeProfile     RuntimeProfile
	SandboxImage       string
	ProxyImage         string
	ProxyPort          int
	RuntimeBinary      string
	WorkspaceMountPath string
	ShellPath          string
	NetworkProxyMode   NetworkProxyMode
	NetworkEgressMode  NetworkEgressMode

	RunnerEndpoint string
	RunnerToken    string
	// RunnerCommandToken optionally authenticates remote command execution.
	// Empty falls back to RunnerToken.
	RunnerCommandToken string
	// RunnerWorkspaceToken optionally authenticates remote workspace file operations.
	// Empty falls back to RunnerToken.
	RunnerWorkspaceToken string
	// RequireSplitRunnerTokens rejects fallback/shared runner token use for
	// production remote-workspace deployments.
	RequireSplitRunnerTokens bool
	// AllowRunnerRestart permits remote runner instance changes during a
	// session. The default false fails closed because runner restarts may lose
	// managed file read-token state.
	AllowRunnerRestart bool
	// RunnerCommandRequestTimeout optionally sets the remote command HTTP
	// request timeout. Zero leaves command cancellation to request contexts.
	RunnerCommandRequestTimeout time.Duration
	// RunnerWorkspaceRequestTimeout sets the remote workspace HTTP request
	// timeout. Zero lets the client use its package default.
	RunnerWorkspaceRequestTimeout time.Duration

	NetworkPolicyListenAddress   string
	NetworkPolicySidecarEndpoint string
	NetworkPolicyToken           string
}

// SessionConfig configures optional session-owned runtime services for the legacy path-based constructor.
//
// Deprecated: use Config with New or NewSessionFromConfig for v1 public runtime assembly.
// This compatibility/testing shape does not validate that an LLM is configured.
type SessionConfig struct {
	ToolProviders                   []ToolProvider
	BuiltinTools                    BuiltinToolsConfig
	AllowWorkspaceToolsInRemoteMode bool
	HookProviders                   []HookProvider
	Execution                       ExecutionConfig
	LLM                             LLM
	VerificationReviewerLLM         LLM
	BaseSystemPrompt                string
	PlanRouting                     PlanRoutingMode
	Events                          EventSink
	OnCompleted                     CompletionCallback
	Policy                          PolicyConfig
	ApprovalHandler                 ApprovalHandler
	// Runtime optionally provides a custom sandbox/proxy runtime backend.
	Runtime RuntimeProvider
	// StateBackendProvider optionally provides a non-file conversation state backend.
	StateBackendProvider StateBackendProvider
}
