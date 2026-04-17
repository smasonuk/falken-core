package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/smasonuk/falken-core/internal/runtimeapi"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// EnterPlanModeTool
type EnterPlanModeTool struct {
	runner *Runner
}

func (t *EnterPlanModeTool) Name() string { return "enter_plan_mode" }
func (t *EnterPlanModeTool) Description() string {
	return `Requests to enter plan mode for complex tasks requiring exploration and design.

CRITICAL USAGE RULES:
1. Read-Only State: Entering this mode instantly disables your ability to edit files or run shell commands. 
2. Goal: Your objective is to use 'glob', 'grep', and 'read_file' to explore the codebase, then use 'write_file' to write a Markdown architecture plan to '.agent_plan.md'.
3. When to Use: Mandatory for multi-file features, complex refactors, or anytime you are unfamiliar with the codebase structure. Do not use for simple 1-line bug fixes.`
}
func (t *EnterPlanModeTool) IsLongRunning() bool { return false }

func (t *EnterPlanModeTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"Reason": {
					Type:        jsonschema.String,
					Description: "The reason why you are entering plan mode.",
				},
			},
		},
	}
}

func (t *EnterPlanModeTool) Run(ctx context.Context, args any) (map[string]any, error) {
	t.runner.mu.Lock()
	if t.runner.Mode == ModePlan {
		t.runner.mu.Unlock()
		return map[string]any{"result": "You are already in plan mode. Proceed with exploration and write your plan to .agent_plan.md."}, nil
	}

	t.runner.Mode = ModePlan
	t.runner.PlanInitiator = PlanInitiatorAgent
	t.runner.mu.Unlock()

	// Initialize/clear the scratchpad file
	os.WriteFile(".agent_plan.md", []byte("# Implementation Plan\n\n"), 0644)

	return map[string]any{
		"result": `Entered plan mode. You should now focus on exploring the codebase and designing an implementation approach.
    
    In plan mode, you should:
    1. Thoroughly explore the codebase using read tools.
    2. Consider multiple approaches and their trade-offs.
    3. Write your concrete implementation strategy to the '.agent_plan.md' file.
    4. When your plan is fully written to that file, use the 'exit_plan_mode' tool.
    
    Remember: DO NOT write or edit any other files yet. This is a read-only exploration phase.`,
	}, nil
}

// ExitPlanModeTool
type ExitPlanModeTool struct {
	runner *Runner
}

func (t *ExitPlanModeTool) Name() string { return "exit_plan_mode" }
func (t *ExitPlanModeTool) Description() string {
	return "Exits plan mode so you can start coding. Use this ONLY after you have written your complete plan to .agent_plan.md."
}
func (t *ExitPlanModeTool) IsLongRunning() bool { return false }

func (t *ExitPlanModeTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"Reason": {
					Type:        jsonschema.String,
					Description: "A brief summary of the plan you have documented.",
				},
			},
		},
	}
}

func (t *ExitPlanModeTool) Run(ctx context.Context, args any) (map[string]any, error) {
	if t.runner.Mode != ModePlan {
		return map[string]any{"error": "You are not in plan mode. Continue with your task."}, nil
	}

	planPath := ".agent_plan.md"
	planBytes, err := os.ReadFile(planPath)
	if err != nil || len(planBytes) == 0 {
		return map[string]any{"error": "Could not read plan file or file is empty. Please write your plan to " + planPath + " first."}, nil
	}

	planContent := string(planBytes)

	if len(planContent) < 100 {
		return map[string]any{"error": "Plan is too short. Please write a detailed architectural plan with Files, Changes, and Verification sections."}, nil
	}

	lowerPlan := strings.ToLower(planContent)
	hasFiles := strings.Contains(lowerPlan, "file") || strings.Contains(lowerPlan, "path")
	hasChanges := strings.Contains(lowerPlan, "change") || strings.Contains(lowerPlan, "implement") || strings.Contains(lowerPlan, "step")
	hasVerification := strings.Contains(lowerPlan, "verif") || strings.Contains(lowerPlan, "test") || strings.Contains(lowerPlan, "check")

	if !hasFiles || !hasChanges || !hasVerification {
		return map[string]any{"error": "Plan is missing required sections. Ensure it contains explicitly labeled sections or clear content for: Files (target paths), Changes (concrete steps), and Verification (how to test)."}, nil
	}

	updateMemoryOnPlanExit := func() {
		if mem, err := t.runner.memoryStore.Read(); err == nil {
			mem.PlanPath = planPath
			mem.ImportantFiles = mergeUniqueStrings(mem.ImportantFiles, []string{planPath})
			mem.Decisions = mergeUniqueStrings(mem.Decisions, []string{"Formulated implementation plan in " + planPath})
			t.runner.memoryStore.Write(mem)
		}
	}

	if t.runner.PlanInitiator == PlanInitiatorUser {
		if t.runner.Interactions == nil {
			return map[string]any{"error": "Internal error: could not send plan approval request."}, nil
		}

		response, err := t.runner.Interactions.RequestPlanApproval(ctx, runtimeapi.PlanApprovalRequest{
			Plan: planContent,
		})
		if err != nil {
			return map[string]any{"error": err.Error()}, nil
		}

		if response.Approved {
			t.runner.mu.Lock()
			t.runner.Mode = ModeDefault
			t.runner.PlanInitiator = ""
			t.runner.mu.Unlock()
			updateMemoryOnPlanExit()
			return map[string]any{"result": fmt.Sprintf("User has approved your plan. You can now start coding.\n\nApproved Plan:\n%s", planContent)}, nil
		} else {
			return map[string]any{"result": fmt.Sprintf("User rejected the plan with the following feedback:\n%s\n\nPlease update the plan in %s based on this feedback and call exit_plan_mode again.", response.Feedback, planPath)}, nil
		}
	} else {
		// Agent initiated plan mode, auto-approve
		t.runner.mu.Lock()
		t.runner.Mode = ModeDefault
		t.runner.PlanInitiator = ""
		t.runner.mu.Unlock()
		updateMemoryOnPlanExit()
		return map[string]any{"result": fmt.Sprintf("Plan successfully documented. You have exited plan mode and may now begin executing the plan.\n\nPlan:\n%s", planContent)}, nil
	}
}
