package falken_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/pkg/falken"
)

func TestPublicSessionConfigValidation(t *testing.T) {
	setStateHomeEnv(t)

	workspace := publicTempWorkspace(t)
	if _, err := falken.New(falken.Config{WorkspaceDir: workspace}); !errors.Is(err, falken.ErrLLMRequired) {
		t.Fatalf("New without LLM error = %v, want ErrLLMRequired", err)
	}

	if _, err := falken.New(falken.Config{LLM: &publicFakeLLM{}}); err == nil || !strings.Contains(err.Error(), "workspace root is required") {
		t.Fatalf("New without workspace error = %v, want workspace validation", err)
	}

	session, err := falken.New(falken.Config{
		WorkspaceDir:     workspace,
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              &publicFakeLLM{},
	})
	if err != nil {
		t.Fatalf("New valid config: %v", err)
	}
	if _, err := os.Stat(session.Paths.MetadataPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata stat after New = %v, want no startup side effect", err)
	}
}

func TestPublicSessionAssemblesRuntimeAndRunsAgent(t *testing.T) {
	setStateHomeEnv(t)

	workspace := publicTempWorkspace(t)
	llm := &publicFakeLLM{responses: []falken.CompletionResponse{{
		AssistantText: "assembled",
		FinishReason:  falken.FinishReasonStop,
	}}}
	var events []falken.Event
	completed := false
	session, err := falken.New(falken.Config{
		WorkspaceDir:     workspace,
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		StateDir:         filepath.Join(t.TempDir(), "state"),
		ToolProviders: []falken.ToolProvider{falken.StaticToolProvider(falken.ToolFunc(publicNativeToolDescriptor("external_reader"), func(context.Context, falken.ToolInvocation) (falken.ToolExecutionResult, error) {
			return falken.ToolExecutionResult{Success: true, Status: "succeeded"}, nil
		}))},
		LLM:              llm,
		BaseSystemPrompt: "You are Falken v1.",
		Events:           func(event falken.Event) { events = append(events, event) },
		OnCompleted:      func(context.Context, falken.RunResult) error { completed = true; return nil },
		Policy:           falken.PolicyConfig{StrictCommandAllowlist: true},
		ApprovalHandler:  nil,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := session.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	result, err := session.Run(context.Background(), falken.RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "assembled" {
		t.Fatalf("result = %+v, want completed output", result)
	}
	if !completed {
		t.Fatal("completion callback was not invoked")
	}
	if got := publicEventTypes(events); !reflect.DeepEqual(got, []falken.EventType{falken.EventPlanRouting, falken.EventAssistantText, falken.EventRunCompleted}) {
		t.Fatalf("events = %v, want assistant_text/run_completed", got)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("LLM requests = %d, want one main call with heuristic routing", len(llm.requests))
	}
	if got := publicToolNames(llm.requests[0].Tools); !publicHasTool(got, "read_file") || !publicHasTool(got, "execute_command") || !publicHasTool(got, "external_reader") {
		t.Fatalf("LLM tools = %v, want built-ins plus discovered active tool", got)
	}

	history := readPublicHistory(t, session.Paths.HistoryPath)
	if got := publicMessageRoles(history); !reflect.DeepEqual(got, []falken.Role{falken.RoleSystem, falken.RoleUser, falken.RoleAssistant}) {
		t.Fatalf("history roles = %v", got)
	}
	if !strings.Contains(history[0].Content, "You are Falken v1.") || strings.Contains(history[0].Content, "--- CURRENT MODE ---") {
		t.Fatalf("system prompt = %q, want stable base prompt without mode context", history[0].Content)
	}
}

func TestPublicSessionReadTodos(t *testing.T) {
	setStateHomeEnv(t)

	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              &publicFakeLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	before, err := session.ReadTodos()
	if err != nil {
		t.Fatalf("ReadTodos before write: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("todos length = %d, want 0", len(before))
	}

	writePublicJSON(t, session.Paths.TodosPath, map[string]any{
		"items": []map[string]any{{
			"id":      "task-1",
			"content": "Update session screen",
			"status":  "in_progress",
		}},
	})

	todos, err := session.ReadTodos()
	if err != nil {
		t.Fatalf("ReadTodos: %v", err)
	}
	want := []falken.Todo{{ID: "task-1", Content: "Update session screen", Status: "in_progress"}}
	if !reflect.DeepEqual(todos, want) {
		t.Fatalf("todos = %+v, want %+v", todos, want)
	}
}

func TestPublicSessionReadMemory(t *testing.T) {
	setStateHomeEnv(t)

	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              &publicFakeLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	before, err := session.ReadMemory()
	if err != nil {
		t.Fatalf("ReadMemory before write: %v", err)
	}
	if len(before.Entries) != 0 || before.CurrentGoal != "" {
		t.Fatalf("memory = %+v, want empty", before)
	}

	writePublicJSON(t, session.Paths.MemoryPath, map[string]any{
		"current_goal":    "Add public memory read API",
		"entries":         []string{"legacy note"},
		"important_files": []string{"pkg/falken/memory.go"},
		"decisions":       []string{"Expose read only."},
		"open_questions":  []string{"Need docs?"},
	})

	memory, err := session.ReadMemory()
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	want := falken.Memory{
		CurrentGoal:    "Add public memory read API",
		Entries:        []string{"legacy note"},
		ImportantFiles: []string{"pkg/falken/memory.go"},
		Decisions:      []string{"Expose read only."},
		OpenQuestions:  []string{"Need docs?"},
	}
	if !reflect.DeepEqual(memory, want) {
		t.Fatalf("memory = %+v, want %+v", memory, want)
	}
}

func TestPublicSessionResetAndCloseLifecycle(t *testing.T) {
	setStateHomeEnv(t)

	llm := &publicFakeLLM{responses: []falken.CompletionResponse{
		{AssistantText: "first", FinishReason: falken.FinishReasonStop},
		{AssistantText: "second", FinishReason: falken.FinishReasonStop},
	}}
	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              llm,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), falken.RunRequest{Prompt: "first"}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if history := readPublicHistory(t, session.Paths.HistoryPath); len(history) == 0 {
		t.Fatal("expected persisted history after run")
	}

	backupPath := filepath.Join(session.Paths.BackupRoot, "durable.txt")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		t.Fatalf("mkdir backup root: %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write durable backup: %v", err)
	}
	if err := session.ResetConversationState(); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}
	if history := readPublicHistory(t, session.Paths.HistoryPath); len(history) != 0 {
		t.Fatalf("history after reset = %+v, want empty", history)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("durable backup after reset: %v", err)
	}

	if result, err := session.Run(context.Background(), falken.RunRequest{Prompt: "second"}); err != nil || result.FinalOutput != "second" {
		t.Fatalf("second Run after reset = %+v/%v", result, err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := session.Run(context.Background(), falken.RunRequest{Prompt: "closed"}); !errors.Is(err, falken.ErrSessionClosed) {
		t.Fatalf("Run after close error = %v, want ErrSessionClosed", err)
	}
}

func TestPublicSessionOverlappingRunsAreRejected(t *testing.T) {
	setStateHomeEnv(t)

	llm := newPublicBlockingLLM()
	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              llm,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := session.Run(context.Background(), falken.RunRequest{Prompt: "first"})
		done <- err
	}()
	<-llm.started

	if _, err := session.Run(context.Background(), falken.RunRequest{Prompt: "second"}); !errors.Is(err, falken.ErrTopLevelRunActive) {
		t.Fatalf("overlapping Run error = %v, want ErrTopLevelRunActive", err)
	}

	close(llm.release)
	if err := <-done; err != nil {
		t.Fatalf("first Run after release: %v", err)
	}
}

func TestPublicSessionCompletionCallbackFailureAndFailedRunBehavior(t *testing.T) {
	setStateHomeEnv(t)

	callbackErr := errors.New("host callback failed")
	var callbackEvents []falken.Event
	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM: &publicFakeLLM{responses: []falken.CompletionResponse{{
			AssistantText: "done",
			FinishReason:  falken.FinishReasonStop,
		}}},
		Events:      func(event falken.Event) { callbackEvents = append(callbackEvents, event) },
		OnCompleted: func(context.Context, falken.RunResult) error { return callbackErr },
	})
	if err != nil {
		t.Fatalf("New callback session: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start callback session: %v", err)
	}
	result, err := session.Run(context.Background(), falken.RunRequest{Prompt: "finish"})
	if err == nil || !strings.Contains(err.Error(), callbackErr.Error()) {
		t.Fatalf("callback Run error = %v, want callback failure", err)
	}
	if result.Completed || !strings.Contains(result.Error, "completion callback") {
		t.Fatalf("callback result = %+v, want failed callback result", result)
	}
	if got := publicEventTypes(callbackEvents); !reflect.DeepEqual(got, []falken.EventType{falken.EventPlanRouting, falken.EventAssistantText, falken.EventRunFailed}) {
		t.Fatalf("callback failure events = %v, want assistant_text/run_failed only", got)
	}

	called := false
	var events []falken.Event
	failed, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              &publicFakeLLM{err: errors.New("llm failed")},
		Events:           func(event falken.Event) { events = append(events, event) },
		OnCompleted:      func(context.Context, falken.RunResult) error { called = true; return nil },
	})
	if err != nil {
		t.Fatalf("New failed session: %v", err)
	}
	if err := failed.Start(); err != nil {
		t.Fatalf("Start failed session: %v", err)
	}
	if _, err := failed.Run(context.Background(), falken.RunRequest{Prompt: "fail"}); err == nil {
		t.Fatal("Run succeeded, want LLM failure")
	}
	if called {
		t.Fatal("completion callback should not run on failed run")
	}
	if got := publicEventTypes(events); !reflect.DeepEqual(got, []falken.EventType{falken.EventPlanRouting, falken.EventRunFailed}) {
		t.Fatalf("failed events = %v, want run_failed", got)
	}
}

