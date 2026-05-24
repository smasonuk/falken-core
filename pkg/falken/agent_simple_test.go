package falken

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	internalagent "github.com/smasonuk/falken-core/internal/agent"
)

func TestNewAgentToolOnlyExposesOnlySuppliedTools(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	tool := ToolFunc(testNonWorkspaceToolDescriptor("lookup_user"), func(context.Context, ToolInvocation) (ToolExecutionResult, error) {
		return ToolExecutionResult{Success: true, Status: "ok", Payload: json.RawMessage(`{"ok":true}`)}, nil
	})
	simpleAgent, err := NewAgent(context.Background(), AgentConfig{
		LLM:          llm,
		SystemPrompt: "Help with accounts.",
		Tools:        []Tool{tool},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer simpleAgent.Close(context.Background())

	answer, err := simpleAgent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "done" {
		t.Fatalf("answer = %q, want done", answer)
	}
	if got := sessionAgentToolNames(llm.requests[0].Tools); !reflect.DeepEqual(got, []string{"lookup_user"}) {
		t.Fatalf("tool-only tools = %v, want lookup_user only", got)
	}
}

func TestNewAgentReadDirectoryExposesReadOnlyFileTools(t *testing.T) {
	setStateHomeEnv(t)

	docs := tempWorkspace(t)
	writeFileInTest(t, filepath.Join(docs, "guide.md"), "hello")
	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "docs", FinishReason: FinishReasonStop}}}
	simpleAgent, err := NewAgent(context.Background(), AgentConfig{
		LLM:           llm,
		SystemPrompt:  "Use the docs.",
		ReadDirectory: docs,
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer simpleAgent.Close(context.Background())
	if _, err := simpleAgent.Run(context.Background(), "summarize"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := sessionAgentToolNames(llm.requests[0].Tools)
	if !reflect.DeepEqual(got, BuiltinReadOnlyFileTools) {
		t.Fatalf("read-directory tools = %v, want %v", got, BuiltinReadOnlyFileTools)
	}
	for _, blocked := range []string{"write_file", "execute_command", "read_plan", "read_memory"} {
		if sessionAgentHasTool(got, blocked) {
			t.Fatalf("read-directory tools = %v, should not expose %s", got, blocked)
		}
	}
}

func TestNewAgentRunStartsAutomaticallyAndDoesNotWriteConversationFiles(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "ready", FinishReason: FinishReasonStop}}}
	simpleAgent, err := NewAgent(context.Background(), AgentConfig{LLM: llm})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer simpleAgent.Close(context.Background())

	if got, err := simpleAgent.Run(context.Background(), "hi"); err != nil || got != "ready" {
		t.Fatalf("Run = %q/%v, want ready", got, err)
	}
	for _, path := range []string{
		simpleAgent.session.Paths.HistoryPath,
		simpleAgent.session.Paths.MemoryPath,
		simpleAgent.session.Paths.TodosPath,
		simpleAgent.session.Paths.PlanPath,
		simpleAgent.session.Paths.CommandEvidencePath,
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("conversation state file %q stat error = %v, want no file-backed state", path, err)
		}
	}
}

func TestAgentCloseIsIdempotent(t *testing.T) {
	setStateHomeEnv(t)

	simpleAgent, err := NewAgent(context.Background(), AgentConfig{LLM: noopLLM{}})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if err := simpleAgent.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := simpleAgent.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := simpleAgent.Run(context.Background(), "after close"); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Run after Close error = %v, want ErrSessionClosed", err)
	}
}

func TestNewAgentAllowWriteFilesAddsMutationTools(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	simpleAgent, err := NewAgent(context.Background(), AgentConfig{
		LLM:           llm,
		ReadDirectory: tempWorkspace(t),
		Permissions:   SimplePermissions{AllowWriteFiles: true},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer simpleAgent.Close(context.Background())
	if _, err := simpleAgent.Run(context.Background(), "tools"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := sessionAgentToolNames(llm.requests[0].Tools)
	for _, want := range []string{"read_file", "write_file", "edit_file", "multi_edit", "apply_patch", "delete_file"} {
		if !sessionAgentHasTool(got, want) {
			t.Fatalf("tools = %v, missing %s", got, want)
		}
	}
	if sessionAgentHasTool(got, "execute_command") {
		t.Fatalf("tools = %v, should not expose execute_command without AllowShell", got)
	}
}

func TestNewAgentAllowShellAddsCommandTool(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	simpleAgent, err := NewAgent(context.Background(), AgentConfig{
		LLM:         llm,
		Permissions: SimplePermissions{AllowShell: true},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer simpleAgent.Close(context.Background())
	if _, err := simpleAgent.Run(context.Background(), "tools"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sessionAgentToolNames(llm.requests[0].Tools); !reflect.DeepEqual(got, []string{"execute_command"}) {
		t.Fatalf("shell tools = %v, want execute_command only", got)
	}
}

func TestNewAgentDisabledBuiltinsAreInactive(t *testing.T) {
	setStateHomeEnv(t)

	simpleAgent, err := NewAgent(context.Background(), AgentConfig{
		LLM:           noopLLM{},
		ReadDirectory: tempWorkspace(t),
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer simpleAgent.Close(context.Background())

	executor := sessionToolExecutor{hub: simpleAgent.session.resources.toolHub, runtime: simpleAgent.session.resources.runtime}
	for _, name := range []string{"write_file", "execute_command"} {
		_, err := executor.ExecuteTool(context.Background(), internalagent.ToolExecutionRequest{Name: name})
		if err == nil || !strings.Contains(err.Error(), "tool is not active or unknown") {
			t.Fatalf("ExecuteTool %s error = %v, want inactive built-in", name, err)
		}
	}
}
