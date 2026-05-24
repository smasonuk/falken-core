package filetools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// ReadFilesTool reads multiple workspace files in a single round-trip.
type ReadFilesTool struct{}

func readFilesSpec() fileToolSpec[runtimefiles.ReadFilesRequest, runtimefiles.ReadFilesResult] {
	return fileToolSpec[runtimefiles.ReadFilesRequest, runtimefiles.ReadFilesResult]{
		descriptor: api.Descriptor{
			Name: "read_files",
			Description: `Read multiple workspace files in one call.

Each file may specify its own start_line / end_line range. The overall result
is successful only when every file was read without error; per-file failures
are reported individually in the "files" array.

Prefer this over sequential read_file calls when you need to examine several
files to understand a feature or trace a call chain.`,
			Parameters: api.MustSchema(api.ObjectSchema(map[string]any{
				"files": api.ArrayProp(
					api.ObjectProp(ReadFileProps(), "A single file to read.", "path"),
					"Ordered list of files to read.",
				),
			}, "files")),
			Category:    "files",
			Keywords:    []string{"read", "files", "batch", "multiple"},
			AlwaysLoad:  true,
			DefaultLoad: true,
			Safety:      api.Safety{PlanSafe: true, ReadsWorkspace: true},
		},
		run: func(ctx context.Context, ops workspacefiles.Operations, req runtimefiles.ReadFilesRequest) (runtimefiles.ReadFilesResult, error) {
			return ops.ReadFiles(ctx, req)
		},
		success: func(result runtimefiles.ReadFilesResult) bool { return result.Success },
		status:  func(result runtimefiles.ReadFilesResult) string { return result.Status },
		content: func(runtimefiles.ReadFilesResult) string { return "" },
	}
}

func (t *ReadFilesTool) Descriptor() api.Descriptor {
	return readFilesSpec().Descriptor()
}

func (t *ReadFilesTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	return readFilesSpec().Execute(ctx, host, args)
}
