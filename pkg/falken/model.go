package falken

import (
	"context"
	"encoding/json"
)

// LLM is the host-provided model interface used by the v1 agent runtime.
type LLM interface {
	Complete(context.Context, CompletionRequest) (CompletionResponse, error)
}

// CompletionRequest is the provider-neutral prompt/tool payload sent to an LLM.
type CompletionRequest struct {
	Messages   []Message
	Tools      []ToolDefinition
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
}

// CompletionResponse is the provider-neutral response returned by an LLM.
type CompletionResponse struct {
	AssistantText string
	ToolCalls     []ToolCall
	FinishReason  FinishReason
}

// Message is the provider-neutral conversation message shape used by the agent runtime.
type Message struct {
	Role       Role        `json:"role"`
	Content    string      `json:"content,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
}

// Role identifies the author/type of a conversation message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolDefinition is the LLM-facing description of an active runtime tool.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolChoice controls whether and how a model should call tools.
type ToolChoice struct {
	Type string `json:"type"` // "auto" | "required" | "none" | "tool"
	Name string `json:"name,omitempty"`
}

// ToolCall is a model-requested tool invocation.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolResult is the payload returned to the model for a prior tool call.
type ToolResult struct {
	CallID  string          `json:"call_id"`
	Name    string          `json:"name"`
	Content string          `json:"content,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// Event is the stable host-facing runtime event shape.
type Event struct {
	Type               EventType                 `json:"type"`
	Text               string                    `json:"text,omitempty"`
	ToolCall           *ToolCall                 `json:"tool_call,omitempty"`
	ToolResult         *ToolResult               `json:"tool_result,omitempty"`
	CommandChunk       *CommandChunk             `json:"command_chunk,omitempty"`
	NetworkRequest     *NetworkRequestEvent      `json:"network_request,omitempty"`
	WorkspaceOperation *WorkspaceOperationEvent  `json:"workspace_operation,omitempty"`
	PlanRouting        *PlanRoutingDecisionEvent `json:"plan_routing_decision,omitempty"`
	RunResult          *RunResult                `json:"run_result,omitempty"`
	Error              string                    `json:"error,omitempty"`
}

// PlanRoutingDecisionEvent is a compact routing observability payload.
type PlanRoutingDecisionEvent struct {
	RequiresPlan  bool     `json:"requires_plan"`
	RequiresTodos bool     `json:"requires_todos"`
	Reason        string   `json:"reason"`
	Confidence    string   `json:"confidence"`
	Signals       []string `json:"signals,omitempty"`
	Source        string   `json:"source"`
}

// WorkspaceOperationEvent reports managed workspace file operation metadata
// without including file contents, patches, or edit strings.
type WorkspaceOperationEvent struct {
	Operation string   `json:"operation"`
	Paths     []string `json:"paths,omitempty"`
	Mutated   bool     `json:"mutated,omitempty"`
	Success   bool     `json:"success"`
	Status    string   `json:"status,omitempty"`
	Remote    bool     `json:"remote,omitempty"`
}

// EventType identifies stable host-facing runtime event kinds.
type EventType string

const (
	EventAssistantText      EventType = "assistant_text"
	EventToolCall           EventType = "tool_call"
	EventToolResult         EventType = "tool_result"
	EventCommandChunk       EventType = "command_chunk"
	EventNetworkRequest     EventType = "network_request"
	EventWorkspaceOperation EventType = "workspace_operation"
	EventPlanRouting        EventType = "plan_routing_decision"
	EventRunCompleted       EventType = "run_completed"
	EventRunFailed          EventType = "run_failed"
	// EventThought is best-effort only; hosts must not depend on it for correctness.
	EventThought EventType = "thought"
)

// EventSink receives runtime events as they happen.
type EventSink func(Event)

// AssistantTextSink receives incremental assistant text from streaming LLMs.
type AssistantTextSink func(string)

// CompletionCallback is an optional host notification invoked after normal run completion.
// When both Config and RunRequest callbacks are set, the config callback runs first.
type CompletionCallback func(context.Context, RunResult) error

// RunResult is the high-level agent run result contract.
type RunResult struct {
	FinalOutput string
	Completed   bool
	Error       string
}

// FinishReason captures why an LLM response completed.
type FinishReason string

const (
	FinishReasonStop      FinishReason = "stop"
	FinishReasonToolCalls FinishReason = "tool_calls"
	FinishReasonLength    FinishReason = "length"
	FinishReasonError     FinishReason = "error"
)

// CommandChunk is a command-output event chunk exposed to hosts.
type CommandChunk struct {
	Stream string `json:"stream"`
	Data   []byte `json:"data,omitempty"`
}

// NetworkRequestEvent describes a policy decision for proxy-mediated network traffic.
type NetworkRequestEvent struct {
	Host       string `json:"host"`
	Port       int    `json:"port,omitempty"`
	Method     string `json:"method,omitempty"`
	Target     string `json:"target,omitempty"`
	Allowed    bool   `json:"allowed"`
	Reason     string `json:"reason,omitempty"`
	Proxy      string `json:"proxy,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Command    string `json:"command,omitempty"`
}

// StreamingLLM is an optional extension for providers that can stream assistant text.
//
// Implementing this interface is not required. The final CompletionResponse is
// still used for history persistence and tool calls; streamed chunks are emitted
// as EventAssistantText events while the provider is generating.
type StreamingLLM interface {
	LLM
	StreamComplete(context.Context, CompletionRequest, AssistantTextSink) (CompletionResponse, error)
}

// RunRequest captures the minimal top-level input shape for the public session runtime.
type RunRequest struct {
	Prompt string
	Events EventSink
	// OnCompleted is invoked after normal completion, after the config callback if one is set.
	OnCompleted CompletionCallback
}
