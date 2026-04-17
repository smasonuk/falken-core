package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/smasonuk/falken-core/internal/runtimectx"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

type ExecuteCommandTool struct {
	runner *Runner
}

func (t *ExecuteCommandTool) Name() string { return "execute_command" }
func (t *ExecuteCommandTool) Description() string {
	return `Executes a bash shell command on the local machine and returns the output.

CRITICAL USAGE RULES:
1. Post-Edit Verification: After modifying ANY file using edit_file, multi_edit, or apply_patch, you MUST use this tool to run the project's test suite, linter, or compiler to verify your changes. Do not guess if your code works; prove it.
2. Non-Interactive ONLY: Never run commands that require user input or an interactive TUI (e.g., nano, vim, top, less, htop). Your execution will hang.
3. Background Processes: Do not use this for long-running servers (e.g., 'npm start' or 'go run main.go'). Use the 'background' plugin tools instead if you need to start a persistent server.
4. Chaining: You can chain commands using '&&' or '|' to save roundtrips (e.g., 'go build ./... && go test ./...').
5. Output Truncation: If a command outputs more than 10,000 characters, it will be truncated and saved to a file. Use 'grep' on that file to find what you need.`
}
func (t *ExecuteCommandTool) IsLongRunning() bool { return true }

func (t *ExecuteCommandTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"Command": {
					Type:        jsonschema.String,
					Description: "The raw, full shell command string to execute.",
				},
			},
			Required: []string{"Command"},
		},
	}
}

func (t *ExecuteCommandTool) Run(ctx context.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid arguments")
	}

	cmdStr, ok := m["Command"].(string)
	if !ok || cmdStr == "" {
		cmdStr, _ = m["command"].(string)
	}

	eventChan, _ := runtimectx.EventChan(ctx)

	// Determine if this is a known long-running command to prevent spamming tiny streams
	// for simple commands like `ls` or `cat`.
	isLongRunning := strings.Contains(cmdStr, "make") ||
		strings.Contains(cmdStr, "build") ||
		strings.Contains(cmdStr, "npm install") ||
		strings.Contains(cmdStr, "go test") ||
		strings.Contains(cmdStr, "npm test")

	var onChunk func(string)
	if eventChan != nil && isLongRunning {
		onChunk = func(chunk string) {
			eventChan <- CommandStreamMsg{Chunk: chunk}
		}
	}

	output, err := t.runner.Shell.Execute(ctx, t.Name(), cmdStr, "requested by agent", []string{}, false, onChunk)

	if err != nil {
		return map[string]any{"result": fmt.Sprintf("Error: %v\nOutput:\n%s", err, output)}, nil
	}

	return map[string]any{"result": output}, nil
}
