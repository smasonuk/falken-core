package filetools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// ApplyPatchTool applies a unified git-format diff patch through the managed
// mutation service.
type ApplyPatchTool struct{}

func applyPatchSpec() fileToolSpec[runtimefiles.ApplyPatchRequest, runtimefiles.ApplyPatchResult] {
	return fileToolSpec[runtimefiles.ApplyPatchRequest, runtimefiles.ApplyPatchResult]{
		descriptor: api.Descriptor{
			Name: "apply_patch",
			Description: `Apply a unified git-format diff patch through the managed mutation service.

CRITICAL USAGE RULES:
1. Read before patch: You MUST call read_file on every file referenced in the
   patch before applying it. Patches for files that were not read, or that
   have changed since the last read, are rejected.
2. Format: The patch must be in unified diff format as produced by "git diff"
   (headers starting with "diff --git a/... b/..."). The legacy
   "*** Begin Patch" envelope is not supported.
3. Atomic with rollback: If any file fails to apply, all files that were
   already written in the same patch are rolled back automatically.
4. Supported operations: create, modify, delete. Rename patches are not yet
   supported; implement a rename as delete_file followed by write_file.`,
			Parameters: api.MustSchema(api.ObjectSchema(map[string]any{
				"patch": api.StringProp("Unified git-format diff patch to apply."),
			}, "patch")),
			Category:    "files",
			Keywords:    []string{"patch", "diff", "apply", "file"},
			AlwaysLoad:  true,
			DefaultLoad: true,
			Safety:      api.Safety{MutatesWorkspace: true},
		},
		run: func(ctx context.Context, ops workspacefiles.Operations, req runtimefiles.ApplyPatchRequest) (runtimefiles.ApplyPatchResult, error) {
			return ops.ApplyPatch(ctx, req)
		},
		success: func(result runtimefiles.ApplyPatchResult) bool { return result.Success },
		status:  func(result runtimefiles.ApplyPatchResult) string { return string(result.Status) },
		content: func(runtimefiles.ApplyPatchResult) string { return "" },
	}
}

func (t *ApplyPatchTool) Descriptor() api.Descriptor {
	return applyPatchSpec().Descriptor()
}

func (t *ApplyPatchTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	return applyPatchSpec().Execute(ctx, host, args)
}
