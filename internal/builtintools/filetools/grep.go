package filetools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// GrepTool searches workspace file contents by regular expression.
type GrepTool struct{}

func grepSpec() fileToolSpec[runtimefiles.GrepRequest, runtimefiles.GrepResult] {
	return fileToolSpec[runtimefiles.GrepRequest, runtimefiles.GrepResult]{
		descriptor: api.Descriptor{
			Name: "grep",
			Description: `Search workspace file contents with a regular expression.

Use this to locate relevant code before reading files. Results are
workspace-scoped and policy-gated. This tool does not issue read tokens; before
editing any matched file you must still call read_file or read_files.

Supports recursive directory search, optional glob filtering, context lines,
and output modes for content, files with matches, and counts.`,
			Parameters:  api.MustSchema(api.ObjectSchema(grepProps(), "regex")),
			Category:    "files",
			Keywords:    []string{"grep", "search", "regex", "contents"},
			AlwaysLoad:  true,
			DefaultLoad: true,
			Safety:      api.Safety{PlanSafe: true, ReadsWorkspace: true},
		},
		run: func(ctx context.Context, ops workspacefiles.Operations, req runtimefiles.GrepRequest) (runtimefiles.GrepResult, error) {
			return ops.Grep(ctx, req)
		},
		success: func(result runtimefiles.GrepResult) bool { return result.Success },
		status:  func(result runtimefiles.GrepResult) string { return result.Status },
		content: func(result runtimefiles.GrepResult) string { return grepContent(result) },
	}
}

func (t *GrepTool) Descriptor() api.Descriptor {
	return grepSpec().Descriptor()
}

func (t *GrepTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	return grepSpec().Execute(ctx, host, args)
}

func grepProps() map[string]any {
	return map[string]any{
		"regex":           api.StringProp("Regular expression to search for."),
		"target_paths":    api.ArrayProp(map[string]any{"type": "string"}, "Files or directories to search. Defaults to the workspace root/working directory."),
		"working_dir":     api.StringProp("Optional working directory for resolving relative paths."),
		"glob":            api.StringProp("Optional glob filter applied to file names and workspace-relative paths."),
		"output_mode":     api.StringEnumProp("Result mode.", "content", "files_with_matches", "count"),
		"case_sensitive":  api.BoolProp("Set false for case-insensitive search. Defaults to true."),
		"before":          api.IntegerProp("Number of context lines before each match, capped at 10."),
		"after":           api.IntegerProp("Number of context lines after each match, capped at 10."),
		"context":         api.IntegerProp("Number of context lines before and after each match, overriding before/after and capped at 10."),
		"limit":           api.IntegerProp("Maximum number of returned matches/files. Defaults to 250 and caps at 1000."),
		"offset":          api.IntegerProp("Number of matches/files to skip before returning results."),
		"max_line_bytes":  api.IntegerProp("Maximum bytes of each returned line before truncation. Defaults to 500."),
		"include_hidden":  api.BoolProp("Include dotfiles and paths inside dot-directories."),
		"include_ignored": api.BoolProp("Include common generated/vendor directories such as .git, node_modules, vendor, dist, and build."),
	}
}

func grepContent(result runtimefiles.GrepResult) string {
	if !result.Success {
		if result.Error != "" {
			return result.Error
		}
		return result.Status
	}
	switch result.OutputMode {
	case "files_with_matches":
		if len(result.Files) == 0 {
			return "No matches found."
		}
		return appendGrepPagination(result, strings.Join(result.Files, "\n"))
	case "count":
		var builder strings.Builder
		fmt.Fprintf(&builder, "Total matching files: %d\nReturned files: %d", result.FilesWithMatches, result.Returned)
		for _, count := range result.Counts {
			fmt.Fprintf(&builder, "\n%s: %d", count.Path, count.Matches)
		}
		return appendGrepPagination(result, builder.String())
	default:
		if len(result.Matches) == 0 {
			return "No matches found."
		}
		var builder strings.Builder
		for i, match := range result.Matches {
			beforeStart := match.Line - len(match.ContextBefore)
			for j, line := range match.ContextBefore {
				if builder.Len() > 0 {
					builder.WriteString("\n")
				}
				builder.WriteString(fmt.Sprintf("%s-%d- %s", match.Path, beforeStart+j, line))
			}
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(fmt.Sprintf("%s:%d: %s", match.Path, match.Line, match.Text))
			for j, line := range match.ContextAfter {
				builder.WriteString("\n")
				builder.WriteString(fmt.Sprintf("%s-%d- %s", match.Path, match.Line+j+1, line))
			}
			if i < len(result.Matches)-1 {
				builder.WriteString("\n")
			}
		}
		return appendGrepPagination(result, builder.String())
	}
}

func appendGrepPagination(result runtimefiles.GrepResult, content string) string {
	if !result.Truncated {
		return content
	}
	var builder strings.Builder
	builder.WriteString(content)
	builder.WriteString("\n--- pagination ---")
	switch result.OutputMode {
	case "files_with_matches":
		fmt.Fprintf(&builder, "\nTotal matching files: %d", result.FilesWithMatches)
		fmt.Fprintf(&builder, "\nReturned files: %d", result.Returned)
	case "content", "":
		fmt.Fprintf(&builder, "\nTotal matches seen: %d", result.TotalMatchesSeen)
		fmt.Fprintf(&builder, "\nReturned: %d", result.Returned)
	default:
		fmt.Fprintf(&builder, "\nReturned: %d", result.Returned)
	}
	fmt.Fprintf(&builder, "\nNext offset: %d", result.NextOffset)
	return builder.String()
}
