package builtintools_test

import (
	"testing"

	"github.com/smasonuk/falken-core/internal/builtintools"
	"github.com/smasonuk/falken-core/internal/toolvalidation"
)

func TestRegistryContract(t *testing.T) {
	wantNames := []string{
		"read_file",
		"read_files",
		"glob",
		"grep",
		"write_file",
		"edit_file",
		"multi_edit",
		"apply_patch",
		"delete_file",
		"execute_command",
		"read_plan",
		"write_plan",
		"read_todos",
		"write_todos",
		"read_command_evidence",
		"submit_plan_implementation",
		"read_memory",
		"update_memory",
	}

	tools := builtintools.All()
	if len(tools) != len(wantNames) {
		t.Fatalf("All() returned %d tools, want %d", len(tools), len(wantNames))
	}

	seen := make(map[string]struct{}, len(tools))
	for i, tool := range tools {
		if tool == nil {
			t.Fatalf("All()[%d] is nil", i)
		}

		desc := tool.Descriptor()
		if desc.Name != wantNames[i] {
			t.Fatalf("All()[%d].Descriptor().Name = %q, want %q", i, desc.Name, wantNames[i])
		}

		if _, ok := seen[desc.Name]; ok {
			t.Fatalf("duplicate built-in tool name %q", desc.Name)
		}
		seen[desc.Name] = struct{}{}

		if got := builtintools.ByName(desc.Name); got == nil {
			t.Fatalf("ByName(%q) returned nil", desc.Name)
		}

		if !builtintools.IsBuiltin(desc.Name) {
			t.Fatalf("IsBuiltin(%q) returned false", desc.Name)
		}

		if err := toolvalidation.ValidateDescriptor(desc.Name, desc.Description, desc.Parameters); err != nil {
			t.Fatalf("descriptor for %q failed validation: %v", desc.Name, err)
		}
	}
}
