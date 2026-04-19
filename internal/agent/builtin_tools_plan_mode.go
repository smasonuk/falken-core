package agent

import (
	"context"
	"fmt"
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
2. Goal: Your objective is to use 'glob', 'grep', and 'read_file' to explore the codebase, then use 'write_plan' to write a Markdown architecture plan into Falken runtime state.
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
	store, errResult := planStoreForRunner(t.runner)
	if errResult != nil {
		return errResult, nil
	}

	t.runner.mu.Lock()
	if t.runner.Mode == ModePlan {
		t.runner.mu.Unlock()
		return map[string]any{"result": "You are already in plan mode. Proceed with exploration and write your plan using the write_plan tool."}, nil
	}

	t.runner.Mode = ModePlan
	t.runner.PlanInitiator = PlanInitiatorAgent
	t.runner.mu.Unlock()

	if err := store.Write("# Implementation Plan\n\n"); err != nil {
		t.runner.mu.Lock()
		t.runner.Mode = ModeDefault
		t.runner.PlanInitiator = ""
		t.runner.mu.Unlock()
		return map[string]any{"error": "Failed to initialize plan: " + err.Error()}, nil
	}

	return map[string]any{
		"result": "Entered plan mode. You should now focus on read-only exploration and architecture planning.\n\n" +
			"In plan mode:\n" +
			"1. Use read tools such as glob, grep, read_file, and read_files to understand the codebase.\n" +
			"2. Do not edit workspace files or run shell commands.\n" +
			"3. Write the plan using write_plan, not write_file.\n" +
			"4. The plan must include these sections: Goal, Files, Changes, Verification, and Risks / Rollback.\n" +
			"5. When the plan is complete, call exit_plan_mode.\n",
	}, nil
}

// ExitPlanModeTool
type ExitPlanModeTool struct {
	runner *Runner
}

func (t *ExitPlanModeTool) Name() string { return "exit_plan_mode" }
func (t *ExitPlanModeTool) Description() string {
	return "Exits plan mode so you can start coding. Use this ONLY after you have written your complete plan with 'write_plan'."
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
	store, errResult := planStoreForRunner(t.runner)
	if errResult != nil {
		return errResult, nil
	}

	if t.runner.Mode != ModePlan {
		return map[string]any{"error": "You are not in plan mode. Continue with your task."}, nil
	}

	planPath := store.Path()
	planContent, err := store.Read()
	if err != nil || strings.TrimSpace(planContent) == "" {
		return map[string]any{"error": "Could not read plan or plan is empty. Please write your plan first using write_plan."}, nil
	}

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
			mem.Decisions = mergeUniqueStrings(mem.Decisions, []string{"Formulated implementation plan"})
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
			return map[string]any{"result": fmt.Sprintf("User rejected the plan with the following feedback:\n%s\n\nPlease update the plan using write_plan based on this feedback and call exit_plan_mode again.", response.Feedback)}, nil
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

// WritePlanTool is the only write operation allowed during plan mode.
type WritePlanTool struct {
	runner *Runner
}

func (t *WritePlanTool) Name() string { return "write_plan" }
func (t *WritePlanTool) Description() string {
	return `Writes or replaces the current runtime implementation plan.

Use this in plan mode after exploring the codebase. The plan should be Markdown and should include:
- Goal
- Files
- Changes
- Verification
- Risks / Rollback

This tool writes to Falken internal runtime state, not to the workspace. Do not use write_file for implementation plans.

This tool can only write the current implementation plan. It cannot write arbitrary files or artifacts.`
}
func (t *WritePlanTool) IsLongRunning() bool { return false }

func (t *WritePlanTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"Content": {
					Type:        jsonschema.String,
					Description: "The full Markdown content of the implementation plan.",
				},
			},
			Required: []string{"Content"},
		},
	}
}

func (t *WritePlanTool) Run(ctx context.Context, args any) (map[string]any, error) {
	store, errResult := planStoreForRunner(t.runner)
	if errResult != nil {
		return errResult, nil
	}

	m, ok := args.(map[string]any)
	if !ok {
		return map[string]any{"error": "Invalid arguments"}, nil
	}
	content, _ := m["Content"].(string)
	if strings.TrimSpace(content) == "" {
		return map[string]any{"error": "Content cannot be empty"}, nil
	}

	if err := store.Write(content); err != nil {
		return map[string]any{"error": "Failed to write plan: " + err.Error()}, nil
	}

	return map[string]any{
		"result": "Plan written successfully.",
		"path":   store.Path(),
	}, nil
}

// ReadPlanTool
type ReadPlanTool struct {
	runner *Runner
}

func (t *ReadPlanTool) Name() string { return "read_plan" }
func (t *ReadPlanTool) Description() string {
	return "Reads the current runtime implementation plan from Falken internal state."
}
func (t *ReadPlanTool) IsLongRunning() bool { return false }

func (t *ReadPlanTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"IncludePath": {
					Type:        jsonschema.Boolean,
					Description: "Whether to include the internal state path in the result. Defaults to true.",
				},
			},
		},
	}
}

func (t *ReadPlanTool) Run(ctx context.Context, args any) (map[string]any, error) {
	store, errResult := planStoreForRunner(t.runner)
	if errResult != nil {
		return errResult, nil
	}

	content, err := store.Read()
	if err != nil {
		return map[string]any{"error": "Failed to read plan: " + err.Error()}, nil
	}

	includePath := true
	if m, ok := args.(map[string]any); ok {
		if val, ok := m["IncludePath"].(bool); ok {
			includePath = val
		}
	}

	if content == "" {
		res := map[string]any{"result": "No plan has been written yet."}
		if includePath {
			res["path"] = store.Path()
		}
		return res, nil
	}

	res := map[string]any{"result": content}
	if includePath {
		res["path"] = store.Path()
	}
	return res, nil
}

// planStoreForRunner returns the plan store for the runner, or a structured error result
// if the runner or its plan store is not initialized.
func planStoreForRunner(r *Runner) (*PlanStore, map[string]any) {
	if r == nil {
		return nil, map[string]any{"error": "Runner is not initialized"}
	}
	if r.planStore == nil {
		return nil, map[string]any{"error": "Plan store is not initialized"}
	}
	return r.planStore, nil
}
