package falken_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/pkg/falken"
)

func TestSessionE2E_FileToolReadThenEditFlow(t *testing.T) {
	workspace := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	target := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	llm := &scriptedFileLLM{responses: []falken.CompletionResponse{
		{ToolCalls: []falken.ToolCall{{ID: "read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"note.txt"}`)}}, FinishReason: falken.FinishReasonToolCalls},
		{ToolCalls: []falken.ToolCall{{ID: "edit-1", Name: "edit_file", Arguments: json.RawMessage(`{"path":"note.txt","old":"world","new":"falken"}`)}}, FinishReason: falken.FinishReasonToolCalls},
		{ToolCalls: []falken.ToolCall{{ID: "read-2", Name: "read_file", Arguments: json.RawMessage(`{"path":"note.txt"}`)}}, FinishReason: falken.FinishReasonToolCalls},
		{AssistantText: "file edited", FinishReason: falken.FinishReasonStop},
	}}
	var events []falken.Event
	session, err := falken.New(falken.Config{
		WorkspaceDir:     workspace,
		StateDir:         state,
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              llm,
		Events:           func(event falken.Event) { events = append(events, event) },
		ApprovalHandler:  fileApprovalHandler{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()

	result, err := session.Run(context.Background(), falken.RunRequest{Prompt: "edit note"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "file edited" {
		t.Fatalf("result = %+v, want completed edit", result)
	}
	if got := readText(t, target); got != "hello falken\n" {
		t.Fatalf("file content = %q, want edited", got)
	}
	if backups := countRegularFiles(t, filepath.Join(state, "backups")); backups == 0 {
		t.Fatal("expected managed edit to create a backup")
	}
	if got := toolResultNames(events); strings.Join(got, ",") != "read_file,edit_file,read_file" {
		t.Fatalf("tool results = %v, want read/edit/read", got)
	}
}

func TestSessionE2E_FileToolDirectEditWithoutReadFails(t *testing.T) {
	workspace := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	target := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	llm := &scriptedFileLLM{responses: []falken.CompletionResponse{
		{ToolCalls: []falken.ToolCall{{ID: "edit-1", Name: "edit_file", Arguments: json.RawMessage(`{"path":"note.txt","old":"world","new":"falken"}`)}}, FinishReason: falken.FinishReasonToolCalls},
		{AssistantText: "edit attempted", FinishReason: falken.FinishReasonStop},
	}}
	var events []falken.Event
	session, err := falken.New(falken.Config{
		WorkspaceDir:     workspace,
		StateDir:         state,
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              llm,
		Events:           func(event falken.Event) { events = append(events, event) },
		ApprovalHandler:  fileApprovalHandler{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()

	if _, err := session.Run(context.Background(), falken.RunRequest{Prompt: "edit note"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readText(t, target); got != "hello world\n" {
		t.Fatalf("file content = %q, want unchanged", got)
	}
	result := lastToolResult(t, events)
	if result.Name != "edit_file" || !strings.Contains(result.Error, "read token") {
		t.Fatalf("tool result = %+v, want missing read-token failure", result)
	}
}

func TestSessionE2E_FileToolStaleReadTokenFails(t *testing.T) {
	workspace := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	target := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	llm := &scriptedFileLLM{
		responses: []falken.CompletionResponse{
			{ToolCalls: []falken.ToolCall{{ID: "read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"note.txt"}`)}}, FinishReason: falken.FinishReasonToolCalls},
			{ToolCalls: []falken.ToolCall{{ID: "edit-1", Name: "edit_file", Arguments: json.RawMessage(`{"path":"note.txt","old":"world","new":"falken"}`)}}, FinishReason: falken.FinishReasonToolCalls},
			{AssistantText: "stale edit attempted", FinishReason: falken.FinishReasonStop},
		},
		beforeResponse: func(turn int) {
			if turn == 1 {
				if err := os.WriteFile(target, []byte("hello stale\n"), 0o600); err != nil {
					t.Fatalf("make token stale: %v", err)
				}
			}
		},
	}
	var events []falken.Event
	session, err := falken.New(falken.Config{
		WorkspaceDir:     workspace,
		StateDir:         state,
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              llm,
		Events:           func(event falken.Event) { events = append(events, event) },
		ApprovalHandler:  fileApprovalHandler{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()

	if _, err := session.Run(context.Background(), falken.RunRequest{Prompt: "edit note"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readText(t, target); got != "hello stale\n" {
		t.Fatalf("file content = %q, want external stale content preserved", got)
	}
	result := lastToolResult(t, events)
	if result.Name != "edit_file" || (!strings.Contains(result.Error, "stale") && !strings.Contains(result.Error, "differs from read token")) {
		t.Fatalf("tool result = %+v, want stale read-token failure", result)
	}
}

type fileApprovalHandler struct{}

func (fileApprovalHandler) ApproveFile(context.Context, falken.FileRequest) (falken.ApprovalScope, error) {
	return falken.ApprovalScopeOnce, nil
}

func (fileApprovalHandler) ApproveShell(context.Context, falken.ShellRequest) (falken.ApprovalScope, error) {
	return falken.ApprovalScopeDeny, nil
}

func (fileApprovalHandler) ApproveNetwork(context.Context, falken.NetworkRequest) (falken.ApprovalScope, error) {
	return falken.ApprovalScopeDeny, nil
}

type scriptedFileLLM struct {
	responses      []falken.CompletionResponse
	beforeResponse func(turn int)
	requests       []falken.CompletionRequest
	turn           int
}

func (l *scriptedFileLLM) Complete(_ context.Context, request falken.CompletionRequest) (falken.CompletionResponse, error) {
	l.requests = append(l.requests, request)
	if isPublicRoutingRequest(request) {
		return publicRoutingResponse(false, "test default", "high", nil), nil
	}
	turn := l.turn
	l.turn++
	if l.beforeResponse != nil {
		l.beforeResponse(turn)
	}
	if len(l.responses) == 0 {
		return falken.CompletionResponse{AssistantText: "done", FinishReason: falken.FinishReasonStop}, nil
	}
	response := l.responses[0]
	l.responses = l.responses[1:]
	return response, nil
}

func toolResultNames(events []falken.Event) []string {
	names := make([]string, 0)
	for _, event := range events {
		if event.ToolResult != nil {
			names = append(names, event.ToolResult.Name)
		}
	}
	return names
}

func lastToolResult(t *testing.T, events []falken.Event) falken.ToolResult {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].ToolResult != nil {
			return *events[i].ToolResult
		}
	}
	t.Fatal("no tool result event")
	return falken.ToolResult{}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func countRegularFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return count
}
