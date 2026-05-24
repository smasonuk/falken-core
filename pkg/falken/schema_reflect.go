package falken

import (
	"fmt"
	"reflect"
	"strings"
)

// SchemaFor derives a JSON object schema from the fields of T.
func SchemaFor[T any]() (Schema, error) {
	var zero T
	return SchemaForType(reflect.TypeOf(zero))
}

// SchemaForType derives a JSON object schema from a struct type.
func SchemaForType(t reflect.Type) (Schema, error) {
	if t == nil {
		return Schema{}, fmt.Errorf("%w: schema type is nil", ErrInvalidConfig)
	}
	t = dereferenceType(t)
	if t.Kind() != reflect.Struct {
		return Schema{}, fmt.Errorf("%w: schema type %s must be a struct", ErrInvalidConfig, t)
	}
	return schemaForStruct(t, make(map[reflect.Type]bool))
}

func schemaForType(t reflect.Type, stack map[reflect.Type]bool) (Schema, error) {
	if t == nil {
		return Schema{}, fmt.Errorf("%w: schema field type is nil", ErrInvalidConfig)
	}
	t = dereferenceType(t)

	switch t.Kind() {
	case reflect.String:
		return String(), nil
	case reflect.Int, reflect.Int32, reflect.Int64:
		return Integer(), nil
	case reflect.Float32, reflect.Float64:
		return Number(), nil
	case reflect.Bool:
		return Boolean(), nil
	case reflect.Slice:
		itemSchema, err := schemaForType(t.Elem(), stack)
		if err != nil {
			return Schema{}, err
		}
		return Array(itemSchema), nil
	case reflect.Struct:
		return schemaForStruct(t, stack)
	default:
		return Schema{}, fmt.Errorf("%w: unsupported schema field type %s", ErrInvalidConfig, t)
	}
}

func schemaForStruct(t reflect.Type, stack map[reflect.Type]bool) (Schema, error) {
	t = dereferenceType(t)
	if stack[t] {
		return Schema{}, fmt.Errorf("%w: recursive schema type %s is not supported", ErrInvalidConfig, t)
	}
	stack[t] = true
	defer delete(stack, t)

	fields := make([]Field, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if field.Anonymous {
			return Schema{}, fmt.Errorf("%w: anonymous embedded schema field %s is not supported", ErrInvalidConfig, field.Name)
		}

		name, skip := jsonFieldName(field)
		if skip {
			continue
		}

		fieldSchema, err := schemaForType(field.Type, stack)
		if err != nil {
			return Schema{}, fmt.Errorf("field %s: %w", field.Name, err)
		}
		tag, err := parseFalkenTag(field.Tag.Get("falken"))
		if err != nil {
			return Schema{}, fmt.Errorf("field %s: %w", field.Name, err)
		}
		if tag.description != "" {
			fieldSchema = fieldSchema.Description(tag.description)
		}
		if tag.format != "" {
			fieldSchema = fieldSchema.Format(tag.format)
		}
		if len(tag.enum) != 0 {
			fieldSchema = withSchemaValue(fieldSchema, "enum", stringsToAny(tag.enum))
		}

		fields = append(fields, Field{
			Name:     name,
			Schema:   fieldSchema,
			Required: tag.required,
		})
	}

	return Object(fields...), nil
}

func dereferenceType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return "", true
	}
	if name == "" {
		name = field.Name
	}
	return name, false
}

type falkenFieldTag struct {
	required    bool
	description string
	format      string
	enum        []string
}

func parseFalkenTag(raw string) (falkenFieldTag, error) {
	var tag falkenFieldTag
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		switch {
		case part == "required":
			tag.required = true
		case strings.HasPrefix(part, "description="):
			tag.description = strings.TrimSpace(strings.TrimPrefix(part, "description="))
		case strings.HasPrefix(part, "format="):
			tag.format = strings.TrimSpace(strings.TrimPrefix(part, "format="))
		case strings.HasPrefix(part, "enum="):
			rawValues := strings.Split(strings.TrimPrefix(part, "enum="), "|")
			tag.enum = tag.enum[:0]
			for _, value := range rawValues {
				value = strings.TrimSpace(value)
				if value != "" {
					tag.enum = append(tag.enum, value)
				}
			}
			if len(tag.enum) == 0 {
				return falkenFieldTag{}, fmt.Errorf("falken enum tag must include at least one value")
			}
		default:
			return falkenFieldTag{}, fmt.Errorf("unsupported falken tag option %q", part)
		}
	}
	return tag, nil
}

func withSchemaValue(schema Schema, key string, value any) Schema {
	object := schemaValue(schema)
	object[key] = value
	return mustSchema(object)
}

func stringsToAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
