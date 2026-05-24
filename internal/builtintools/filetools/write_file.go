package filetools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// WriteFileTool creates or overwrites a workspace file through the managed
// mutation service.
type WriteFileTool struct{}

func writeFileSpec() fileToolSpec[runtimefiles.WriteFileRequest, runtimefiles.WriteFileResult] {
	return fileToolSpec[runtimefiles.WriteFileRequest, runtimefiles.WriteFileResult]{
		descriptor: api.Descriptor{
			Name: "write_file",
			Description: `Create or overwrite a workspace file through the managed mutation service.

CRITICAL USAGE RULES:
1. Read before write: You MUST call read_file on any existing file before
   writing to it. The service issues a read token on read and rejects the write
   with status "missing_read_token" or "stale_read_token" if the file was not
   read first or has changed in the meantime.
2. Full content required: Provide the entire intended file content. For
   targeted edits to existing files, use edit_file, multi_edit, or apply_patch.
3. Operation semantics:
   - "create"              — fails if the file already exists.
   - "overwrite"           — fails if the file does not exist.
   - "create_or_overwrite" — succeeds in both cases (default).
4. Backups: Every overwrite creates an automatic backup whose path is returned
   in "backup_paths" so you can restore the original if needed.`,
			Parameters: api.MustSchema(api.ObjectSchema(map[string]any{
				"path":        api.StringProp("Workspace-relative or absolute path of the file to write."),
				"content":     api.StringProp("Complete content to write to the file."),
				"operation":   api.StringEnumProp(`Write mode. Defaults to "create_or_overwrite".`, "create", "overwrite", "create_or_overwrite"),
				"mode":        api.StringProp(`Optional octal permission bits, e.g. "0644".`),
				"working_dir": api.StringProp("Optional working directory for resolving relative paths."),
			}, "path", "content")),
			Category:    "files",
			Keywords:    []string{"write", "create", "file", "save"},
			AlwaysLoad:  true,
			DefaultLoad: true,
			Safety:      api.Safety{MutatesWorkspace: true},
		},
		run: func(ctx context.Context, ops workspacefiles.Operations, req runtimefiles.WriteFileRequest) (runtimefiles.WriteFileResult, error) {
			return ops.WriteFile(ctx, req)
		},
		success: func(result runtimefiles.WriteFileResult) bool { return result.Success },
		status:  func(result runtimefiles.WriteFileResult) string { return string(result.Status) },
		content: func(runtimefiles.WriteFileResult) string { return "" },
	}
}

func (t *WriteFileTool) Descriptor() api.Descriptor {
	return writeFileSpec().Descriptor()
}

func (t *WriteFileTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	return writeFileSpec().Execute(ctx, host, args)
}
