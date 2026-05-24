package planningtools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
)

// ReadPlanTool reads the current runtime implementation plan from agent state.
type ReadPlanTool struct{}

func (t *ReadPlanTool) Descriptor() api.Descriptor {
	return api.Descriptor{
		Name: "read_plan",
		Description: `Read the current runtime implementation plan.

The plan is stored in Falken internal state, not in the workspace filesystem.
It cannot be accessed via read_file. Use this tool whenever you need to review
or continue from an existing plan.`,
		Parameters:  api.MustSchema(api.ObjectSchema(map[string]any{})),
		Category:    "planning",
		Keywords:    []string{"plan", "read", "view"},
		AlwaysLoad:  true,
		DefaultLoad: true,
		Safety:      api.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true},
	}
}

func (t *ReadPlanTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	var params struct{}
	if err := api.DecodeArgs(args, &params); err != nil {
		return api.Fail("invalid_arguments", err.Error()), nil
	}

	state, err := host.RequireConversationState()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	plan, err := state.ReadPlan()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}

	content := plan
	if content == "" {
		content = "(no plan has been written yet)"
	}

	return api.Success(content, map[string]any{
		"success": true,
		"plan":    plan,
		"empty":   plan == "",
	})
}
