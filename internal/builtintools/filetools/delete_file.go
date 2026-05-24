package filetools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// DeleteFileTool deletes a single workspace file through the managed file service.
type DeleteFileTool struct{}

func deleteFileSpec() fileToolSpec[runtimefiles.DeleteFileRequest, runtimefiles.DeleteFileResult] {
	return fileToolSpec[runtimefiles.DeleteFileRequest, runtimefiles.DeleteFileResult]{
		descriptor: api.Descriptor{
			Name: "delete_file",
			Description: `Delete a workspace file through the managed mutation service.

CRITICAL USAGE RULES:
1. Read before delete: You MUST call read_file on the target file before
   deleting it. The service validates the read token and rejects the delete if
   the file was not read or has changed since the read.
2. Automatic backup: A copy of the file is written to the backup store before
   deletion. The backup path is returned so you can restore it if needed.
3. Files only: This tool cannot remove directories. To remove a directory tree
   use execute_command with rm -rf and appropriate approval handling.`,
			Parameters: api.MustSchema(api.ObjectSchema(map[string]any{
				"path":        api.StringProp("Workspace-relative or absolute path of the file to delete."),
				"working_dir": api.StringProp("Optional working directory for resolving relative paths."),
			}, "path")),
			Category:    "files",
			Keywords:    []string{"delete", "remove", "file"},
			AlwaysLoad:  true,
			DefaultLoad: true,
			Safety:      api.Safety{MutatesWorkspace: true},
		},
		run: func(ctx context.Context, ops workspacefiles.Operations, req runtimefiles.DeleteFileRequest) (runtimefiles.DeleteFileResult, error) {
			return ops.DeleteFile(ctx, req)
		},
		success: func(result runtimefiles.DeleteFileResult) bool { return result.Success },
		status:  func(result runtimefiles.DeleteFileResult) string { return string(result.Status) },
		content: func(runtimefiles.DeleteFileResult) string { return "" },
	}
}

func (t *DeleteFileTool) Descriptor() api.Descriptor {
	return deleteFileSpec().Descriptor()
}

func (t *DeleteFileTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	return deleteFileSpec().Execute(ctx, host, args)
}
