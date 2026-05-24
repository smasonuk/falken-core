package filetools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// MultiEditTool applies multiple targeted edits across one or more files in a
// single atomic operation.
type MultiEditTool struct{}

func multiEditSpec() fileToolSpec[runtimefiles.MultiEditRequest, runtimefiles.MultiEditResult] {
	return fileToolSpec[runtimefiles.MultiEditRequest, runtimefiles.MultiEditResult]{
		descriptor: api.Descriptor{
			Name: "multi_edit",
			Description: `Apply multiple exact-string replacements atomically through the managed mutation service.

CRITICAL USAGE RULES:
1. Read before edit: You MUST call read_file (or read_files) on every target
   file before calling multi_edit. Edits are rejected for any file whose read
   token is missing or stale.
2. Atomic with rollback: If any edit in the batch fails, all previously
   applied edits in the batch are automatically rolled back. You will never be
   left with a partially-edited state.
3. Intra-file ordering: Edits targeting the same file are applied in array
   order. Each edit operates on the output of the previous edit for that file,
   so "old" strings must account for text already changed by earlier edits in
   the same file.
4. Prefer over sequential edit_file calls: Use multi_edit whenever you need
   to change more than one location in the same file, or make related changes
   across several files that should succeed or fail together.`,
			Parameters: api.MustSchema(api.ObjectSchema(map[string]any{
				"edits": api.ArrayProp(
					api.ObjectProp(EditFileProps(), "A single edit to apply.", "path", "old", "new"),
					"Ordered list of edits to apply atomically.",
				),
			}, "edits")),
			Category:    "files",
			Keywords:    []string{"edit", "multi", "batch", "files", "replace", "atomic"},
			AlwaysLoad:  true,
			DefaultLoad: true,
			Safety:      api.Safety{MutatesWorkspace: true},
		},
		run: func(ctx context.Context, ops workspacefiles.Operations, req runtimefiles.MultiEditRequest) (runtimefiles.MultiEditResult, error) {
			return ops.MultiEdit(ctx, req)
		},
		success: func(result runtimefiles.MultiEditResult) bool { return result.Success },
		status:  func(result runtimefiles.MultiEditResult) string { return string(result.Status) },
		content: func(runtimefiles.MultiEditResult) string { return "" },
	}
}

func (t *MultiEditTool) Descriptor() api.Descriptor {
	return multiEditSpec().Descriptor()
}

func (t *MultiEditTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	return multiEditSpec().Execute(ctx, host, args)
}
