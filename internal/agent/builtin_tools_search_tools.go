package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/smasonuk/falken-core/internal/extensions"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

type searchToolsTool struct {
	runner *Runner
}

func (t *searchToolsTool) Name() string { return "search_tools" }
func (t *searchToolsTool) Description() string {
	return "Search the local tool registry for specialized tools. Use this if you need to perform a task (e.g., 'email', 'kubernetes', 'http') but don't have the required tool active."
}
func (t *searchToolsTool) IsLongRunning() bool { return false }

func (t *searchToolsTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"Query": {
					Type:        jsonschema.String,
					Description: "A keyword or phrase to search for (e.g., 'download website').",
				},
			},
			Required: []string{"Query"},
		},
	}
}

func (t *searchToolsTool) Run(ctx context.Context, args any) (map[string]any, error) {
	m, _ := args.(map[string]any)
	query, _ := m["Query"].(string)
	query = strings.ToLower(query)

	var found []string

	// Basic search implementation
	for name, tool := range t.runner.ToolRegistry {
		desc := ""
		if descGetter, ok := tool.(interface{ Description() string }); ok {
			desc = descGetter.Description()
		}

		descLower := strings.ToLower(desc)
		match := false
		if strings.Contains(strings.ToLower(name), query) || strings.Contains(descLower, query) {
			match = true
		} else if wasmTool, ok := tool.(*extensions.WasmTool); ok {
			for _, kw := range wasmTool.ToolDef.Keywords {
				if strings.Contains(strings.ToLower(kw), query) {
					match = true
					break
				}
			}
			if !match && strings.Contains(strings.ToLower(wasmTool.ToolDef.Category), query) {
				match = true
			}
		}

		if match {
			// Activate it automatically
			t.runner.ActivateTool(name)
			found = append(found, fmt.Sprintf("- %s: %s", name, desc))
		}

		if len(found) >= 3 {
			break
		}
	}

	if len(found) == 0 {
		return map[string]any{"result": "No tools found matching your query. Try a different keyword."}, nil
	}

	resultMsg := fmt.Sprintf("Found and activated the following tools:\n%s\n\nThey are now available for you to use in your next turn.", strings.Join(found, "\n"))
	return map[string]any{"result": resultMsg}, nil
}
