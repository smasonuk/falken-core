package toolvalidation

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrSchemaPropertiesNotObject identifies object schemas whose properties key
// is present but not itself a JSON object.
var ErrSchemaPropertiesNotObject = errors.New("parameters schema properties must be a JSON object")

var namePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]*$`)

// ValidateName checks the shared model-facing tool/hook name shape.
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New("name must start with a letter and contain only letters, numbers, dots, underscores, or hyphens")
	}
	return nil
}

// ValidateDescription checks that a model-facing descriptor has nonempty text.
func ValidateDescription(description string) error {
	if strings.TrimSpace(description) == "" {
		return errors.New("description is required")
	}
	return nil
}

// ValidateObjectSchema checks that parameters are a valid JSON object schema.
func ValidateObjectSchema(schema json.RawMessage) error {
	if len(schema) == 0 {
		return errors.New("parameters schema is required")
	}

	var decoded map[string]any
	if err := json.Unmarshal(schema, &decoded); err != nil {
		return fmt.Errorf("parameters schema must be valid JSON: %w", err)
	}
	if decoded == nil || decoded["type"] != "object" {
		return errors.New("parameters schema must be a JSON object schema")
	}
	if properties, ok := decoded["properties"]; ok {
		if _, ok := properties.(map[string]any); !ok {
			return ErrSchemaPropertiesNotObject
		}
	}
	return nil
}

// ValidateDescriptor checks the shared name, description, and parameter schema
// contract for built-in, public provider, and manifest tool descriptors.
func ValidateDescriptor(name, description string, schema json.RawMessage) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := ValidateDescription(description); err != nil {
		return err
	}
	return ValidateObjectSchema(schema)
}
