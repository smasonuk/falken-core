package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/runtimeapi"

	"github.com/sashabaranov/go-openai"
)

func TestPaths_PlanPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "falken-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	paths, err := runtimeapi.NewPaths(tmpDir, "")
	if err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(tmpDir, ".falken", "state", "current", "plan.md")
	if paths.PlanPath() != expected {
		t.Errorf("expected %s, got %s", expected, paths.PlanPath())
	}
}

func TestPlanStore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "falken-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	planPath := filepath.Join(tmpDir, "plan.md")
	store := NewPlanStore(planPath)

	// Read before write
	content, err := store.Read()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}

	// Write
	expectedContent := "# My Plan"
	if err := store.Write(expectedContent); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Read after write
	content, err = store.Read()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if content != expectedContent {
		t.Errorf("expected %q, got %q", expectedContent, content)
	}
}

func TestEnterPlanModeTool_InitializesPlan(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "falken-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	paths, _ := runtimeapi.NewPaths(tmpDir, "")
	_ = paths.EnsureStateDirs(false)

	r := &Runner{
		Paths:     paths,
		planStore: NewPlanStore(paths.PlanPath()),
	}

	tool := &EnterPlanModeTool{runner: r}
	_, err = tool.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("tool run failed: %v", err)
	}

	if r.Mode != ModePlan {
		t.Errorf("expected ModePlan, got %s", r.Mode)
	}

	content, _ := r.planStore.Read()
	if content != "# Implementation Plan\n\n" {
		t.Errorf("plan not initialized correctly, got %q", content)
	}

	// Ensure .agent_plan.md was NOT created in CWD
	if _, err := os.Stat(".agent_plan.md"); err == nil {
		t.Errorf(".agent_plan.md should not have been created in CWD")
		os.Remove(".agent_plan.md")
	}
}

func TestExitPlanModeTool_ReadsPlan(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "falken-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	paths, _ := runtimeapi.NewPaths(tmpDir, "")
	_ = paths.EnsureStateDirs(false)

	r := &Runner{
		Mode:          ModePlan,
		PlanInitiator: PlanInitiatorAgent,
		Paths:         paths,
		planStore:     NewPlanStore(paths.PlanPath()),
		memoryStore:   NewMemoryStore(paths.MemoryPath()),
	}

	validPlan := `# Implementation Plan

## Files
- internal/example.go

## Changes
- Implement the required behavior.

## Verification
- Run go test ./...
`
	_ = r.planStore.Write(validPlan)

	tool := &ExitPlanModeTool{runner: r}
	res, err := tool.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("tool run failed: %v", err)
	}

	if errStr, ok := res["error"].(string); ok {
		t.Fatalf("tool returned error: %s", errStr)
	}

	if r.Mode != ModeDefault {
		t.Errorf("expected ModeDefault after exit, got %s", r.Mode)
	}
}

func TestSubRunPaths_PlanPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "falken-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	paths, err := runtimeapi.NewPaths(tmpDir, "")
	if err != nil {
		t.Fatal(err)
	}

	subPaths := paths.SubRunPaths("subagent_123")
	// Sub-run StateDir is under .../runs/<id>; CurrentStateDir detects the /state/current/runs/
	// prefix and returns the run dir itself (no further nesting).
	expected := filepath.Join(tmpDir, ".falken", "state", "current", "runs", "subagent_123", "plan.md")
	if subPaths.PlanPath() != expected {
		t.Errorf("expected %s, got %s", expected, subPaths.PlanPath())
	}
}

func TestPlanStore_ParentDirsCreated(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "falken-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// planPath with nested dirs that don't yet exist
	planPath := filepath.Join(tmpDir, "nested", "dirs", "plan.md")
	store := NewPlanStore(planPath)

	if err := store.Write("hello"); err != nil {
		t.Fatalf("expected Write to create parent dirs, got: %v", err)
	}

	content, err := store.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if content != "hello" {
		t.Errorf("expected %q, got %q", "hello", content)
	}
}

func TestWritePlanTool_WritesToRuntimeStateNotWorkspace(t *testing.T) {
	workspaceDir, err := os.MkdirTemp("", "falken-workspace-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspaceDir)

	paths, err := runtimeapi.NewPaths(workspaceDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureStateDirs(false); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		Paths:     paths,
		planStore: NewPlanStore(paths.PlanPath()),
	}

	tool := &WritePlanTool{runner: r}
	res, err := tool.Run(context.Background(), map[string]any{"Content": "# Goal\n\nTest plan..."})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if errStr, ok := res["error"].(string); ok {
		t.Fatalf("tool returned error: %s", errStr)
	}

	// Plan must exist in runtime state
	if _, err := os.Stat(paths.PlanPath()); err != nil {
		t.Errorf("plan file not created at runtime path: %v", err)
	}

	// Plan must NOT exist as .agent_plan.md in workspace
	workspacePlan := filepath.Join(workspaceDir, ".agent_plan.md")
	if _, err := os.Stat(workspacePlan); err == nil {
		t.Errorf(".agent_plan.md should not have been created in workspace")
	}
}

func TestReadPlanTool_ReadsRuntimeState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "falken-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	paths, _ := runtimeapi.NewPaths(tmpDir, "")
	_ = paths.EnsureStateDirs(false)

	r := &Runner{
		Paths:     paths,
		planStore: NewPlanStore(paths.PlanPath()),
	}

	// No plan yet
	tool := &ReadPlanTool{runner: r}
	res, err := tool.Run(context.Background(), map[string]any{"IncludePath": false})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res["result"] != "No plan has been written yet." {
		t.Errorf("expected no-plan message, got %q", res["result"])
	}

	// Write then read
	_ = r.planStore.Write("# My Plan\n\nsome content")
	res, err = tool.Run(context.Background(), map[string]any{"IncludePath": false})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res["result"] != "# My Plan\n\nsome content" {
		t.Errorf("expected plan content, got %q", res["result"])
	}
}

