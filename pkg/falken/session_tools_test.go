package falken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/extensions/manifest"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/policy"
)

func TestSessionToolProviderStartsAndCloses(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if provider.starts != 1 {
		t.Fatalf("provider starts = %d, want 1", provider.starts)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if provider.closes != 1 {
		t.Fatalf("provider closes = %d, want 1", provider.closes)
	}
}

func TestSessionToolProviderStartCanReenterSessionWithoutDeadlock(t *testing.T) {
	setStateHomeEnv(t)

	var session *Session
	provider := &recordingToolProvider{
		startFunc: func() error {
			_, err := session.CurrentMode()
			if !errors.Is(err, ErrSessionStarting) {
				return fmt.Errorf("CurrentMode during Start error = %w, want ErrSessionStarting", err)
			}
			return nil
		},
	}
	var err error
	session, err = New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func TestSessionStartContextCancellationIsObserved(t *testing.T) {
	setStateHomeEnv(t)

	session := newSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := session.StartContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartContext error = %v, want context.Canceled", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start after canceled StartContext: %v", err)
	}
}

func TestSessionStartContextReachesToolProvider(t *testing.T) {
	setStateHomeEnv(t)

	ctx, cancel := context.WithCancel(context.Background())
	provider := &recordingToolProvider{
		startWithContext: func(ctx context.Context) error {
			cancel()
			return ctx.Err()
		},
	}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := session.StartContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartContext error = %v, want context.Canceled", err)
	}
	if provider.starts != 1 {
		t.Fatalf("provider starts = %d, want 1", provider.starts)
	}
}

func TestSessionCloseContextReachesToolProvider(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{
		closeWithContext: func(ctx context.Context) error {
			return ctx.Err()
		},
	}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := session.CloseContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseContext error = %v, want context.Canceled", err)
	}
}

func TestCloneToolEntriesDeepClonesFilePermissionModes(t *testing.T) {
	entries := []tools.Entry{{
		Name: "external",
		Permissions: manifest.DeclaredPermissions{
			Files: []manifest.FilePermission{{
				Path:  "src",
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessRead},
			}},
		},
	}}
	cloned := tools.CloneEntries(entries)
	cloned[0].Permissions.Files[0].Modes[0] = policy.FileAccessWrite
	if entries[0].Permissions.Files[0].Modes[0] != policy.FileAccessRead {
		t.Fatalf("source modes mutated through clone: %+v", entries[0].Permissions.Files[0].Modes)
	}
}

func TestSessionStartRejectsDuplicateExternalToolName(t *testing.T) {
	setStateHomeEnv(t)

	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders: []ToolProvider{
			&recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("external_tool")}},
			&recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("external_tool")}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if err == nil || !strings.Contains(err.Error(), "duplicate tool name") {
		t.Fatalf("Start error = %v, want duplicate tool name", err)
	}
}

func TestSessionStartRejectsDuplicateBuiltinToolName(t *testing.T) {
	setStateHomeEnv(t)

	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders:    []ToolProvider{&recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("read_file")}}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if err == nil || !strings.Contains(err.Error(), "duplicate tool name") {
		t.Fatalf("Start error = %v, want duplicate built-in tool name", err)
	}
}

func TestRemoteWorkspaceModeRejectsProviderWorkspaceTool(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("external_reader")}}
	session, err := New(Config{
		WorkspaceDir:     filepath.Join(t.TempDir(), "missing-workspace"),
		StateDir:         filepath.Join(t.TempDir(), "agent-state"),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeSandbox},
		Runtime: runtimeAdaptersProvider{adapters: RuntimeAdapters{
			SandboxRuntime: &countingSandboxRuntime{},
			WorkspaceFiles: stubWorkspaceOperations{},
		}},
		LLM:           &sessionFakeLLM{},
		ToolProviders: []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if err == nil || !strings.Contains(err.Error(), "cannot run in remote workspace mode") {
		t.Fatalf("Start error = %v, want remote workspace custom-tool rejection", err)
	}
	if provider.closes != 1 {
		t.Fatalf("provider closes = %d, want cleanup after descriptor rejection", provider.closes)
	}
}

