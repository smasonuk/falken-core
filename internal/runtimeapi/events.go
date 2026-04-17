package runtimeapi

// EventType identifies the payload variant populated on an Event.
type EventType string

const (
	// EventTypeThought reports non-user-facing reasoning text.
	EventTypeThought EventType = "thought"
	// EventTypeAssistantText reports streamed assistant output intended for display.
	EventTypeAssistantText EventType = "assistant_text"
	// EventTypeToolCall reports a tool invocation before it executes.
	EventTypeToolCall EventType = "tool_call"
	// EventTypeToolResult reports the structured result returned by a tool call.
	EventTypeToolResult EventType = "tool_result"
	// EventTypeCommandChunk reports incremental command output from the sandbox.
	EventTypeCommandChunk EventType = "command_chunk"
	// EventTypeWorkSubmitted reports that the run handed work back for review.
	EventTypeWorkSubmitted EventType = "work_submitted"
	// EventTypeRunCompleted reports normal run completion.
	EventTypeRunCompleted EventType = "run_completed"
	// EventTypeRunFailed reports terminal run failure.
	EventTypeRunFailed EventType = "run_failed"
)

// Event is the tagged union used to deliver runtime activity to host code.
// Exactly one payload field should be populated for a given Type.
type Event struct {
	Type          EventType
	Thought       *ThoughtEvent
	AssistantText *AssistantTextEvent
	ToolCall      *ToolCallEvent
	ToolResult    *ToolResultEvent
	CommandChunk  *CommandChunkEvent
	WorkSubmitted *WorkSubmittedEvent
	RunCompleted  *RunCompletedEvent
	RunFailed     *RunFailedEvent
}

// ThoughtEvent carries internal reasoning text that frontends may choose to hide.
type ThoughtEvent struct {
	Text string
}

// AssistantTextEvent carries user-visible assistant output.
type AssistantTextEvent struct {
	Text string
}

// ToolCallEvent describes a tool invocation about to be executed.
type ToolCallEvent struct {
	Name string
	Args map[string]any
}

// ToolResultEvent carries the structured output returned by a tool invocation.
type ToolResultEvent struct {
	Name   string
	Result map[string]any
}

// CommandChunkEvent carries a streamed fragment of command output.
type CommandChunkEvent struct {
	Chunk string
}

// WorkSubmittedEvent summarizes the work that was submitted for review.
type WorkSubmittedEvent struct {
	Summary string
}

// RunCompletedEvent marks a successful terminal run state.
type RunCompletedEvent struct{}

// RunFailedEvent carries the error that terminated a run.
type RunFailedEvent struct {
	Error error
}

// EventHandler consumes runtime events emitted during a session run.
type EventHandler interface {
	OnEvent(Event)
}

// EventHandlerFunc adapts a plain function to the EventHandler interface.
type EventHandlerFunc func(Event)

func (f EventHandlerFunc) OnEvent(event Event) {
	if f != nil {
		f(event)
	}
}
