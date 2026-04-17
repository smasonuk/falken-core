package agent

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// TODO: rename to agent?
type delegateTaskTool struct {
	runner *Runner
}

func (t *delegateTaskTool) Name() string {
	return "agent"
}

func (t *delegateTaskTool) Description() string {
	return `Spawns an asynchronous sub-agent to perform a specific, isolated task or codebase analysis in the background.

CRITICAL USAGE RULES:
1. Asynchronous: This tool launches the sub-agent and returns immediately. Do not wait for it. You can check its status later using 'TaskList' and 'TaskGet' with the returned task_id.
2. Profiles: Use the 'explore' profile for safe, read-only research tasks (e.g., 'Find where authentication is implemented and summarize it'). Use 'general' only if the sub-agent needs to write code.
3. Clear Instructions: Provide hyper-specific instructions and define exactly what the 'RequiredOutput' should look like.
4. When to Use: Perfect for offloading token-heavy codebase exploration, isolating a complex refactor, or having a sub-agent write unit tests for you while you continue coding.`
}

func (t *delegateTaskTool) IsLongRunning() bool {
	return false // Changed to false because we return immediately
}

func (t *delegateTaskTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"Instructions": {
					Type:        jsonschema.String,
					Description: "Detailed instructions for the sub-agent.",
				},
				"RequiredOutput": {
					Type:        jsonschema.String,
					Description: "Description of the expected output format or content.",
				},
				"Profile": {
					Type:        jsonschema.String,
					Description: "The security/tool profile for the sub-agent. Allowed values: 'explore' (read-only tools like read_file, glob, grep), 'general' (all tools). Default is 'general'.",
					Enum:        []string{"explore", "general"},
				},
			},
			Required: []string{"Instructions", "RequiredOutput"},
		},
	}
}

func (t *delegateTaskTool) Run(ctx context.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid arguments")
	}
	instructions, _ := m["Instructions"].(string)
	requiredOutput, _ := m["RequiredOutput"].(string)

	if instructions == "" {
		return nil, fmt.Errorf("Instructions is required")
	}

	profile, _ := m["Profile"].(string)
	if profile == "" {
		profile = "general"
	}

	return newDelegatedTaskService(t.runner).Launch(ctx, delegatedTaskRequest{
		Instructions:   instructions,
		RequiredOutput: requiredOutput,
		Profile:        profile,
	})
}
