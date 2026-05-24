package planningtools

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/conversation"
)

func todoListSchema(description string) map[string]any {
	return api.ArrayProp(api.ObjectProp(map[string]any{
		"id":      api.StringProp("Stable todo identifier."),
		"content": api.StringProp("Short todo item content."),
		"status":  api.StringEnumProp("Current todo status.", "pending", "in_progress", "completed"),
	}, "One runtime todo item.", "id", "content", "status"), description)
}

func decodeTodos(raw json.RawMessage) ([]conversation.Todo, error) {
	if len(raw) == 0 {
		return nil, errors.New("todos field is required")
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, errors.New("todos must be an array")
	}
	var todos []conversation.Todo
	if err := api.DecodeStrictJSON(raw, &todos); err != nil {
		return nil, err
	}
	return conversation.NormalizeTodos(todos)
}
