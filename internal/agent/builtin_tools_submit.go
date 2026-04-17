package agent

import (
	"context"

	"github.com/smasonuk/falken-core/internal/runtimeapi"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

type SubmitTaskTool struct {
	runner *Runner
}

func (t *SubmitTaskTool) Name() string { return "submit_task" }
func (t *SubmitTaskTool) Description() string {
	return `Submits your completed work for human review and ends your current execution loop.

CRITICAL USAGE RULES:
1. Verification Required: You MUST NOT call this tool unless you have successfully run tests, linters, or compiled the code via 'execute_command' to prove your solution works.
2. Comprehensive Summary: Your 'summary' must explicitly state: 
   - What files were changed.
   - The logic/reasoning behind the changes.
   - The exact commands you ran to verify it works, and their results.
3. Terminal Action: Calling this ends your current session. Do not call this if you still have pending, unblocked tasks in your TaskList.`
}
func (t *SubmitTaskTool) IsLongRunning() bool { return false }

func (t *SubmitTaskTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"summary": {Type: jsonschema.String, Description: "A summary of the changes made."},
			},
			Required: []string{"summary"},
		},
	}
}

func (t *SubmitTaskTool) Run(ctx context.Context, args any) (map[string]any, error) {
	summary := ""
	if m, ok := args.(map[string]any); ok {
		summary, _ = m["summary"].(string)
	}

	if t.runner.Interactions != nil {
		if err := t.runner.Interactions.OnSubmit(ctx, runtimeapi.SubmitRequest{Summary: summary}); err != nil {
			return map[string]any{"error": err.Error()}, nil
		}
	}
	return map[string]any{"result": "Task submitted for human review."}, nil
}