func TestRemoteWorkspaceModeRejectsProviderStartupWorkspaceSafety(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{startupSafety: ToolSafety{ReadsWorkspace: true}}
	session, err := New(Config{
		WorkspaceDir:     filepath.Join(t.TempDir(), "missing-workspace"),
		StateDir:         filepath.Join(t.TempDir(), "agent-state"),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeSandbox},
		Runtime: runtimeAdaptersProvider{adapters: RuntimeAdapters{
			SandboxRuntime: &countingSandboxRuntime{},
			WorkspaceFiles: stubWorkspaceOperations{},
		}},
		LLM:           &sessionFakeLLM{},
		ToolProviders: []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if err == nil || !strings.Contains(err.Error(), "cannot run in remote workspace mode") {
		t.Fatalf("Start error = %v, want startup safety rejection", err)
	}
	if provider.starts != 0 {
		t.Fatalf("provider starts = %d, want rejection before provider startup", provider.starts)
	}
}

func TestRemoteWorkspaceModeAllowsNonWorkspaceProviderTool(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{descriptors: []ToolDescriptor{testNonWorkspaceToolDescriptor("external_state")}}
	session, err := New(Config{
		WorkspaceDir:     filepath.Join(t.TempDir(), "missing-workspace"),
		StateDir:         filepath.Join(t.TempDir(), "agent-state"),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeSandbox},
		Runtime: runtimeAdaptersProvider{adapters: RuntimeAdapters{
			SandboxRuntime: &countingSandboxRuntime{},
			WorkspaceFiles: stubWorkspaceOperations{},
		}},
		LLM:           &sessionFakeLLM{},
		ToolProviders: []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()
	if provider.starts != 1 {
		t.Fatalf("provider starts = %d, want provider started", provider.starts)
	}
}

func TestRemoteWorkspaceModeCanOptIntoProviderWorkspaceTool(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("external_reader")}}
	session, err := New(Config{
		WorkspaceDir:     filepath.Join(t.TempDir(), "missing-workspace"),
		StateDir:         filepath.Join(t.TempDir(), "agent-state"),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeSandbox},
		Runtime: runtimeAdaptersProvider{adapters: RuntimeAdapters{
			SandboxRuntime: &countingSandboxRuntime{},
			WorkspaceFiles: stubWorkspaceOperations{},
		}},
		LLM:                             &sessionFakeLLM{},
		ToolProviders:                   []ToolProvider{provider},
		AllowWorkspaceToolsInRemoteMode: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()
}

func TestLocalWorkspaceModeAllowsProviderWorkspaceTool(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("external_reader")}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()
}

func TestSessionToolProviderToolIsListedToLLM(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		ToolProviders:    []ToolProvider{&recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("external_reader")}}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "list tools"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(llm.requests) == 0 {
		t.Fatal("LLM received no requests")
	}
	if !toolDefinitionNames(llm.requests[0].Tools)["external_reader"] {
		t.Fatalf("LLM tools = %+v, want external_reader", llm.requests[0].Tools)
	}
}

