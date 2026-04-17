package agent

import (
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestModePolicy_BlocksWriteInPlanMode(t *testing.T) {
	policy := &modePolicy{}
	r := &Runner{Mode: ModePlan}

	msg, blocked := policy.blockedToolMessage(r, "write_file", map[string]any{"Path": "foo.txt"})
	if !blocked {
		t.Fatalf("expected write_file to be blocked in plan mode")
	}
	if msg == "" {
		t.Fatalf("expected explanatory error message")
	}
}

func TestModePolicy_AllowsPlanFileInPlanMode(t *testing.T) {
	policy := &modePolicy{}
	r := &Runner{Mode: ModePlan}

	if _, blocked := policy.blockedToolMessage(r, "write_file", map[string]any{"Path": ".agent_plan.md"}); blocked {
		t.Fatalf("expected .agent_plan.md write to be allowed in plan mode")
	}
}

func TestModePolicy_AllowsExecuteCommandInVerifyMode(t *testing.T) {
	policy := &modePolicy{}
	r := &Runner{Mode: ModeVerify}

	if _, blocked := policy.blockedToolMessage(r, "execute_command", map[string]any{}); blocked {
		t.Fatalf("expected execute_command to be allowed in verify mode")
	}
}

func TestModePolicy_ExploreModeToolsAreReadOnly(t *testing.T) {
	policy := &modePolicy{}
	r := &Runner{
		Mode: ModeExplore,
		tools: []openai.Tool{
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "read_file"}},
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "glob"}},
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "execute_command"}},
		},
	}

	filtered := policy.toolsForCurrentMode(r)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 tools in explore mode, got %d", len(filtered))
	}
	for _, tool := range filtered {
		if tool.Function.Name == "execute_command" {
			t.Fatalf("execute_command should not be available in explore mode")
		}
	}
}