func TestEnterPlanModeTool_ResultContainsRequiredSections(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "falken-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	paths, _ := runtimeapi.NewPaths(tmpDir, "")
	_ = paths.EnsureStateDirs(false)

	r := &Runner{
		Paths:     paths,
		planStore: NewPlanStore(paths.PlanPath()),
	}

	tool := &EnterPlanModeTool{runner: r}
	res, err := tool.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	result, _ := res["result"].(string)

	for _, want := range []string{"write_plan", "Goal", "Files", "Changes", "Verification", "Risks / Rollback"} {
		if !strings.Contains(result, want) {
			t.Errorf("enter_plan_mode result missing %q", want)
		}
	}
}

func TestEnterPlanModeTool_RollsBackOnWriteFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "falken-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	paths, _ := runtimeapi.NewPaths(tmpDir, "")
	_ = paths.EnsureStateDirs(false)

	// Make plan path a directory so Write will fail
	planPath := paths.PlanPath()
	if err := os.MkdirAll(planPath, 0755); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		Paths:     paths,
		planStore: NewPlanStore(planPath),
	}

	tool := &EnterPlanModeTool{runner: r}
	res, err := tool.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	if _, ok := res["error"]; !ok {
		t.Errorf("expected error result on write failure, got: %v", res)
	}
	if r.Mode != ModeDefault {
		t.Errorf("expected mode to roll back to ModeDefault, got %s", r.Mode)
	}
	if r.PlanInitiator != "" {
		t.Errorf("expected PlanInitiator to be cleared, got %q", r.PlanInitiator)
	}
}

func TestPlanTools_NilRunner(t *testing.T) {
	ctx := context.Background()

	enter := &EnterPlanModeTool{runner: nil}
	res, err := enter.Run(ctx, nil)
	if err != nil || res["error"] == nil {
		t.Errorf("EnterPlanModeTool with nil runner should return error result")
	}

	exit := &ExitPlanModeTool{runner: nil}
	res, err = exit.Run(ctx, nil)
	if err != nil || res["error"] == nil {
		t.Errorf("ExitPlanModeTool with nil runner should return error result")
	}

	write := &WritePlanTool{runner: nil}
	res, err = write.Run(ctx, map[string]any{"Content": "hello"})
	if err != nil || res["error"] == nil {
		t.Errorf("WritePlanTool with nil runner should return error result")
	}

	read := &ReadPlanTool{runner: nil}
	res, err = read.Run(ctx, nil)
	if err != nil || res["error"] == nil {
		t.Errorf("ReadPlanTool with nil runner should return error result")
	}
}

func TestPlanTools_NilPlanStore(t *testing.T) {
	ctx := context.Background()
	r := &Runner{planStore: nil}

	enter := &EnterPlanModeTool{runner: r}
	res, err := enter.Run(ctx, nil)
	if err != nil || res["error"] == nil {
		t.Errorf("EnterPlanModeTool with nil planStore should return error result")
	}

	write := &WritePlanTool{runner: r}
	res, err = write.Run(ctx, map[string]any{"Content": "hello"})
	if err != nil || res["error"] == nil {
		t.Errorf("WritePlanTool with nil planStore should return error result")
	}

	read := &ReadPlanTool{runner: r}
	res, err = read.Run(ctx, nil)
	if err != nil || res["error"] == nil {
		t.Errorf("ReadPlanTool with nil planStore should return error result")
	}
}

func TestModePolicy_Tools(t *testing.T) {
	policy := &modePolicy{}
	r := &Runner{
		Mode: ModePlan,
		tools: []openai.Tool{
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "write_plan"}},
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "read_plan"}},
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "exit_plan_mode"}},
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "write_file"}},
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "execute_command"}},
		},
	}

	filtered := policy.toolsForCurrentMode(r)
	found := make(map[string]bool)
	for _, t := range filtered {
		found[t.Function.Name] = true
	}

	if !found["write_plan"] {
		t.Errorf("write_plan should be allowed in plan mode")
	}
	if !found["read_plan"] {
		t.Errorf("read_plan should be allowed in plan mode")
	}
	if !found["exit_plan_mode"] {
		t.Errorf("exit_plan_mode should be allowed in plan mode")
	}
	if found["write_file"] {
		t.Errorf("write_file should NOT be allowed in plan mode")
	}
	if found["execute_command"] {
		t.Errorf("execute_command should NOT be allowed in plan mode")
	}
}
