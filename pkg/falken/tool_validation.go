package falken

import (
	"errors"
	"fmt"
	"strings"

	"github.com/smasonuk/falken-core/internal/toolvalidation"
)

// ValidateToolDescriptor checks that a public tool descriptor is suitable for
// exposure to the model.
func ValidateToolDescriptor(descriptor ToolDescriptor) error {
	name := strings.TrimSpace(descriptor.Name)
	if name == "" {
		return errors.New("tool descriptor has empty name")
	}
	if err := toolvalidation.ValidateName(name); err != nil {
		return fmt.Errorf("tool descriptor %q has invalid name: must start with a letter and contain only letters, numbers, dots, underscores, or hyphens", descriptor.Name)
	}
	if err := toolvalidation.ValidateDescription(descriptor.Description); err != nil {
		return fmt.Errorf("tool descriptor %q has empty description", descriptor.Name)
	}
	if len(descriptor.Parameters) == 0 {
		return fmt.Errorf("tool descriptor %q has empty parameters schema", descriptor.Name)
	}
	if err := toolvalidation.ValidateObjectSchema(descriptor.Parameters); err != nil {
		if errors.Is(err, toolvalidation.ErrSchemaPropertiesNotObject) {
			return fmt.Errorf("tool descriptor %q parameters schema properties must be a JSON object", descriptor.Name)
		}
		if strings.Contains(err.Error(), "valid JSON") {
			return fmt.Errorf("tool descriptor %q has invalid parameters schema: %w", descriptor.Name, err)
		}
		return fmt.Errorf("tool descriptor %q has invalid parameters schema: %w", descriptor.Name, err)
	}
	return nil
}