type publicFakeLLM struct {
	requests  []falken.CompletionRequest
	responses []falken.CompletionResponse
	err       error
}

func (l *publicFakeLLM) Complete(_ context.Context, request falken.CompletionRequest) (falken.CompletionResponse, error) {
	l.requests = append(l.requests, request)
	if isPublicRoutingRequest(request) {
		return publicRoutingResponse(false, "test default", "high", nil), nil
	}
	if l.err != nil {
		return falken.CompletionResponse{}, l.err
	}
	if len(l.responses) == 0 {
		return falken.CompletionResponse{AssistantText: "default", FinishReason: falken.FinishReasonStop}, nil
	}
	response := l.responses[0]
	l.responses = l.responses[1:]
	return response, nil
}

func isPublicRoutingRequest(request falken.CompletionRequest) bool {
	return request.ToolChoice != nil &&
		request.ToolChoice.Type == "tool" &&
		request.ToolChoice.Name == "decide_plan_mode"
}

func publicRoutingResponse(requiresPlan bool, reason, confidence string, signals []string) falken.CompletionResponse {
	if signals == nil {
		signals = []string{}
	}
	arguments, err := json.Marshal(map[string]any{
		"requires_plan": requiresPlan,
		"reason":        reason,
		"confidence":    confidence,
		"signals":       signals,
	})
	if err != nil {
		panic(err)
	}
	return falken.CompletionResponse{
		ToolCalls: []falken.ToolCall{{
			ID:        "route-1",
			Name:      "decide_plan_mode",
			Arguments: arguments,
		}},
		FinishReason: falken.FinishReasonToolCalls,
	}
}

