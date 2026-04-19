package agent

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

type UpdateMemoryTool struct {
	runner *Runner
}

func (t *UpdateMemoryTool) Name() string { return "update_memory" }
func (t *UpdateMemoryTool) Description() string {
	return `Updates your persistent long-term structured memory scratchpad. 

CRITICAL USAGE RULES:
1. Merge Semantics: This tool merges your updates into existing memory. You do not need to repeat old information to keep it.
2. Brevity is Key: Keep strings extremely concise. Store only critical file paths, major architectural decisions, and current task state. Do NOT store raw code snippets here.
3. Context Window Protection: Because your conversation history is aggressively truncated to save tokens, use this frequently to summarize what you've learned before you forget it.`
}
func (t *UpdateMemoryTool) IsLongRunning() bool { return false }

func (t *UpdateMemoryTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"CurrentGoal": {
					Type:        jsonschema.String,
					Description: "A concise description of what you are currently trying to achieve. Replaces existing goal.",
				},
				"AddImportantFiles": {
					Type: jsonschema.Array,
					Items: &jsonschema.Definition{
						Type: jsonschema.String,
					},
					Description: "List of important file paths to remember.",
				},
				"RemoveImportantFiles": {
					Type: jsonschema.Array,
					Items: &jsonschema.Definition{
						Type: jsonschema.String,
					},
					Description: "List of file paths to stop remembering.",
				},
				"AddDecisions": {
					Type: jsonschema.Array,
					Items: &jsonschema.Definition{
						Type: jsonschema.String,
					},
					Description: "List of key architectural or implementation decisions made.",
				},
				"AddOpenQuestions": {
					Type: jsonschema.Array,
					Items: &jsonschema.Definition{
						Type: jsonschema.String,
					},
					Description: "List of unanswered questions or unknowns to investigate.",
				},
				"RemoveOpenQuestions": {
					Type: jsonschema.Array,
					Items: &jsonschema.Definition{
						Type: jsonschema.String,
					},
					Description: "List of questions that have been resolved and should be removed.",
				},
			},
		},
	}
}

func (t *UpdateMemoryTool) Run(ctx context.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid arguments")
	}

	mem, err := t.runner.memoryStore.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read memory: %v", err)
	}

	if goal, ok := m["CurrentGoal"].(string); ok && goal != "" {
		mem.CurrentGoal = goal
	}

	if addFiles, ok := m["AddImportantFiles"].([]any); ok {
		var toAdd []string
		for _, f := range addFiles {
			if s, ok := f.(string); ok {
				toAdd = append(toAdd, s)
			}
		}
		mem.ImportantFiles = mergeUniqueStrings(mem.ImportantFiles, toAdd)
	}

	if rmFiles, ok := m["RemoveImportantFiles"].([]any); ok {
		var toRm []string
		for _, f := range rmFiles {
			if s, ok := f.(string); ok {
				toRm = append(toRm, s)
			}
		}
		mem.ImportantFiles = removeStrings(mem.ImportantFiles, toRm)
	}

	if addDecisions, ok := m["AddDecisions"].([]any); ok {
		var toAdd []string
		for _, d := range addDecisions {
			if s, ok := d.(string); ok {
				toAdd = append(toAdd, s)
			}
		}
		mem.Decisions = mergeUniqueStrings(mem.Decisions, toAdd)
	}

	if addQuestions, ok := m["AddOpenQuestions"].([]any); ok {
		var toAdd []string
		for _, q := range addQuestions {
			if s, ok := q.(string); ok {
				toAdd = append(toAdd, s)
			}
		}
		mem.OpenQuestions = mergeUniqueStrings(mem.OpenQuestions, toAdd)
	}

	if rmQuestions, ok := m["RemoveOpenQuestions"].([]any); ok {
		var toRm []string
		for _, q := range rmQuestions {
			if s, ok := q.(string); ok {
				toRm = append(toRm, s)
			}
		}
		mem.OpenQuestions = removeStrings(mem.OpenQuestions, toRm)
	}

	if err := t.runner.memoryStore.Write(mem); err != nil {
		return nil, fmt.Errorf("failed to write memory: %v", err)
	}

	return map[string]any{"result": "Memory successfully updated.", "memory_state": mem}, nil
}
