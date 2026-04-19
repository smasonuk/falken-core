package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/runtimeapi"
	"github.com/smasonuk/falken-core/internal/tasks"

	"github.com/sashabaranov/go-openai"
)

func assertNoInternalPathLeak(t *testing.T, v any) {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	s := string(data)
	forbiddenMarkers := []string{
		"plan_path",
		"result_path",
		".falken/state",
		".falken/cache",
		"/state/current/",
		"/cache/",
	}

	for _, forbidden := range forbiddenMarkers {
		if strings.Contains(s, forbidden) {
			t.Fatalf("tool output leaked forbidden marker %q:\n%s", forbidden, s)
		}
	}
}

func TestRefreshMemoryPromptDoesNotExposeInternalPaths(t *testing.T) {
	tmp := t.TempDir()

	paths, err := runtimeapi.NewPaths(tmp, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureStateDirs(false); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		systemPrompt: "system",
		memoryStore:  NewMemoryStore(paths.MemoryPath()),
		History: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "system"},
		},
	}

	mem := &AgentMemory{
		CurrentGoal:    "test goal",
		ImportantFiles: []string{"internal/agent/memory.go"},
	}
	if err := r.memoryStore.Write(mem); err != nil {
		t.Fatal(err)
	}

	h := &historyManager{}
	h.refreshMemoryPrompt(r)

	content := r.History[0].Content
	forbiddenMarkers := []string{
		"plan_path",
		".falken/state",
		".falken/cache",
		paths.PlanPath(),
	}

	for _, forbidden := range forbiddenMarkers {
		if forbidden == "" {
			continue
		}
		if strings.Contains(content, forbidden) {
			t.Fatalf("system prompt leaked forbidden internal path marker %q:\n%s", forbidden, content)
		}
	}
}

func TestPlanToolsDoNotLeakPaths(t *testing.T) {
	tmp := t.TempDir()
	paths, _ := runtimeapi.NewPaths(tmp, tmp)
	_ = paths.EnsureStateDirs(false)

	r := &Runner{
		Paths:     paths,
		planStore: NewPlanStore(paths.PlanPath()),
	}

	ctx := context.Background()

	t.Run("WritePlan", func(t *testing.T) {
		tool := &WritePlanTool{runner: r}
		res, err := tool.Run(ctx, map[string]any{"Content": "# My Plan\nWith enough content to be valid."})
		if err != nil {
			t.Fatal(err)
		}
		assertNoInternalPathLeak(t, res)
	})

	t.Run("ReadPlan", func(t *testing.T) {
		tool := &ReadPlanTool{runner: r}
		res, err := tool.Run(ctx, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		assertNoInternalPathLeak(t, res)
		if !strings.Contains(res["result"].(string), "# My Plan") {
			t.Fatalf("expected plan content not found: %v", res["result"])
		}
	})
}

func TestTaskToolsDoNotLeakPaths(t *testing.T) {
	tmp := t.TempDir()
	paths, _ := runtimeapi.NewPaths(tmp, tmp)
	_ = paths.EnsureStateDirs(false)

	store := tasks.NewTaskStore(paths.TasksPath())
	id, _ := store.CreateTask("subagent", "test task", "description", nil, "")

	// Manually set paths as if a subagent finished
	taskDir := filepath.Join(paths.TasksDir(), id)
	os.MkdirAll(taskDir, 0755)
	resultPath := filepath.Join(taskDir, "result.md")
	planPath := filepath.Join(taskDir, "plan.md")
	os.WriteFile(resultPath, []byte("Task result content"), 0644)
	os.WriteFile(planPath, []byte("Task plan content"), 0644)

	patch := tasks.TaskPatch{
		ResultPath: &resultPath,
		PlanPath:   &planPath,
	}
	_ = store.UpdateTask(id, patch)

	t.Run("TaskList", func(t *testing.T) {
		tool := &TaskListTool{store: store}
		res, err := tool.Run(context.Background(), map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		assertNoInternalPathLeak(t, res)

		// Verify has_result and has_plan are present
		found := false
		for _, group := range []string{"ready_tasks", "blocked_tasks", "in_progress_tasks", "completed_tasks", "other_tasks"} {
			tasks := res[group].([]map[string]any)
			for _, task := range tasks {
				if task["id"] == id {
					found = true
					if task["has_result"] != true || task["has_plan"] != true {
						t.Errorf("expected has_result/has_plan to be true, got %v/%v", task["has_result"], task["has_plan"])
					}
				}
			}
		}
		if !found {
			t.Fatal("task not found in list")
		}
	})

	t.Run("TaskGet", func(t *testing.T) {
		tool := &TaskGetTool{store: store}
		res, err := tool.Run(context.Background(), map[string]any{"ID": id})
		if err != nil {
			t.Fatal(err)
		}
		assertNoInternalPathLeak(t, res)
		if res["result"] != "Task result content" {
			t.Errorf("expected result content, got %v", res["result"])
		}
		if res["plan"] != "Task plan content" {
			t.Errorf("expected plan content, got %v", res["plan"])
		}
	})

	t.Run("TaskUpdateDefinition", func(t *testing.T) {
		tool := &TaskUpdateTool{store: store}
		def := tool.Definition()
		assertNoInternalPathLeak(t, def)
	})
}

func TestToolExecutorTruncationSanitization(t *testing.T) {
	tmp := t.TempDir()
	paths, _ := runtimeapi.NewPaths(tmp, tmp)
	_ = paths.EnsureStateDirs(false)

	r := &Runner{
		Paths:       paths,
		ActiveTools: make(map[string]any),
	}

	// Create a tool that returns large output
	largeOutput := strings.Repeat("A", 20000)
	r.ActiveTools["large_tool"] = &testLargeTool{output: largeOutput}

	executor := &toolExecutor{}
	tc := openai.ToolCall{
		ID: "call_123",
		Function: openai.FunctionCall{
			Name:      "large_tool",
			Arguments: "{}",
		},
	}

	eventChan := make(chan any, 10)
	msg := executor.executeToolCall(r, context.Background(), tc, eventChan)

	assertNoInternalPathLeak(t, msg)
	if !strings.Contains(msg.Content, "tool_output_call_123") {
		t.Errorf("expected artifact ID in truncated output, got %s", msg.Content)
	}
}

type testLargeTool struct {
	output string
}

func (t *testLargeTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{Name: "large_tool"}
}
func (t *testLargeTool) Run(ctx context.Context, args any) (map[string]any, error) {
	return map[string]any{"result": t.output}, nil
}
