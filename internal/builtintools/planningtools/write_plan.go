package planningtools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/conversation"
)

// WritePlanTool replaces the current runtime implementation plan.
type WritePlanTool struct{}

func (t *WritePlanTool) Descriptor() api.Descriptor {
	return api.Descriptor{
		Name: "write_plan",
		Description: `Write or replace the current runtime implementation plan.

The plan is stored in Falken internal state, not in the workspace filesystem.
Access it with read_plan; never with read_file.

A valid plan must be a Markdown document covering all four required concerns, with these sections:

  1. Goal         — what the change achieves and why.
  2. Files        — which workspace paths will be created, modified, or deleted.
  3. Changes      — ordered, concrete implementation steps.
  4. Verification — the exact commands or checks you will run to confirm
                    correctness after applying the changes.

Including a "Risks / Rollback" section is strongly recommended for changes
with broad impact or irreversible side effects.

This tool replaces the entire plan and todo list atomically after validating
both. write_todos is for later implementation progress updates, not the initial
planning commit.`,
		Parameters: api.MustSchema(api.ObjectSchema(map[string]any{
			"plan":  api.StringProp("Complete Markdown implementation plan."),
			"todos": todoListSchema("Initial implementation todo list derived from the plan."),
		}, "plan", "todos")),
		Category:    "planning",
		Keywords:    []string{"plan", "write", "draft"},
		AlwaysLoad:  true,
		DefaultLoad: true,
		Safety:      api.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true, MutatesHostState: true},
	}
}

func (t *WritePlanTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	mode, err := host.RequireMode()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	if mode.Current() != agent.ModePlan {
		return api.Fail("blocked_by_mode", "write_plan is only available in plan mode"), nil
	}
	state, err := host.RequireConversationState()
	if err != nil {
		return agent.ToolExecutionResult{}, api.ErrHostUnavailable
	}

	var params struct {
		Plan  string          `json:"plan"`
		Todos json.RawMessage `json:"todos"`
	}
	if err := api.DecodeArgs(args, &params); err != nil {
		return api.Fail("invalid_arguments", err.Error()), nil
	}

	if err := conversation.ValidateImplementationPlan(params.Plan); err != nil {
		return api.Fail("invalid_plan", err.Error()), nil
	}
	parsedTodos, err := decodeTodos(params.Todos)
	if err != nil {
		return api.Fail("invalid_todos", err.Error()), nil
	}
	if len(parsedTodos) == 0 {
		return api.Fail("invalid_todos", "todos must contain at least one item"), nil
	}
	if err := state.WritePlanAndTodos(params.Plan, parsedTodos); err != nil {
		return agent.ToolExecutionResult{}, err
	}

	return api.Success("Plan written successfully.", map[string]any{
		"success":                  true,
		"bytes_written":            len([]byte(params.Plan)),
		"plan_written":             true,
		"todos_written":            true,
		"plan_valid":               true,
		"todos_valid":              true,
		"ready_for_implementation": true,
	})
}
