package falken

import (
	"context"

	falkenruntime "github.com/smasonuk/falken-core/pkg/falken/runtime"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// RuntimeProvider supplies optional runtime adapters to the core session.
// Core owns policy, execution state, command/file operations, and lifecycle
// assembly; providers own adapter implementations such as sandbox runtimes or
// network policy endpoints.
type RuntimeProvider interface {
	NewRuntimeAdapters(context.Context, RuntimeAdapterRequest) (RuntimeAdapters, error)
}

// RuntimeAdapterRequest describes the public information an adapter needs to
// build sandbox and network runtime handles.
type RuntimeAdapterRequest struct {
	WorkspaceRoot string
	StateRoot     string
	Execution     ExecutionConfig
	Events        EventSink
	NetworkPolicy NetworkPolicyEvaluator
}

// RuntimeAdapters holds adapter-owned runtime handles returned by a provider.
type RuntimeAdapters struct {
	SandboxRuntime falkenruntime.SandboxRuntime
	WorkspaceFiles workspacefiles.Operations
	NetworkProxy   NetworkProxyHandle
	NetworkPolicy  NetworkPolicyHandle
}

// SandboxRuntimeHandle is the lifecycle and execution interface for sandbox
// adapters.
type SandboxRuntimeHandle = falkenruntime.SandboxRuntime

// NetworkProxyHandle is the lifecycle interface for the session network proxy.
type NetworkProxyHandle interface {
	Start(context.Context) error
	Close(context.Context) error
	Endpoint() ProxyEndpoint
}

// NetworkPolicyHandle is the lifecycle interface for the proxy policy endpoint.
type NetworkPolicyHandle interface {
	Start(context.Context) error
	Close(context.Context) error
}

// ProxyEndpoint holds proxy environment values that may be injected into runtimes.
type ProxyEndpoint struct {
	HTTPProxyURL  string
	HTTPSProxyURL string
	NoProxy       string
}

// NetworkPolicyEvaluator is the public network policy surface used by optional
// proxy adapters.
type NetworkPolicyEvaluator interface {
	EvaluateNetworkPolicy(context.Context, NetworkPolicyEvaluationRequest) (NetworkPolicyEvaluationResult, error)
}

// NetworkPolicyEvaluationRequest asks whether a runtime may access a network target.
type NetworkPolicyEvaluationRequest struct {
	Host             string
	Port             int
	Method           string
	Target           string
	ApprovalRequired bool
}

// NetworkPolicyEvaluationResult is the adapter-facing network policy decision.
type NetworkPolicyEvaluationResult struct {
	Allowed     bool
	Explanation string
}
