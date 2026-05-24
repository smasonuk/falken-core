package filetools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// ReadFileTool reads a single workspace file through the managed file service.
type ReadFileTool struct{}

func readFileSpec() fileToolSpec[runtimefiles.ReadFileRequest, runtimefiles.ReadFileResult] {
	return fileToolSpec[runtimefiles.ReadFileRequest, runtimefiles.ReadFileResult]{
		descriptor: api.Descriptor{
			Name: "read_file",
			Description: `Read one workspace file through the managed file service.

A read token is issued automatically. Any subsequent write_file, edit_file,
multi_edit, apply_patch, or delete_file call targeting the same path will be
rejected if the file has changed since this read, protecting you from
silently overwriting external modifications.

Use start_line / end_line to read a slice of a large file without loading
the entire contents into the context window.`,
			Parameters:  api.MustSchema(api.ObjectSchema(ReadFileProps(), "path")),
			Category:    "files",
			Keywords:    []string{"read", "file", "view", "cat"},
			AlwaysLoad:  true,
			DefaultLoad: true,
			Safety:      api.Safety{PlanSafe: true, ReadsWorkspace: true},
		},
		run: func(ctx context.Context, ops workspacefiles.Operations, req runtimefiles.ReadFileRequest) (runtimefiles.ReadFileResult, error) {
			return ops.ReadFile(ctx, req)
		},
		success: func(result runtimefiles.ReadFileResult) bool { return result.Success },
		status:  func(result runtimefiles.ReadFileResult) string { return string(result.Status) },
		content: func(result runtimefiles.ReadFileResult) string { return result.Content },
	}
}

func (t *ReadFileTool) Descriptor() api.Descriptor {
	return readFileSpec().Descriptor()
}

func (t *ReadFileTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	return readFileSpec().Execute(ctx, host, args)
}

// ReadFileProps is exported so ReadFilesTool can reuse it for per-file entries.
func ReadFileProps() map[string]any {
	return map[string]any{
		"path":        api.StringProp("Workspace-relative or absolute path of the file to read."),
		"start_line":  api.IntegerProp("Optional 1-based line number to begin reading from."),
		"end_line":    api.IntegerProp("Optional 1-based line number to stop reading at (inclusive)."),
		"working_dir": api.StringProp("Optional working directory for resolving relative paths."),
	}
}
