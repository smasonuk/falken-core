package filetools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// EditFileTool performs a targeted exact-string replacement on an existing file.
type EditFileTool struct{}

func editFileSpec() fileToolSpec[runtimefiles.EditFileRequest, runtimefiles.EditFileResult] {
	return fileToolSpec[runtimefiles.EditFileRequest, runtimefiles.EditFileResult]{
		descriptor: api.Descriptor{
			Name: "edit_file",
			Description: `Replace a string in an existing workspace file through the managed mutation service.

CRITICAL USAGE RULES:
1. Read before edit: You MUST call read_file on the target file first. The
   service validates the read token and rejects the edit if the file was not
   read or has changed since the read.
2. Match strategy: By default, "old" must appear exactly as it does in
   the file, including all whitespace and line endings. Copy it directly from
   the read_file result. Set match_strategy to "close_match" only when exact
   matching is not appropriate.
3. Unique context required: If "old" matches more than one location the edit
   is rejected with status "ambiguous_match". Expand "old" to include more
   surrounding lines to make it unique, or set replace_all to true to replace
   every occurrence.
4. Use multi_edit for multiple changes: When you need to change several
   disjoint sections of the same file in one logical operation, use multi_edit
   to apply them atomically and avoid multiple read-token round-trips.`,
			Parameters:  api.MustSchema(api.ObjectSchema(EditFileProps(), "path", "old", "new")),
			Category:    "files",
			Keywords:    []string{"edit", "replace", "change", "file"},
			AlwaysLoad:  true,
			DefaultLoad: true,
			Safety:      api.Safety{MutatesWorkspace: true},
		},
		run: func(ctx context.Context, ops workspacefiles.Operations, req runtimefiles.EditFileRequest) (runtimefiles.EditFileResult, error) {
			return ops.EditFile(ctx, req)
		},
		success: func(result runtimefiles.EditFileResult) bool { return result.Success },
		status:  func(result runtimefiles.EditFileResult) string { return string(result.Status) },
		content: func(result runtimefiles.EditFileResult) string { return result.Summary },
	}
}

func (t *EditFileTool) Descriptor() api.Descriptor {
	return editFileSpec().Descriptor()
}

func (t *EditFileTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	return editFileSpec().Execute(ctx, host, args)
}

// EditFileProps is exported so MultiEditTool can reuse it for per-edit entries.
func EditFileProps() map[string]any {
	return map[string]any{
		"path":           api.StringProp("Workspace-relative or absolute path of the file to edit."),
		"old":            api.StringProp("Exact text to find. Must match verbatim including whitespace."),
		"new":            api.StringProp("Replacement text. May be empty to delete the matched section."),
		"replace_all":    api.BoolProp("Replace every occurrence of 'old' rather than requiring a unique match."),
		"match_strategy": api.StringEnumProp(`Matching strategy. Defaults to "exact". "close_match" will first ignore whitespaces, then use a distance algorithm to find something almost the same.`, "exact", "close_match"),
		"working_dir":    api.StringProp("Optional working directory for resolving relative paths."),
	}
}
