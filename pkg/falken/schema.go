package falken

import "encoding/json"

// Schema wraps a JSON Schema fragment.
type Schema struct {
	raw json.RawMessage
}

// JSON returns a defensive copy of the schema JSON.
func (s Schema) JSON() json.RawMessage {
	return append(json.RawMessage(nil), s.raw...)
}

// Object builds a JSON object schema with the supplied fields.
func Object(fields ...Field) Schema {
	properties := make(map[string]any, len(fields))
	required := make([]string, 0, len(fields))
	for _, field := range fields {
		properties[field.Name] = schemaValue(field.Schema)
		if field.Required {
			required = append(required, field.Name)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) != 0 {
		schema["required"] = required
	}
	return mustSchema(schema)
}

// String builds a JSON string schema.
func String() Schema {
	return mustSchema(map[string]any{"type": "string"})
}

// Integer builds a JSON integer schema.
func Integer() Schema {
	return mustSchema(map[string]any{"type": "integer"})
}

// Number builds a JSON number schema.
func Number() Schema {
	return mustSchema(map[string]any{"type": "number"})
}

// Boolean builds a JSON boolean schema.
func Boolean() Schema {
	return mustSchema(map[string]any{"type": "boolean"})
}

// Array builds a JSON array schema.
func Array(items Schema) Schema {
	return mustSchema(map[string]any{
		"type":  "array",
		"items": schemaValue(items),
	})
}

// Enum builds a JSON string enum schema.
func Enum(values ...string) Schema {
	enumValues := make([]any, 0, len(values))
	for _, value := range values {
		enumValues = append(enumValues, value)
	}
	return mustSchema(map[string]any{
		"type": "string",
		"enum": enumValues,
	})
}

// Field describes one object field.
type Field struct {
	Name     string
	Schema   Schema
	Required bool
}

// Required builds a required object field.
func Required(name string, schema Schema) Field {
	return Field{Name: name, Schema: schema, Required: true}
}

// Optional builds an optional object field.
func Optional(name string, schema Schema) Field {
	return Field{Name: name, Schema: schema}
}

// Description returns a copy of s with a JSON Schema description.
func (s Schema) Description(text string) Schema {
	return s.with("description", text)
}

// Format returns a copy of s with a JSON Schema format.
func (s Schema) Format(format string) Schema {
	return s.with("format", format)
}

func (s Schema) with(key string, value any) Schema {
	object := schemaValue(s)
	object[key] = value
	return mustSchema(object)
}

func schemaValue(schema Schema) map[string]any {
	if len(schema.raw) == 0 {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(schema.raw, &decoded); err != nil {
		return map[string]any{}
	}
	if decoded == nil {
		return map[string]any{}
	}
	return decoded
}

func mustSchema(value map[string]any) Schema {
	data, err := json.Marshal(value)
	if err != nil {
		panic("falken schema: marshal failed: " + err.Error())
	}
	return Schema{raw: json.RawMessage(data)}
}