func TestSessionToolProviderToolExecutesAndReturnsPayload(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{
		descriptors: []ToolDescriptor{testToolDescriptor("external_reader")},
		result: ToolExecutionResult{
			Success: true,
			Status:  "succeeded",
			Content: "provider content",
			Payload: json.RawMessage(`{"value":42}`),
		},
	}
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-provider",
				Name:      "external_reader",
				Arguments: json.RawMessage(`{"path":"x"}`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "use provider"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(provider.invocations) != 1 || provider.invocations[0].Name != "external_reader" {
		t.Fatalf("provider invocations = %+v, want external_reader", provider.invocations)
	}
	if len(llm.requests) < 2 {
		t.Fatalf("LLM requests = %d, want follow-up request with tool result", len(llm.requests))
	}
	var found bool
	for _, message := range llm.requests[1].Messages {
		if message.ToolResult == nil || message.ToolResult.Name != "external_reader" {
			continue
		}
		found = true
		if message.ToolResult.Content != "provider content" || string(message.ToolResult.Payload) != `{"value":42}` {
			t.Fatalf("tool result = %+v, want provider content and payload", message.ToolResult)
		}
	}
	if !found {
		t.Fatalf("messages = %+v, want external_reader tool result", llm.requests[2].Messages)
	}
}

func TestScopedToolHostDeniesUndeclaredCapabilities(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{
		descriptors: []ToolDescriptor{{
			Name:        "limited_tool",
			Description: "limited provider tool",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			DefaultLoad: true,
		}},
		executeWithHost: func(ctx context.Context, _ ToolInvocation, host ToolHost) (ToolExecutionResult, error) {
			command, err := host.ExecuteCommand(ctx, ToolCommandRequest{Command: "echo nope"})
			if err != nil {
				return ToolExecutionResult{}, err
			}
			file, err := host.CheckFileAccess(ctx, ToolFileAccessRequest{Path: "notes.txt", Mode: FileAccessRead})
			if err != nil {
				return ToolExecutionResult{}, err
			}
			_, stateErr := host.GetState(ctx, ToolStateGetRequest{Key: "secret"})
			payload := map[string]any{
				"command_status": command.Status,
				"file_allowed":   file.Allowed,
				"state_error":    "",
			}
			if stateErr != nil {
				payload["state_error"] = stateErr.Error()
			}
			data, _ := json.Marshal(payload)
			return ToolExecutionResult{Success: true, Status: "succeeded", Payload: data}, nil
		},
	}
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls:    []ToolCall{{ID: "call-limited", Name: "limited_tool", Arguments: json.RawMessage(`{}`)}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "use limited"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	tool := lastToolResultMessage(t, session)
	var payload map[string]any
	if err := json.Unmarshal(tool.Payload, &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["command_status"] != "blocked" || payload["file_allowed"] != false || !strings.Contains(fmt.Sprint(payload["state_error"]), "capability") {
		t.Fatalf("payload = %+v, want blocked command/file/state", payload)
	}
}

func TestScopedToolHostEmitReachesConfigAndRunEvents(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{
		descriptors: []ToolDescriptor{testToolDescriptor("event_tool")},
		executeWithHost: func(_ context.Context, _ ToolInvocation, host ToolHost) (ToolExecutionResult, error) {
			host.Emit(Event{Type: EventThought, Text: "from provider"})
			return ToolExecutionResult{Success: true, Status: "succeeded"}, nil
		},
	}
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls:    []ToolCall{{ID: "call-event", Name: "event_tool", Arguments: json.RawMessage(`{}`)}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}
	var configEvents []Event
	var runEvents []Event
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		ToolProviders:    []ToolProvider{provider},
		Events:           func(event Event) { configEvents = append(configEvents, event) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if _, err := session.Run(context.Background(), RunRequest{
		Prompt: "use event tool",
		Events: func(event Event) { runEvents = append(runEvents, event) },
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsThoughtEvent(configEvents, "from provider") {
		t.Fatalf("config events = %+v, want provider event", configEvents)
	}
	if !containsThoughtEvent(runEvents, "from provider") {
		t.Fatalf("run events = %+v, want provider event", runEvents)
	}
}

func TestSessionPlanModeExposesPlanSafeProviderTool(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		ToolProviders:    []ToolProvider{&recordingToolProvider{descriptors: []ToolDescriptor{testPlanSafeToolDescriptor("external_reader")}}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "plan with provider"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !toolDefinitionNames(llm.requests[0].Tools)["external_reader"] {
		t.Fatalf("plan-mode tools = %+v, want external_reader", llm.requests[0].Tools)
	}
}

func TestSessionPlanModeExposesReadOnlyProviderToolWithoutPlanSafe(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		ToolProviders:    []ToolProvider{&recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("external_reader")}}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "plan with provider"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !toolDefinitionNames(llm.requests[0].Tools)["external_reader"] {
		t.Fatalf("plan-mode tools = %+v, want external_reader", llm.requests[0].Tools)
	}
}

func TestSessionPlanModeBlocksMutatingProviderTool(t *testing.T) {
	setStateHomeEnv(t)

	mutating := testToolDescriptor("external_writer")
	mutating.Safety = ToolSafety{MutatesWorkspace: true}
	provider := &recordingToolProvider{descriptors: []ToolDescriptor{mutating}}
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-provider",
				Name:      "external_writer",
				Arguments: json.RawMessage(`{}`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "force provider"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(provider.invocations) != 0 {
		t.Fatalf("provider invocations = %+v, want none for blocked plan-mode tool", provider.invocations)
	}
	tool := lastToolResultMessage(t, session)
	if tool.Name != "external_writer" || !strings.Contains(tool.Error, "plan mode") {
		t.Fatalf("tool result = %+v, want plan-mode block", tool)
	}
}

func TestSessionPlanModeExecutesPlanSafeProviderTool(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{
		descriptors: []ToolDescriptor{testPlanSafeToolDescriptor("external_reader")},
		result:      ToolExecutionResult{Success: true, Status: "succeeded", Payload: json.RawMessage(`{"planned":true}`)},
	}
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-provider",
				Name:      "external_reader",
				Arguments: json.RawMessage(`{}`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "use provider"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(provider.invocations) != 1 {
		t.Fatalf("provider invocations = %+v, want one invocation", provider.invocations)
	}
	tool := lastToolResultMessage(t, session)
	if tool.Name != "external_reader" || string(tool.Payload) != `{"planned":true}` {
		t.Fatalf("tool result = %+v, want plan-safe provider payload", tool)
	}
}

func TestCloneSessionConfigClonesToolProvidersSlice(t *testing.T) {
	first := &recordingToolProvider{}
	second := &recordingToolProvider{}
	config := SessionConfig{ToolProviders: []ToolProvider{first}}

	cloned := cloneSessionConfig(config)
	if len(cloned.ToolProviders) != 1 {
		t.Fatalf("cloned providers = %d, want 1", len(cloned.ToolProviders))
	}
	config.ToolProviders[0] = second
	if cloned.ToolProviders[0] != first {
		t.Fatal("clone shares tool provider slice backing array")
	}
}

func TestToolDescriptorValidationRejectsInvalidDescriptors(t *testing.T) {
	tests := []struct {
		name       string
		descriptor ToolDescriptor
		want       string
	}{
		{name: "empty name", descriptor: ToolDescriptor{Description: "desc", Parameters: json.RawMessage(`{"type":"object"}`)}, want: "empty name"},
		{name: "invalid name", descriptor: ToolDescriptor{Name: "1bad", Description: "desc", Parameters: json.RawMessage(`{"type":"object"}`)}, want: "invalid name"},
		{name: "empty description", descriptor: ToolDescriptor{Name: "bad", Parameters: json.RawMessage(`{"type":"object"}`)}, want: "empty description"},
		{name: "empty schema", descriptor: ToolDescriptor{Name: "bad", Description: "desc"}, want: "empty parameters schema"},
		{name: "invalid json", descriptor: ToolDescriptor{Name: "bad", Description: "desc", Parameters: json.RawMessage(`{`)}, want: "invalid parameters schema"},
		{name: "non object", descriptor: ToolDescriptor{Name: "bad", Description: "desc", Parameters: json.RawMessage(`{"type":"string"}`)}, want: "object schema"},
		{name: "non object properties", descriptor: ToolDescriptor{Name: "bad", Description: "desc", Parameters: json.RawMessage(`{"type":"object","properties":[]}`)}, want: "properties must be a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateToolDescriptor(tt.descriptor)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateToolDescriptor error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSessionToolProviderClosesStartedProvidersOnStartFailure(t *testing.T) {
	setStateHomeEnv(t)

	var closeLog []string
	first := &recordingToolProvider{name: "first", closeLog: &closeLog}
	second := &recordingToolProvider{name: "second", closeLog: &closeLog, startErr: errors.New("start failed")}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders:    []ToolProvider{first, second},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if err == nil || !strings.Contains(err.Error(), "start failed") {
		t.Fatalf("Start error = %v, want start failed", err)
	}
	if got := strings.Join(closeLog, ","); got != "first" {
		t.Fatalf("close order = %q, want first", got)
	}
	if second.closes != 0 {
		t.Fatalf("second closes = %d, failed Start provider should not be closed", second.closes)
	}
}

func TestSessionToolProviderClosesStartedProvidersOnToolsFailure(t *testing.T) {
	setStateHomeEnv(t)

	var closeLog []string
	first := &recordingToolProvider{name: "first", closeLog: &closeLog, descriptors: []ToolDescriptor{testToolDescriptor("first_tool")}}
	second := &recordingToolProvider{name: "second", closeLog: &closeLog, err: errors.New("tools failed")}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders:    []ToolProvider{first, second},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if err == nil || !strings.Contains(err.Error(), "tools failed") {
		t.Fatalf("Start error = %v, want tools failed", err)
	}
	if first.closes != 1 || second.closes != 1 {
		t.Fatalf("closes first=%d second=%d, want both closed", first.closes, second.closes)
	}
	if got := strings.Join(closeLog, ","); got != "second,first" {
		t.Fatalf("close order = %q, want reverse started order", got)
	}
}

func TestSessionToolProviderStartupHostIsCapabilityScoped(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	if err := os.WriteFile(filepath.Join(workspace, "known.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	unscoped := &recordingToolProvider{}
	scoped := &recordingToolProvider{startupSafety: ToolSafety{ReadsHostState: true, ReadsWorkspace: true, ExecutesShell: true}}
	session, err := New(Config{
		WorkspaceDir:     workspace,
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders:    []ToolProvider{unscoped, scoped},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if _, err := unscoped.host.GetState(context.Background(), ToolStateGetRequest{Key: "missing"}); !errors.Is(err, errToolHostCapabilityDenied) {
		t.Fatalf("unscoped GetState error = %v, want capability denied", err)
	}
	denied, err := unscoped.host.CheckFileAccess(context.Background(), ToolFileAccessRequest{Path: "known.txt", Mode: FileAccessRead})
	if err != nil {
		t.Fatalf("unscoped CheckFileAccess: %v", err)
	}
	if denied.Allowed || !strings.Contains(denied.Explanation, "does not declare") {
		t.Fatalf("unscoped file access = %+v, want denied by capability scope", denied)
	}
	blocked, err := unscoped.host.ExecuteCommand(context.Background(), ToolCommandRequest{Command: "printf hi"})
	if err != nil {
		t.Fatalf("unscoped ExecuteCommand: %v", err)
	}
	if blocked.Executed || blocked.Status != "blocked" {
		t.Fatalf("unscoped command = %+v, want blocked", blocked)
	}

	if _, err := scoped.host.GetState(context.Background(), ToolStateGetRequest{Key: "missing"}); err != nil {
		t.Fatalf("scoped GetState: %v", err)
	}
	allowed, err := scoped.host.CheckFileAccess(context.Background(), ToolFileAccessRequest{Path: "known.txt", Mode: FileAccessRead})
	if err != nil {
		t.Fatalf("scoped CheckFileAccess: %v", err)
	}
	if !allowed.Allowed {
		t.Fatalf("scoped file access = %+v, want allowed", allowed)
	}
	command, err := scoped.host.ExecuteCommand(context.Background(), ToolCommandRequest{Command: "printf hi"})
	if err != nil {
		t.Fatalf("scoped ExecuteCommand: %v", err)
	}
	if !command.Executed || command.Status != "succeeded" || command.Stdout != "hi" {
		t.Fatalf("scoped command = %+v, want executed printf", command)
	}
}

func TestSessionToolProviderCloseIsIdempotent(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("external_tool")}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if provider.closes != 1 {
		t.Fatalf("provider closes = %d, want one close", provider.closes)
	}
}

func testToolDescriptor(name string) ToolDescriptor {
	return ToolDescriptor{
		Name:        name,
		Description: "test provider tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		DefaultLoad: true,
		Safety:      ToolSafety{ReadsWorkspace: true},
	}
}

func testPlanSafeToolDescriptor(name string) ToolDescriptor {
	descriptor := testToolDescriptor(name)
	descriptor.Safety.PlanSafe = true
	return descriptor
}

func testNonWorkspaceToolDescriptor(name string) ToolDescriptor {
	return ToolDescriptor{
		Name:        name,
		Description: "test provider tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		DefaultLoad: true,
		Safety:      ToolSafety{PlanSafe: true},
	}
}

func toolDefinitionNames(definitions []ToolDefinition) map[string]bool {
	names := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		names[definition.Name] = true
	}
	return names
}

func containsThoughtEvent(events []Event, text string) bool {
	for _, event := range events {
		if event.Type == EventThought && event.Text == text {
			return true
		}
	}
	return false
}

type recordingToolProvider struct {
	name             string
	starts           int
	closes           int
	closeLog         *[]string
	host             ToolHost
	descriptors      []ToolDescriptor
	result           ToolExecutionResult
	err              error
	startErr         error
	invocations      []ToolInvocation
	startFunc        func() error
	startWithContext func(context.Context) error
	closeWithContext func(context.Context) error
	executeWithHost  func(context.Context, ToolInvocation, ToolHost) (ToolExecutionResult, error)
	startupSafety    ToolSafety
}

func (p *recordingToolProvider) Start(ctx context.Context, host ToolHost) error {
	p.starts++
	p.host = host
	if p.startWithContext != nil {
		if err := p.startWithContext(ctx); err != nil {
			return err
		}
	}
	if p.startFunc != nil {
		if err := p.startFunc(); err != nil {
			return err
		}
	}
	return p.startErr
}

func (p *recordingToolProvider) Tools(context.Context) ([]ToolDescriptor, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.descriptors == nil {
		return nil, nil
	}
	out := make([]ToolDescriptor, len(p.descriptors))
	copy(out, p.descriptors)
	return out, nil
}

func (p *recordingToolProvider) ExecuteTool(_ context.Context, invocation ToolInvocation) (ToolExecutionResult, error) {
	p.invocations = append(p.invocations, invocation)
	if p.err != nil {
		return ToolExecutionResult{}, p.err
	}
	if len(p.result.Payload) != 0 || p.result.Content != "" || p.result.Status != "" || p.result.Error != "" {
		return p.result, nil
	}
	return ToolExecutionResult{Success: true, Status: "succeeded", Payload: json.RawMessage(`{"ok":true}`)}, nil
}

func (p *recordingToolProvider) ExecuteToolWithHost(ctx context.Context, invocation ToolInvocation, host ToolHost) (ToolExecutionResult, error) {
	if p.executeWithHost != nil {
		p.invocations = append(p.invocations, invocation)
		return p.executeWithHost(ctx, invocation, host)
	}
	return p.ExecuteTool(ctx, invocation)
}

func (p *recordingToolProvider) Close(ctx context.Context) error {
	p.closes++
	if p.closeWithContext != nil {
		if err := p.closeWithContext(ctx); err != nil {
			return err
		}
	}
	if p.name != "" && p.closeLog != nil {
		*p.closeLog = append(*p.closeLog, p.name)
	}
	return nil
}

func (p *recordingToolProvider) StartupSafety() ToolSafety {
	return p.startupSafety
}
