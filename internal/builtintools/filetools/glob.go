package filetools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// GlobTool finds workspace files and directories by glob pattern.
type GlobTool struct{}

func globSpec() fileToolSpec[runtimefiles.GlobRequest, runtimefiles.GlobResult] {
	return fileToolSpec[runtimefiles.GlobRequest, runtimefiles.GlobResult]{
		descriptor: api.Descriptor{
			Name: "glob",
			Description: `Find workspace files and directories matching a glob pattern.

Use this to discover files before reading them. Results are workspace-scoped
and policy-gated. This tool does not issue read tokens; before editing any
matched file you must still call read_file or read_files.

Supports ** for recursive matching. Hidden files and common generated/vendor
directories are skipped by default.`,
			Parameters:  api.MustSchema(api.ObjectSchema(globProps(), "pattern")),
			Category:    "files",
			Keywords:    []string{"glob", "find", "files", "search"},
			AlwaysLoad:  true,
			DefaultLoad: true,
			Safety:      api.Safety{PlanSafe: true, ReadsWorkspace: true},
		},
		run: func(ctx context.Context, ops workspacefiles.Operations, req runtimefiles.GlobRequest) (runtimefiles.GlobResult, error) {
			return ops.Glob(ctx, req)
		},
		success: func(result runtimefiles.GlobResult) bool { return result.Success },
		status:  func(result runtimefiles.GlobResult) string { return result.Status },
		content: func(result runtimefiles.GlobResult) string { return globContent(result) },
	}
}

func (t *GlobTool) Descriptor() api.Descriptor {
	return globSpec().Descriptor()
}

func (t *GlobTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	return globSpec().Execute(ctx, host, args)
}

func globProps() map[string]any {
	return map[string]any{
		"pattern":         api.StringProp("Glob pattern to match. Supports ** for recursive matching."),
		"path":            api.StringProp("Workspace-relative or absolute directory to search from. Defaults to the workspace root/working directory."),
		"working_dir":     api.StringProp("Optional working directory for resolving relative paths."),
		"include_dirs":    api.BoolProp("Include matching directories in results."),
		"include_files":   api.BoolProp("Include matching files in results. Defaults to true."),
		"include_hidden":  api.BoolProp("Include dotfiles and paths inside dot-directories."),
		"include_ignored": api.BoolProp("Include common generated/vendor directories such as .git, node_modules, vendor, dist, and build."),
		"ignore":          api.ArrayProp(map[string]any{"type": "string"}, "Additional ignore glob patterns."),
		"limit":           api.IntegerProp("Maximum number of matches to return. Defaults to 100 and caps at 1000."),
		"offset":          api.IntegerProp("Number of matches to skip before returning results."),
		"sort":            api.StringEnumProp("Sort order for matches.", "path", "modified_desc", "modified_asc"),
	}
}

func globContent(result runtimefiles.GlobResult) string {
	if !result.Success {
		if result.Error != "" {
			return result.Error
		}
		return result.Status
	}
	if len(result.Matches) == 0 {
		return "(no matches found)"
	}
	return strings.Join(result.Matches, "\n")
}
