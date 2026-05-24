package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/smasonuk/falken-core/pkg/falken"
	"github.com/smasonuk/falken-core/pkg/falken/llm"
)

func TestAdapterSatisfiesFalkenLLM(_ *testing.T) {
	var _ falken.LLM = llm.New(fakeProvider{})
}

func TestAdapterPreservesStreamingProvider(t *testing.T) {
	provider := &streamingProvider{
		chunks: []string{"he", "llo"},
		response: falken.CompletionResponse{
			FinishReason: falken.FinishReasonStop,
		},
	}
	adapter := llm.New(provider)
	var _ falken.StreamingLLM = adapter

	var chunks []string
	response, err := adapter.StreamComplete(context.Background(), falken.CompletionRequest{
		Messages: []falken.Message{{Role: falken.RoleUser, Content: "hi"}},
	}, func(text string) {
		chunks = append(chunks, text)
	})
	if err != nil {
		t.Fatalf("StreamComplete: %v", err)
	}
	if !reflect.DeepEqual(chunks, []string{"he", "llo"}) {
		t.Fatalf("chunks = %v, want streamed provider chunks", chunks)
	}
	if response.FinishReason != falken.FinishReasonStop {
		t.Fatalf("response = %+v", response)
	}
	if len(provider.request.Messages) != 1 || provider.request.Messages[0].Content != "hi" {
		t.Fatalf("provider request = %+v", provider.request)
	}
}

func TestAdapterRoundTripPreservesRequestAndResponse(t *testing.T) {
	provider := &recordingProvider{
		response: falken.CompletionResponse{
			AssistantText: "calling tool",
			ToolCalls: []falken.ToolCall{{
				ID:        "call-2",
				Name:      "write_file",
				Arguments: json.RawMessage(`{"path":"out.txt","content":"ok"}`),
			}},
			FinishReason: falken.FinishReasonToolCalls,
		},
	}
	adapter := llm.New(provider)

	response, err := adapter.Complete(context.Background(), falken.CompletionRequest{
		Messages: []falken.Message{
			{Role: falken.RoleSystem, Content: "system"},
			{
				Role: falken.RoleAssistant,
				ToolCalls: []falken.ToolCall{{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"notes.txt"}`),
				}},
			},
			{
				Role: falken.RoleTool,
				ToolResult: &falken.ToolResult{
					CallID:  "call-1",
					Name:    "read_file",
					Content: "notes",
					Payload: json.RawMessage(`{"status":"ok"}`),
				},
			},
		},
		Tools: []falken.ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			Parameters:  json.RawMessage(`{"type":"object","required":["path"]}`),
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(provider.request.Messages) != 3 {
		t.Fatalf("provider messages = %+v, want 3", provider.request.Messages)
	}
	assistant := provider.request.Messages[1]
	if assistant.Role != falken.RoleAssistant || len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant message = %+v, want tool call", assistant)
	}
	if assistant.ToolCalls[0].ID != "call-1" || assistant.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool call = %+v, want preserved id/name", assistant.ToolCalls[0])
	}
	if !jsonEqual(t, assistant.ToolCalls[0].Arguments, `{"path":"notes.txt"}`) {
		t.Fatalf("tool call arguments = %s", string(assistant.ToolCalls[0].Arguments))
	}
	toolMessage := provider.request.Messages[2]
	if toolMessage.ToolResult == nil || toolMessage.ToolResult.CallID != "call-1" || toolMessage.ToolResult.Name != "read_file" {
		t.Fatalf("tool result message = %+v, want preserved call id/name", toolMessage)
	}
	if len(provider.request.Tools) != 1 || provider.request.Tools[0].Name != "read_file" {
		t.Fatalf("provider tools = %+v, want read_file", provider.request.Tools)
	}
	if !jsonEqual(t, provider.request.Tools[0].Parameters, `{"type":"object","required":["path"]}`) {
		t.Fatalf("tool schema = %s", string(provider.request.Tools[0].Parameters))
	}

	if response.AssistantText != "calling tool" || response.FinishReason != falken.FinishReasonToolCalls {
		t.Fatalf("response = %+v, want tool-call response", response)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].ID != "call-2" || response.ToolCalls[0].Name != "write_file" {
		t.Fatalf("response tool calls = %+v, want preserved id/name", response.ToolCalls)
	}
	if !jsonEqual(t, response.ToolCalls[0].Arguments, `{"path":"out.txt","content":"ok"}`) {
		t.Fatalf("response arguments = %s", string(response.ToolCalls[0].Arguments))
	}
}

func TestAdapterReportsMissingProvider(t *testing.T) {
	_, err := (&llm.Adapter{}).Complete(context.Background(), falken.CompletionRequest{})
	if !errors.Is(err, llm.ErrProviderRequired) {
		t.Fatalf("Complete error = %v, want ErrProviderRequired", err)
	}
}

type fakeProvider struct{}

func (fakeProvider) Complete(context.Context, falken.CompletionRequest) (falken.CompletionResponse, error) {
	return falken.CompletionResponse{}, nil
}

type recordingProvider struct {
	request  falken.CompletionRequest
	response falken.CompletionResponse
}

func (p *recordingProvider) Complete(_ context.Context, request falken.CompletionRequest) (falken.CompletionResponse, error) {
	p.request = request
	return p.response, nil
}

type streamingProvider struct {
	request  falken.CompletionRequest
	chunks   []string
	response falken.CompletionResponse
}

func (p *streamingProvider) Complete(context.Context, falken.CompletionRequest) (falken.CompletionResponse, error) {
	panic("Complete should not be called for streamingProvider")
}

func (p *streamingProvider) StreamComplete(_ context.Context, request falken.CompletionRequest, sink falken.AssistantTextSink) (falken.CompletionResponse, error) {
	p.request = request
	for _, chunk := range p.chunks {
		if sink != nil {
			sink(chunk)
		}
	}
	return p.response, nil
}

func jsonEqual(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON %q: %v", string(got), err)
	}
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want JSON %q: %v", want, err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}
