package agent

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
)

// Role identifies the author/type of a conversation message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is the provider-neutral conversation message shape used by the agent runtime.
type Message struct {
	Role       Role        `json:"role"`
	Content    string      `json:"content,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
}

// SystemMessage constructs a system conversation message.
func SystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: content}
}

// UserMessage constructs a user conversation message.
func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

// AssistantMessage constructs an assistant conversation message.
func AssistantMessage(content string, toolCalls ...ToolCall) Message {
	return Message{
		Role:      RoleAssistant,
		Content:   content,
		ToolCalls: cloneToolCalls(toolCalls),
	}
}

// ToolResultMessage constructs a tool-result conversation message.
func ToolResultMessage(result ToolResult) Message {
	cloned := cloneToolResult(result)
	return Message{
		Role:       RoleTool,
		Content:    result.Content,
		ToolResult: &cloned,
	}
}

// ToolDefinition is the LLM-facing description of one callable runtime tool.
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

// ToolDefinitionFromEntry maps active tool metadata into the agent runtime tool model.
func ToolDefinitionFromEntry(entry tools.Entry) ToolDefinition {
	return ToolDefinition{
		Name:        entry.Name,
		Description: entry.Description,
		Parameters:  append(json.RawMessage(nil), entry.InputSchema...),
	}
}

// ToolDefinitionsFromEntries maps active tool metadata into deterministic LLM-facing tool definitions.
func ToolDefinitionsFromEntries(entries []tools.Entry) []ToolDefinition {
	definitions := make([]ToolDefinition, 0, len(entries))
	for _, entry := range entries {
		definitions = append(definitions, ToolDefinitionFromEntry(entry))
	}
	return definitions
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

// EventType identifies stable host-facing runtime events.
type EventType string

const (
	EventAssistantText EventType = "assistant_text"
	EventToolCall      EventType = "tool_call"
	EventToolResult    EventType = "tool_result"
	EventCommandChunk  EventType = "command_chunk"
	EventPlanRouting   EventType = "plan_routing_decision"
	EventRunCompleted  EventType = "run_completed"
	EventRunFailed     EventType = "run_failed"
	// EventThought is best-effort only; hosts must not depend on it for correctness.
	EventThought EventType = "thought"
)

// Event is the explicit runtime event shape emitted to host integrations.
type Event struct {
	Type         EventType                 `json:"type"`
	Text         string                    `json:"text,omitempty"`
	ToolCall     *ToolCall                 `json:"tool_call,omitempty"`
	ToolResult   *ToolResult               `json:"tool_result,omitempty"`
	CommandChunk *runtimeexec.StreamChunk  `json:"command_chunk,omitempty"`
	PlanRouting  *PlanRoutingDecisionEvent `json:"plan_routing_decision,omitempty"`
	RunResult    *RunResult                `json:"run_result,omitempty"`
	Error        string                    `json:"error,omitempty"`
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

// EventSink receives runtime events as they happen.
type EventSink func(Event)

// AssistantTextSink receives incremental assistant text from streaming LLMs.
type AssistantTextSink func(string)

// CompletionCallback is an optional host notification invoked after normal run completion.
type CompletionCallback func(context.Context, RunResult) error

// AssistantTextEvent constructs an assistant text event.
func AssistantTextEvent(text string) Event {
	return Event{Type: EventAssistantText, Text: text}
}

// ThoughtEvent constructs a best-effort thought event.
func ThoughtEvent(text string) Event {
	return Event{Type: EventThought, Text: text}
}

// PlanRoutingEvent constructs an internal routing observability event.
func PlanRoutingEvent(decision PlanRoutingDecision, source string) Event {
	payload := PlanRoutingDecisionEvent{
		RequiresPlan:  decision.RequiresPlan,
		RequiresTodos: decision.RequiresTodos,
		Reason:        decision.Reason,
		Confidence:    decision.Confidence,
		Signals:       append([]string(nil), decision.Signals...),
		Source:        source,
	}
	return Event{Type: EventPlanRouting, PlanRouting: &payload}
}

// ToolCallEvent constructs a tool-call event.
func ToolCallEvent(call ToolCall) Event {
	cloned := cloneToolCall(call)
	return Event{Type: EventToolCall, ToolCall: &cloned}
}

// ToolResultEvent constructs a tool-result event.
func ToolResultEvent(result ToolResult) Event {
	cloned := cloneToolResult(result)
	return Event{Type: EventToolResult, ToolResult: &cloned}
}

// CommandChunkEvent constructs a command-output chunk event.
func CommandChunkEvent(chunk runtimeexec.StreamChunk) Event {
	cloned := runtimeexec.StreamChunk{
		Stream: chunk.Stream,
		Data:   append([]byte(nil), chunk.Data...),
	}
	return Event{Type: EventCommandChunk, CommandChunk: &cloned}
}

// RunCompletedEvent constructs a run-completed event.
func RunCompletedEvent(result RunResult) Event {
	cloned := cloneRunResult(result)
	return Event{Type: EventRunCompleted, RunResult: &cloned}
}

// RunFailedEvent constructs a run-failed event.
func RunFailedEvent(err error) Event {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return Event{Type: EventRunFailed, Error: message}
}

// RunRequest is the high-level agent run input contract.
type RunRequest struct {
	Prompt      string
	Events      EventSink
	OnCompleted CompletionCallback
}

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

// CompletionRequest is the minimal provider-neutral LLM request.
type CompletionRequest struct {
	Messages   []Message
	Tools      []ToolDefinition
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
}

// CompletionResponse is the minimal provider-neutral LLM response.
type CompletionResponse struct {
	AssistantText string
	ToolCalls     []ToolCall
	FinishReason  FinishReason
}

// LLM is the minimal test-friendly interface for later agent orchestration.
type LLM interface {
	Complete(context.Context, CompletionRequest) (CompletionResponse, error)
}

// StreamingLLM is an optional extension for providers that can stream assistant text.
//
// The final CompletionResponse remains authoritative for persisted assistant
// messages and tool calls; streamed chunks are emitted as best-effort runtime
// events only.
type StreamingLLM interface {
	LLM
	StreamComplete(context.Context, CompletionRequest, AssistantTextSink) (CompletionResponse, error)
}

func cloneToolCalls(calls []ToolCall) []ToolCall {
	cloned := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		cloned = append(cloned, cloneToolCall(call))
	}
	return cloned
}

func cloneToolCall(call ToolCall) ToolCall {
	call.Arguments = append(json.RawMessage(nil), call.Arguments...)
	return call
}

func cloneToolResult(result ToolResult) ToolResult {
	result.Payload = append(json.RawMessage(nil), result.Payload...)
	return result
}

func cloneRunResult(result RunResult) RunResult {
	return result
}