type publicBlockingLLM struct {
	started chan struct{}
	release chan struct{}
}

func newPublicBlockingLLM() *publicBlockingLLM {
	return &publicBlockingLLM{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (l *publicBlockingLLM) Complete(_ context.Context, request falken.CompletionRequest) (falken.CompletionResponse, error) {
	if isPublicRoutingRequest(request) {
		return publicRoutingResponse(false, "test default", "high", nil), nil
	}
	close(l.started)
	<-l.release
	return falken.CompletionResponse{AssistantText: "released", FinishReason: falken.FinishReasonStop}, nil
}

func publicTempWorkspace(t *testing.T) string {
	t.Helper()

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	return workspace
}

func writePublicToolPackage(t *testing.T, root, packageName, manifest string) {
	t.Helper()

	dir := filepath.Join(root, packageName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tool package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tool.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write tool manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm placeholder"), 0o600); err != nil {
		t.Fatalf("write tool wasm: %v", err)
	}
}

func writePublicPluginPackage(t *testing.T, root, packageName, manifest string) {
	t.Helper()

	dir := filepath.Join(root, packageName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm placeholder"), 0o600); err != nil {
		t.Fatalf("write plugin wasm: %v", err)
	}
}

func publicToolManifest(toolName, packageName string, defaultLoad bool) string {
	return `{
		"manifest_version": 1,
		"name": "` + packageName + `",
		"description": "Public API tool package.",
		"runtime": "wasm",
		"tools": [{
			"name": "` + toolName + `",
			"description": "Reads a file.",
			"input_schema": {"type": "object"},
			"default_load": ` + publicBoolJSON(defaultLoad) + `
		}],
		"permissions": {
			"files": [{"path": "/workspace/", "match": "prefix", "modes": ["read"]}]
		}
	}`
}

func publicPluginManifest(packageName, hookName, event string) string {
	return `{
		"manifest_version": 1,
		"name": "` + packageName + `",
		"description": "Public API plugin package.",
		"runtime": "wasm",
		"hooks": [{"name": "` + hookName + `", "event": "` + event + `"}],
		"permissions": {
			"files": [{"path": "/workspace/", "match": "prefix", "modes": ["read"]}]
		}
	}`
}

func readPublicHistory(t *testing.T, path string) []falken.Message {
	t.Helper()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read history file: %v", err)
	}
	var entries []string
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("decode history entries: %v", err)
	}
	messages := make([]falken.Message, 0, len(entries))
	for _, entry := range entries {
		var message falken.Message
		if err := json.Unmarshal([]byte(entry), &message); err != nil {
			t.Fatalf("decode history message %q: %v", entry, err)
		}
		messages = append(messages, message)
	}
	return messages
}

func writePublicJSON(t *testing.T, path string, value any) {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write JSON %q: %v", path, err)
	}
}

func publicEventTypes(events []falken.Event) []falken.EventType {
	types := make([]falken.EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func publicToolNames(definitions []falken.ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func publicHasTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func publicNativeToolDescriptor(name string) falken.ToolDescriptor {
	return falken.ToolDescriptor{
		Name:        name,
		Description: "Host-provided test tool.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		DefaultLoad: true,
		Safety:      falken.ToolSafety{ReadsWorkspace: true},
	}
}

func publicMessageRoles(messages []falken.Message) []falken.Role {
	roles := make([]falken.Role, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, message.Role)
	}
	return roles
}

func publicBoolJSON(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
