package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
)

func TestMessageModelConstructors(t *testing.T) {
	system := agent.SystemMessage("system")
	if system.Role != agent.RoleSystem || system.Content != "system" {
		t.Fatalf("system message = %+v", system)
	}

	user := agent.UserMessage("hello")
	if user.Role != agent.RoleUser || user.Content != "hello" {
		t.Fatalf("user message = %+v", user)
	}

	call := agent.ToolCall{
		ID:        "call-1",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"README.md"}`),
	}
	assistant := agent.AssistantMessage("I will check.", call)
	if assistant.Role != agent.RoleAssistant || assistant.Content != "I will check." || len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant message = %+v", assistant)
	}
	if assistant.ToolCalls[0].ID != "call-1" || assistant.ToolCalls[0].Name != "read_file" {
		t.Fatalf("assistant tool call = %+v", assistant.ToolCalls[0])
	}

	call.Arguments[0] = '!'
	if string(assistant.ToolCalls[0].Arguments) != `{"path":"README.md"}` {
		t.Fatalf("assistant tool calls were not defensively cloned: %s", assistant.ToolCalls[0].Arguments)
	}

	result := agent.ToolResult{
		CallID:  "call-1",
		Name:    "read_file",
		Content: "contents",
		Payload: json.RawMessage(`{"bytes":8}`),
	}
	message := agent.ToolResultMessage(result)
	if message.Role != agent.RoleTool || message.Content != "contents" || message.ToolResult == nil {
		t.Fatalf("tool result message = %+v", message)
	}
	if message.ToolResult.CallID != "call-1" || message.ToolResult.Name != "read_file" {
		t.Fatalf("tool result payload = %+v", message.ToolResult)
	}
}

func TestToolDefinitionFromActiveToolMetadata(t *testing.T) {
	entry := tools.Entry{
		Name:        "read_note",
		Description: "Read a note.",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}

	definition := agent.ToolDefinitionFromEntry(entry)
	if definition.Name != entry.Name || definition.Description != entry.Description {
		t.Fatalf("definition = %+v, want name/description preserved", definition)
	}
	if string(definition.Parameters) != string(entry.InputSchema) {
		t.Fatalf("parameters = %s, want %s", definition.Parameters, entry.InputSchema)
	}

	entry.InputSchema[0] = '!'
	if string(definition.Parameters) != `{"type":"object","properties":{"path":{"type":"string"}}}` {
		t.Fatalf("definition parameters were not defensively cloned: %s", definition.Parameters)
	}

	definitions := agent.ToolDefinitionsFromEntries([]tools.Entry{entry})
	if len(definitions) != 1 || definitions[0].Name != "read_note" {
		t.Fatalf("definitions = %+v, want mapped active tool definition", definitions)
	}
}

func TestEventModelConstructors(t *testing.T) {
	if event := agent.AssistantTextEvent("hello"); event.Type != agent.EventAssistantText || event.Text != "hello" {
		t.Fatalf("assistant text event = %+v", event)
	}
	if event := agent.ThoughtEvent("maybe"); event.Type != agent.EventThought || event.Text != "maybe" {
		t.Fatalf("thought event = %+v", event)
	}

	call := agent.ToolCall{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a"}`)}
	callEvent := agent.ToolCallEvent(call)
	if callEvent.Type != agent.EventToolCall || callEvent.ToolCall == nil || callEvent.ToolCall.ID != "call-1" {
		t.Fatalf("tool call event = %+v", callEvent)
	}

	result := agent.ToolResult{CallID: "call-1", Name: "read_file", Payload: json.RawMessage(`{"ok":true}`)}
	resultEvent := agent.ToolResultEvent(result)
	if resultEvent.Type != agent.EventToolResult || resultEvent.ToolResult == nil || resultEvent.ToolResult.Name != "read_file" {
		t.Fatalf("tool result event = %+v", resultEvent)
	}

	chunkData := []byte("chunk")
	chunkEvent := agent.CommandChunkEvent(runtimeexec.StreamChunk{
		Stream: runtimeexec.StreamStdout,
		Data:   chunkData,
	})
	if chunkEvent.Type != agent.EventCommandChunk || chunkEvent.CommandChunk == nil || string(chunkEvent.CommandChunk.Data) != "chunk" {
		t.Fatalf("command chunk event = %+v", chunkEvent)
	}
	chunkData[0] = '!'
	if string(chunkEvent.CommandChunk.Data) != "chunk" {
		t.Fatalf("command chunk data was not defensively cloned: %q", chunkEvent.CommandChunk.Data)
	}

	completed := agent.RunCompletedEvent(agent.RunResult{FinalOutput: "done"})
	if completed.Type != agent.EventRunCompleted || completed.RunResult == nil || completed.RunResult.FinalOutput != "done" {
		t.Fatalf("run completed event = %+v", completed)
	}

	failed := agent.RunFailedEvent(errors.New("boom"))
	if failed.Type != agent.EventRunFailed || failed.Error != "boom" {
		t.Fatalf("run failed event = %+v", failed)
	}
}

func TestLLMInterfaceAndModelResponse(t *testing.T) {
	llm := fakeLLM{
		response: agent.CompletionResponse{
			AssistantText: "I can do that.",
			ToolCalls: []agent.ToolCall{{
				ID:        "call-1",
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			}},
			FinishReason: agent.FinishReasonToolCalls,
		},
	}

	var provider agent.LLM = llm
	response, err := provider.Complete(context.Background(), agent.CompletionRequest{
		Messages: []agent.Message{agent.UserMessage("read README")},
		Tools: []agent.ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if response.AssistantText != "I can do that." || response.FinishReason != agent.FinishReasonToolCalls {
		t.Fatalf("response = %+v", response)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool calls = %+v, want read_file call", response.ToolCalls)
	}

	textOnly := agent.CompletionResponse{
		AssistantText: "done",
		FinishReason:  agent.FinishReasonStop,
	}
	if textOnly.AssistantText != "done" || len(textOnly.ToolCalls) != 0 {
		t.Fatalf("text-only response = %+v", textOnly)
	}
}

func TestJSONRoundTripPreservesBasicShapes(t *testing.T) {
	message := agent.AssistantMessage("using tool", agent.ToolCall{
		ID:        "call-1",
		Name:      "search",
		Arguments: json.RawMessage(`{"query":"falken"}`),
	})

	data, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal message: %v", err)
	}
	var decoded agent.Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal message: %v", err)
	}
	if !reflect.DeepEqual(decoded, message) {
		t.Fatalf("decoded message = %+v, want %+v", decoded, message)
	}
}

type fakeLLM struct {
	response agent.CompletionResponse
	err      error
}

func (f fakeLLM) Complete(context.Context, agent.CompletionRequest) (agent.CompletionResponse, error) {
	return f.response, f.err
}
