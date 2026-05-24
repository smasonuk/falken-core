package api

import (
	"encoding/json"
	"fmt"
)

func MustSchema(schema map[string]any) json.RawMessage {
	data, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("builtintools: marshal schema: %v", err))
	}
	return json.RawMessage(data)
}

func ObjectSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func StringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func IntegerProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func BoolProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func StringEnumProp(description string, values ...string) map[string]any {
	enums := make([]any, len(values))
	for i, v := range values {
		enums[i] = v
	}
	return map[string]any{"type": "string", "description": description, "enum": enums}
}

func ArrayProp(items map[string]any, description string) map[string]any {
	return map[string]any{"type": "array", "items": items, "description": description}
}

func ObjectProp(props map[string]any, description string, required ...string) map[string]any {
	s := map[string]any{"type": "object", "description": description, "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func StringMapProp(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          description,
		"additionalProperties": map[string]any{"type": "string"},
	}
}
