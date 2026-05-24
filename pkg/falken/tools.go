package falken

import (
	"context"
	"encoding/json"
)

// HookEvent identifies a session lifecycle or runtime hook point.
type HookEvent string

const (
	// HookSessionStart runs after session resources start successfully.
	HookSessionStart HookEvent = "session_start"
	// HookSessionClose runs before session resources are closed.
	HookSessionClose HookEvent = "session_close"
)

// ToolDescriptor describes one tool exposed by a ToolProvider.
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`

	Category    string   `json:"category,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	AlwaysLoad  bool     `json:"always_load,omitempty"`
	DefaultLoad bool     `json:"default_load,omitempty"`

	Safety ToolSafety `json:"safety,omitempty"`
}

// ToolSafety describes high-level capabilities required by a tool.
type ToolSafety struct {
	PlanSafe         bool `json:"plan_safe,omitempty"`
	ReadsWorkspace   bool `json:"reads_workspace,omitempty"`
	MutatesWorkspace bool `json:"mutates_workspace,omitempty"`
	ExecutesShell    bool `json:"executes_shell,omitempty"`
	UsesNetwork      bool `json:"uses_network,omitempty"`
	ReadsHostState   bool `json:"reads_host_state,omitempty"`
	MutatesHostState bool `json:"mutates_host_state,omitempty"`
	UsesHostState    bool `json:"uses_host_state,omitempty"`
}

// StartupCapabilityProvider lets providers declare the ToolHost capabilities
// they need during Start. Providers that do not implement this interface get a
// startup host with no file, shell, or host-state capabilities.
type StartupCapabilityProvider interface {
	StartupSafety() ToolSafety
}

// ToolInvocation describes one provider-owned tool call.
type ToolInvocation struct {
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolExecutionResult is the public result shape returned by ToolProvider implementations.
type ToolExecutionResult struct {
	Success bool            `json:"success"`
	Status  string          `json:"status,omitempty"`
	Content string          `json:"content,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// ToolCommandRequest asks the host to execute a policy-gated shell command.
type ToolCommandRequest struct {
	Command          string
	WorkingDir       string
	Env              map[string]string
	ApprovalRequired bool
	MaxInlineOutput  int
}

// ToolCommandResult is the public-safe result of a host command execution.
type ToolCommandResult struct {
	Success           bool
	Status            string
	Executed          bool
	Command           string
	WorkingDir        string
	Stdout            string
	Stderr            string
	CombinedOutput    string
	ExitCode          int
	PolicyOutcome     string
	PolicyExplanation string
	Output            ToolCommandOutputSummary
	StartError        string
	ExecutionError    string
	ExitError         string
	CleanupError      string
}

// ToolCommandOutputSummary describes command output truncation for host tools.
type ToolCommandOutputSummary struct {
	Truncated     bool
	ArtifactPath  string
	InlineLimit   int
	OriginalBytes int
	PreviewBytes  int
}

// ToolFileAccessRequest asks the host to evaluate file access policy.
type ToolFileAccessRequest struct {
	Path             string
	Mode             FileAccessMode
	ApprovalRequired bool
}

// ToolFileAccessResult is a public-safe file access policy decision.
type ToolFileAccessResult struct {
	Allowed     bool
	Explanation string
}

// ToolStateGetRequest reads host-managed state scoped to the provider or tool
// receiving the ToolHost. Callers choose only the key, not the state namespace.
type ToolStateGetRequest struct {
	Key string
}

// ToolStateGetResult is a plugin/tool-scoped host state read response.
type ToolStateGetResult struct {
	Value string
	Found bool
}

// ToolStateSetRequest writes host-managed state scoped to the provider or tool
// receiving the ToolHost. Callers choose only the key, not the state namespace.
type ToolStateSetRequest struct {
	Key   string
	Value string
}

// ToolStateSetResult is a plugin/tool-scoped host state write response.
type ToolStateSetResult struct {
	Path string
}

// Tool is a native Go tool implementation that can be exposed through a
// ToolProvider.
type Tool interface {
	Descriptor() ToolDescriptor
	Execute(context.Context, ToolInvocation) (ToolExecutionResult, error)
}

// ToolHost exposes session context to provider-owned tools.
type ToolHost interface {
	WorkspaceRoot() string
	StateRoot() string
	CurrentWorkingDir() string
	Emit(Event)

	CheckFileAccess(context.Context, ToolFileAccessRequest) (ToolFileAccessResult, error)
	ExecuteCommand(context.Context, ToolCommandRequest) (ToolCommandResult, error)
	GetState(context.Context, ToolStateGetRequest) (ToolStateGetResult, error)
	SetState(context.Context, ToolStateSetRequest) (ToolStateSetResult, error)
}

// ToolProvider contributes host-owned tools to a Falken session.
type ToolProvider interface {
	Start(context.Context, ToolHost) error
	Tools(context.Context) ([]ToolDescriptor, error)
	ExecuteTool(context.Context, ToolInvocation) (ToolExecutionResult, error)
	Close(context.Context) error
}

// ScopedToolProvider is an optional ToolProvider extension for providers that
// want a per-tool, capability-scoped host during execution. Core passes a
// ToolHost whose command, file, and state services are limited by the invoked
// tool's ToolSafety metadata.
type ScopedToolProvider interface {
	ExecuteToolWithHost(context.Context, ToolInvocation, ToolHost) (ToolExecutionResult, error)
}

// HookDescriptor describes one hook exposed by a HookProvider.
type HookDescriptor struct {
	Name        string
	Event       HookEvent
	Description string
}

// HookInvocation describes one hook execution request.
type HookInvocation struct {
	Event     HookEvent
	Name      string
	Arguments json.RawMessage
}

// HookResult is the public result shape returned by HookProvider implementations.
type HookResult struct {
	Success bool
	Status  string
	Payload json.RawMessage
	Error   string
}

// HookProvider contributes host-owned lifecycle/runtime hooks to a Falken session.
type HookProvider interface {
	Start(context.Context, ToolHost) error
	Hooks(context.Context) ([]HookDescriptor, error)
	RunHook(context.Context, HookInvocation) (HookResult, error)
	Close(context.Context) error
}
