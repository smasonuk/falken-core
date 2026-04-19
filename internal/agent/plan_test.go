package agent

import (
	"context"
	"os"
	"path/filepath"
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
