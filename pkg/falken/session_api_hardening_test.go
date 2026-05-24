package falken_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/pkg/falken"
)

func TestPublicAPIHardening_ConfigValidation(t *testing.T) {
	setStateHomeEnv(t)

	workspace := publicTempWorkspace(t)
	if _, err := falken.New(falken.Config{WorkspaceDir: workspace}); !errors.Is(err, falken.ErrLLMRequired) {
		t.Fatalf("missing LLM error = %v, want ErrLLMRequired", err)
	}
	if _, err := falken.New(falken.Config{LLM: &publicFakeLLM{}}); !errors.Is(err, falken.ErrInvalidConfig) || !strings.Contains(err.Error(), "workspace root is required") {
		t.Fatalf("missing workspace error = %v, want ErrInvalidConfig with workspace reason", err)
	}
	if _, err := falken.New(falken.Config{WorkspaceDir: string(rune(0)), LLM: &publicFakeLLM{}}); !errors.Is(err, falken.ErrInvalidConfig) {
		t.Fatalf("invalid workspace error = %v, want ErrInvalidConfig", err)
	}
	if _, err := falken.New(falken.Config{WorkspaceDir: workspace, StateDir: string(rune(0)), LLM: &publicFakeLLM{}}); !errors.Is(err, falken.ErrInvalidConfig) {
		t.Fatalf("invalid state dir error = %v, want ErrInvalidConfig", err)
	}

	session, err := falken.New(falken.Config{WorkspaceDir: workspace, LLM: &publicFakeLLM{}})
	if err != nil {
		t.Fatalf("valid minimal config: %v", err)
	}
	if session.Paths.WorkspaceRoot == "" || session.Paths.StateRoot == "" {
		t.Fatalf("paths = %+v, want canonical workspace/state paths", session.Paths)
	}
}

func TestPublicAPIHardening_SafeDefaults(t *testing.T) {
	setStateHomeEnv(t)

	llm := &publicFakeLLM{responses: []falken.CompletionResponse{{
		AssistantText: "ok",
		FinishReason:  falken.FinishReasonStop,
	}}}
	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              llm,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start with omitted roots/handlers/system prompt: %v", err)
	}
	result, err := session.Run(context.Background(), falken.RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Run with omitted handlers: %v", err)
	}
	if !result.Completed || result.FinalOutput != "ok" {
		t.Fatalf("result = %+v, want successful run", result)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("LLM requests = %d, want one main call with heuristic routing", len(llm.requests))
	}
	if got := publicToolNames(llm.requests[0].Tools); !publicHasTool(got, "read_file") || !publicHasTool(got, "execute_command") {
		t.Fatalf("tools = %v, want default built-in tools when roots omitted", got)
	}
	if len(llm.requests[0].Messages) < 3 || !strings.Contains(llm.requests[0].Messages[len(llm.requests[0].Messages)-1].Content, "mode: default") {
		t.Fatalf("messages = %+v, want deterministic default runtime context", llm.requests[0].Messages)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestPublicAPIHardening_PublicLifecycleErrors(t *testing.T) {
	setStateHomeEnv(t)

	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              &publicFakeLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := session.Run(context.Background(), falken.RunRequest{Prompt: "before start"}); !errors.Is(err, falken.ErrSessionNotStarted) {
		t.Fatalf("run before start error = %v, want ErrSessionNotStarted", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close before start: %v", err)
	}
	if err := session.Start(); !errors.Is(err, falken.ErrSessionClosed) {
		t.Fatalf("start after close error = %v, want ErrSessionClosed", err)
	}
	if _, err := session.Run(context.Background(), falken.RunRequest{Prompt: "after close"}); !errors.Is(err, falken.ErrSessionClosed) {
		t.Fatalf("run after close error = %v, want ErrSessionClosed", err)
	}

	blocking := newPublicBlockingLLM()
	active, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              blocking,
	})
	if err != nil {
		t.Fatalf("New active: %v", err)
	}
	if err := active.Start(); err != nil {
		t.Fatalf("Start active: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := active.Run(context.Background(), falken.RunRequest{Prompt: "first"})
		done <- err
	}()
	<-blocking.started
	if _, err := active.Run(context.Background(), falken.RunRequest{Prompt: "second"}); !errors.Is(err, falken.ErrTopLevelRunActive) {
		t.Fatalf("overlapping run error = %v, want ErrTopLevelRunActive", err)
	}
	close(blocking.release)
	if err := <-done; err != nil {
		t.Fatalf("first Run: %v", err)
	}
}

func TestPublicAPIHardening_EventAndResultShapes(t *testing.T) {
	call := falken.ToolCall{ID: "call-1", Name: "read_file"}
	toolResult := falken.ToolResult{CallID: "call-1", Name: "read_file", Content: "content"}
	runResult := falken.RunResult{FinalOutput: "done", Completed: true}
	network := falken.NetworkRequestEvent{Host: "example.com:443", Method: "CONNECT", Allowed: false, Reason: "blocked"}
	events := []falken.Event{
		{Type: falken.EventAssistantText, Text: "hello"},
		{Type: falken.EventToolCall, ToolCall: &call},
		{Type: falken.EventToolResult, ToolResult: &toolResult},
		{Type: falken.EventCommandChunk, CommandChunk: &falken.CommandChunk{Stream: "stdout", Data: []byte("chunk")}},
		{Type: falken.EventNetworkRequest, NetworkRequest: &network},
		{Type: falken.EventRunCompleted, RunResult: &runResult},
		{Type: falken.EventRunFailed, Error: "failed"},
		{Type: falken.EventThought, Text: "best effort"},
	}
	got := make([]falken.EventType, 0, len(events))
	for _, event := range events {
		got = append(got, event.Type)
	}
	want := []falken.EventType{
		falken.EventAssistantText,
		falken.EventToolCall,
		falken.EventToolResult,
		falken.EventCommandChunk,
		falken.EventNetworkRequest,
		falken.EventRunCompleted,
		falken.EventRunFailed,
		falken.EventThought,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if events[4].NetworkRequest.Host != "example.com:443" || events[4].NetworkRequest.Allowed ||
		!events[5].RunResult.Completed || events[5].RunResult.FinalOutput != "done" || events[6].Error != "failed" {
		t.Fatalf("event payloads = %+v, want public result/error payloads", events)
	}
}

func TestPublicAPIHardening_V1NonGoalsAreNotPublicConfig(t *testing.T) {
	configType := reflect.TypeOf(falken.Config{})
	for i := 0; i < configType.NumField(); i++ {
		if configType.Field(i).Name == "VerificationReviewerLLM" {
			continue
		}
		name := strings.ToLower(configType.Field(i).Name)
		for _, forbidden := range []string{"verify", "verification", "delegate", "delegation", "job", "submit"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("Config exposes v1 non-goal field %q", configType.Field(i).Name)
			}
		}
	}

	sessionType := reflect.TypeOf((*falken.Session)(nil))
	for i := 0; i < sessionType.NumMethod(); i++ {
		name := strings.ToLower(sessionType.Method(i).Name)
		for _, forbidden := range []string{"verify", "verification", "delegate", "delegation", "job", "submit"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("Session exposes v1 non-goal method %q", sessionType.Method(i).Name)
			}
		}
	}
}
