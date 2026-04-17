package falken

import "github.com/smasonuk/falken-core/internal/runtimeapi"

// PermissionRequest describes a host-driven permission decision request.
type PermissionRequest = runtimeapi.PermissionRequest

// PermissionResponse describes a host-provided permission decision.
type PermissionResponse = runtimeapi.PermissionResponse

// PlanApprovalRequest describes a request to approve or reject a plan.
type PlanApprovalRequest = runtimeapi.PlanApprovalRequest

// PlanApprovalResponse describes the host's response to a plan approval request.
type PlanApprovalResponse = runtimeapi.PlanApprovalResponse

// SubmitRequest describes a submit-for-review handoff from the runtime.
type SubmitRequest = runtimeapi.SubmitRequest

// InteractionHandler handles human-in-the-loop runtime interactions.
type InteractionHandler = runtimeapi.InteractionHandler

// Event is a typed runtime event emitted by a session.
type Event = runtimeapi.Event

// EventType identifies the kind of runtime event.
type EventType = runtimeapi.EventType

const (
	// EventTypeThought indicates a reasoning/thought event.
	EventTypeThought = runtimeapi.EventTypeThought
	// EventTypeAssistantText indicates assistant-facing text output.
	EventTypeAssistantText = runtimeapi.EventTypeAssistantText
	// EventTypeToolCall indicates that the runtime is invoking a tool.
	EventTypeToolCall = runtimeapi.EventTypeToolCall
	// EventTypeToolResult indicates that a tool invocation has completed.
	EventTypeToolResult = runtimeapi.EventTypeToolResult
	// EventTypeCommandChunk indicates streamed command output.
	EventTypeCommandChunk = runtimeapi.EventTypeCommandChunk
	// EventTypeWorkSubmitted indicates the runtime has submitted work for review.
	EventTypeWorkSubmitted = runtimeapi.EventTypeWorkSubmitted
	// EventTypeRunCompleted indicates a run completed successfully.
	EventTypeRunCompleted = runtimeapi.EventTypeRunCompleted
	// EventTypeRunFailed indicates a run finished with an error.
	EventTypeRunFailed = runtimeapi.EventTypeRunFailed
)

// ThoughtEvent carries a reasoning/thought payload.
type ThoughtEvent = runtimeapi.ThoughtEvent

// AssistantTextEvent carries assistant text output.
type AssistantTextEvent = runtimeapi.AssistantTextEvent

// ToolCallEvent carries a tool invocation payload.
type ToolCallEvent = runtimeapi.ToolCallEvent

// ToolResultEvent carries a tool result payload.
type ToolResultEvent = runtimeapi.ToolResultEvent

// CommandChunkEvent carries streamed command output.
type CommandChunkEvent = runtimeapi.CommandChunkEvent

// WorkSubmittedEvent carries submit-for-review metadata.
type WorkSubmittedEvent = runtimeapi.WorkSubmittedEvent

// RunCompletedEvent marks successful run completion.
type RunCompletedEvent = runtimeapi.RunCompletedEvent

// RunFailedEvent carries the terminal run error.
type RunFailedEvent = runtimeapi.RunFailedEvent

// EventHandler receives runtime events emitted by a session.
type EventHandler = runtimeapi.EventHandler

// EventHandlerFunc adapts a function to the EventHandler interface.
type EventHandlerFunc = runtimeapi.EventHandlerFunc

// ToolInfo describes a loaded tool.
type ToolInfo struct {
	Name string
}

// DiffApplyResult describes the outcome of applying reviewed sandbox changes.
type DiffApplyResult struct {
	Partial      bool
	SkippedFiles []string
}

// PluginInfo describes a discovered plugin and its declared permissions.
type PluginInfo struct {
	Name            string
	Description     string
	Internal        bool
	NetworkTargets  []string
	ShellCommands   []string
	FilePermissions []string
}
