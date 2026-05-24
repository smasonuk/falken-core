package planningtools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/conversation"
)

// ReadTodosTool reads the current runtime todo list from agent state.
type ReadTodosTool struct{}

func (t *ReadTodosTool) Descriptor() api.Descriptor {
	return api.Descriptor{
		Name: "read_todos",
		Description: `Read the current runtime todo list.

Todos are stored in Falken internal state, not in the workspace filesystem.
Use this tool to recover implementation progress after plan mode exits or
after history compaction.`,
		Parameters:  api.MustSchema(api.ObjectSchema(map[string]any{})),
		Category:    "planning",
		Keywords:    []string{"todos", "read", "progress"},
		AlwaysLoad:  true,
		DefaultLoad: true,
		Safety:      api.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true},
	}
}

func (t *ReadTodosTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	var params struct{}
	if err := api.DecodeArgs(args, &params); err != nil {
		return api.Fail("invalid_arguments", err.Error()), nil
	}

	state, err := host.RequireConversationState()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	current, err := state.ReadTodos()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}

	content := conversation.RenderTodos(current)
	return api.Success(content, map[string]any{
		"success": true,
		"todos":   current,
		"empty":   len(current) == 0,
	})
}
