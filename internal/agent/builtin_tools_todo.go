package agent

import (
	"context"

	"github.com/smasonuk/falken-core/internal/todo"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

type TodoWriteTool struct {
	store *todo.TodoStore
}

func (t *TodoWriteTool) Name() string { return "TodoWrite" }
func (t *TodoWriteTool) Description() string {
	return "Replace the current execution checklist for the session. Use this to keep track of your current plan and progress."
}
func (t *TodoWriteTool) IsLongRunning() bool { return false }

func (t *TodoWriteTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"Todos": {
					Type: jsonschema.Array,
					Items: &jsonschema.Definition{
						Type: jsonschema.Object,
						Properties: map[string]jsonschema.Definition{
							"id": {
								Type:        jsonschema.String,
								Description: "Unique identifier for the todo item",
							},
							"content": {
								Type:        jsonschema.String,
								Description: "Description of the todo item",
							},
							"status": {
								Type:        jsonschema.String,
								Description: "Status of the todo item: pending, in_progress, completed",
								Enum:        []string{"pending", "in_progress", "completed"},
							},
							"priority": {
								Type:        jsonschema.String,
								Description: "Optional priority string",
							},
						},
						Required: []string{"id", "content", "status"},
					},
					Description: "The complete list of todo items, overwriting the previous list.",
				},
			},
			Required: []string{"Todos"},
		},
	}
}

func (t *TodoWriteTool) Run(ctx context.Context, args any) (map[string]any, error) {
	argsMap, ok := args.(map[string]any)
	if !ok {
		return map[string]any{"error": "Invalid arguments format"}, nil
	}

	todosRaw, ok := argsMap["Todos"].([]any)
	if !ok {
		return map[string]any{"error": "Invalid or missing Todos argument"}, nil
	}

	var todos []todo.TodoItem
	for _, raw := range todosRaw {
		itemMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		id, _ := itemMap["id"].(string)
		content, _ := itemMap["content"].(string)
		statusRaw, _ := itemMap["status"].(string)
		priority, _ := itemMap["priority"].(string)

		var status todo.TodoStatus
		switch statusRaw {
		case "pending":
			status = todo.TodoPending
		case "in_progress":
			status = todo.TodoInProgress
		case "completed":
			status = todo.TodoCompleted
		default:
			status = todo.TodoPending
		}

		todos = append(todos, todo.TodoItem{
			ID:       id,
			Content:  content,
			Status:   status,
			Priority: priority,
		})
	}

	if err := t.store.Write(todos); err != nil {
		return map[string]any{"error": "Failed to write todos: " + err.Error()}, nil
	}

	return map[string]any{"result": "Todos updated successfully.", "todos": todos}, nil
}
