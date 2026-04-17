package agent

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/host"
	"github.com/smasonuk/falken-core/internal/permissions"
	"github.com/smasonuk/falken-core/internal/runtimeapi"
	"github.com/smasonuk/falken-core/internal/tasks"

	openai "github.com/sashabaranov/go-openai"
)

type delegateTestTool struct {
	name string
}

func (t delegateTestTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{Name: t.name}
}

func TestDelegateTaskTool_FailedSandboxBootstrapMarksTaskFailed(t *testing.T) {
	workspace := t.TempDir()
	paths, err := runtimeapi.NewPaths(workspace, t.TempDir())
	if err != nil {
		t.Fatalf("NewPaths failed: %v", err)
	}
	if err := paths.EnsureStateDirs(); err != nil {
		t.Fatalf("EnsureStateDirs failed: %v", err)
	}

	permMgr := permissions.NewManager(nil, nil)
	shell := host.NewStatefulShell(paths.WorkspaceDir, paths.StateDir, permMgr, log.New(io.Discard, "", 0))

	runner := &Runner{
		Shell:        shell,
		Paths:        paths,
		taskStore:    tasks.NewTaskStore(paths.TasksPath()),
		logger:       log.New(io.Discard, "", 0),
		ToolDir:      filepath.Join(paths.WorkspaceDir, "tools"),
		PluginDir:    filepath.Join(paths.WorkspaceDir, "plugins"),
		SandboxImage: "falken/definitely-missing-test-image:does-not-exist",
	}

	tool := &delegateTaskTool{runner: runner}
	result, err := tool.Run(context.Background(), map[string]any{
		"Instructions":   "inspect authentication flow",
		"RequiredOutput": "summary",
		"Profile":        "explore",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result["status"] != "launch_failed" {
		t.Fatalf("expected launch_failed status, got %#v", result["status"])
	}
	taskID, _ := result["task_id"].(string)
	if taskID == "" {
		t.Fatal("expected task_id in result")
	}

	task, err := runner.taskStore.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task.Status != tasks.StatusFailed {
		t.Fatalf("expected task status failed, got %s", task.Status)
	}
	if !strings.Contains(task.LastError, "sandbox") {
		t.Fatalf("expected sandbox error, got %q", task.LastError)
	}
}

func TestDelegatedTaskHistoryStripsTrailingAssistantAndAppendsDirective(t *testing.T) {
	parent := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system"},
		{Role: openai.ChatMessageRoleUser, Content: "user"},
		{Role: openai.ChatMessageRoleAssistant, Content: "assistant"},
	}

	got := delegatedTaskHistory(parent, "inspect auth", "summary")
	if len(got) != 3 {
		t.Fatalf("expected 3 messages after stripping and appending, got %d", len(got))
	}
	if got[len(got)-1].Role != openai.ChatMessageRoleUser {
		t.Fatalf("expected final directive to be user role, got %s", got[len(got)-1].Role)
	}
	if !strings.Contains(got[len(got)-1].Content, "inspect auth") || !strings.Contains(got[len(got)-1].Content, "summary") {
		t.Fatalf("expected directive to include task details, got %q", got[len(got)-1].Content)
	}
}

func TestDelegatedTaskToolFilteringHelpers(t *testing.T) {
	allTools := []any{
		delegateTestTool{name: "read_file"},
		delegateTestTool{name: "read_files"},
		delegateTestTool{name: "glob"},
		delegateTestTool{name: "grep"},
		delegateTestTool{name: "search_tools"},
		delegateTestTool{name: "write_file"},
		delegateTestTool{name: "execute_command"},
	}

	exploreNames := toolNames(filterDelegatedTools(allTools, "explore", nil))
	if !reflect.DeepEqual(exploreNames, []string{"read_file", "read_files", "glob", "grep", "search_tools"}) {
		t.Fatalf("unexpected explore tools: %#v", exploreNames)
	}

	verifyNames := toolNames(verificationTools(allTools))
	if !reflect.DeepEqual(verifyNames, []string{"read_file", "read_files", "glob", "grep"}) {
		t.Fatalf("unexpected verification tools: %#v", verifyNames)
	}
}

func toolNames(tools []any) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		runnableTool, ok := tool.(interface {
			Definition() openai.FunctionDefinition
		})
		if !ok {
			continue
		}
		names = append(names, runnableTool.Definition().Name)
	}
	return names
}
