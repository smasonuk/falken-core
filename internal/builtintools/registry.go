package builtintools

import (
	"github.com/smasonuk/falken-core/internal/builtintools/commandtools"
	"github.com/smasonuk/falken-core/internal/builtintools/filetools"
	"github.com/smasonuk/falken-core/internal/builtintools/memorytools"
	"github.com/smasonuk/falken-core/internal/builtintools/planningtools"
)

// All returns every built-in tool in default registration order.
// The returned slice is freshly allocated on every call.
func All() []Tool {
	return []Tool{
		new(filetools.ReadFileTool),
		new(filetools.ReadFilesTool),
		new(filetools.GlobTool),
		new(filetools.GrepTool),
		new(filetools.WriteFileTool),
		new(filetools.EditFileTool),
		new(filetools.MultiEditTool),
		new(filetools.ApplyPatchTool),
		new(filetools.DeleteFileTool),
		new(commandtools.ExecuteCommandTool),
		new(planningtools.ReadPlanTool),
		new(planningtools.WritePlanTool),
		new(planningtools.ReadTodosTool),
		new(planningtools.WriteTodosTool),
		new(planningtools.ReadCommandEvidenceTool),
		new(planningtools.SubmitPlanImplementationTool),
		new(memorytools.ReadMemoryTool),
		new(memorytools.UpdateMemoryTool),
	}
}

// ByName returns the first tool whose name equals name, or nil.
func ByName(name string) Tool {
	for _, t := range All() {
		if t.Descriptor().Name == name {
			return t
		}
	}
	return nil
}

// IsBuiltin reports whether name identifies a registered built-in tool.
func IsBuiltin(name string) bool { return ByName(name) != nil }
