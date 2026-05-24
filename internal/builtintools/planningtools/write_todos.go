package planningtools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
)

// WriteTodosTool replaces the current runtime todo list.
type WriteTodosTool struct{}

func (t *WriteTodosTool) Descriptor() api.Descriptor {
	return api.Descriptor{
		Name: "write_todos",
		Description: `Replace the current runtime todo list.

Use this tool as a progress checkpoint during implementation, not just at the
end. After a successful tool result completes the current in_progress todo,
call write_todos immediately before starting unrelated work or the next todo.

Do not defer completed status updates until submit_plan_implementation. The
expected pattern is:
1. mark one todo in_progress,
2. do the work,
3. mark it completed immediately,
4. mark the next todo in_progress.

Todos are stored in Falken internal state, not in the workspace filesystem.
This tool is for implementation progress updates after the initial plan commit;
write_plan is responsible for writing the initial plan and todo list together.`,
		Parameters: api.MustSchema(api.ObjectSchema(map[string]any{
			"todos": todoListSchema("Complete replacement todo list. An empty list intentionally clears todos."),
		}, "todos")),
		Category:    "planning",
		Keywords:    []string{"todos", "write", "progress"},
		AlwaysLoad:  true,
		DefaultLoad: true,
		Safety:      api.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true, MutatesHostState: true},
	}
}

func (t *WriteTodosTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	var params struct {
		Todos json.RawMessage `json:"todos"`
	}
	if err := api.DecodeArgs(args, &params); err != nil {
		return api.Fail("invalid_arguments", err.Error()), nil
	}

	state, err := host.RequireConversationState()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	parsedTodos, err := decodeTodos(params.Todos)
	if err != nil {
		return api.Fail("invalid_todos", err.Error()), nil
	}

	if err := state.ReplaceTodos(parsedTodos); err != nil {
		return agent.ToolExecutionResult{}, err
	}

	return api.Success("Todos written successfully.", map[string]any{
		"success":       true,
		"todos_written": true,
		"todos_valid":   true,
		"todos":         parsedTodos,
	})
}
