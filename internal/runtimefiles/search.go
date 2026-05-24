package runtimefiles

import (
	"context"

	"github.com/smasonuk/falken-core/internal/files"
)

// GlobRequest describes a runtime-facing workspace glob search.
type GlobRequest struct {
	Pattern           string   `json:"pattern"`
	Path              string   `json:"path,omitempty"`
	CurrentWorkingDir string   `json:"working_dir,omitempty"`
	IncludeDirs       bool     `json:"include_dirs,omitempty"`
	IncludeFiles      *bool    `json:"include_files,omitempty"`
	IncludeHidden     bool     `json:"include_hidden,omitempty"`
	IncludeIgnored    bool     `json:"include_ignored,omitempty"`
	Ignore            []string `json:"ignore,omitempty"`
	Limit             int      `json:"limit,omitempty"`
	Offset            int      `json:"offset,omitempty"`
	Sort              string   `json:"sort,omitempty"`
	ApprovalRequired  bool     `json:"approval_required,omitempty"`
}

// GlobResult reports a runtime-facing workspace glob search.
type GlobResult struct {
	CommonResult
	Pattern      string   `json:"pattern"`
	Root         string   `json:"root"`
	ResolvedRoot string   `json:"resolved_root,omitempty"`
	Matches      []string `json:"matches"`
	TotalMatches int      `json:"total_matches"`
	Returned     int      `json:"returned"`
	Offset       int      `json:"offset"`
	Limit        int      `json:"limit"`
	Truncated    bool     `json:"truncated"`
	FilesSkipped int      `json:"files_skipped,omitempty"`
}

// GrepRequest describes a runtime-facing content search.
type GrepRequest struct {
	Regex             string   `json:"regex"`
	TargetPaths       []string `json:"target_paths,omitempty"`
	CurrentWorkingDir string   `json:"working_dir,omitempty"`
	Glob              string   `json:"glob,omitempty"`
	OutputMode        string   `json:"output_mode,omitempty"`
	CaseSensitive     *bool    `json:"case_sensitive,omitempty"`
	Before            int      `json:"before,omitempty"`
	After             int      `json:"after,omitempty"`
	Context           int      `json:"context,omitempty"`
	Limit             int      `json:"limit,omitempty"`
	Offset            int      `json:"offset,omitempty"`
	MaxLineBytes      int      `json:"max_line_bytes,omitempty"`
	IncludeHidden     bool     `json:"include_hidden,omitempty"`
	IncludeIgnored    bool     `json:"include_ignored,omitempty"`
	ApprovalRequired  bool     `json:"approval_required,omitempty"`
}

// GrepMatch is one line-level content match.
type GrepMatch struct {
	Path          string   `json:"path"`
	Line          int      `json:"line"`
	Text          string   `json:"text"`
	ContextBefore []string `json:"context_before,omitempty"`
	ContextAfter  []string `json:"context_after,omitempty"`
}

// GrepFileCount is the line-match count for one file.
type GrepFileCount struct {
	Path    string `json:"path"`
	Matches int    `json:"matches"`
}

// GrepResult reports a runtime-facing content search.
type GrepResult struct {
	CommonResult
	Regex            string          `json:"regex"`
	OutputMode       string          `json:"output_mode"`
	Matches          []GrepMatch     `json:"matches,omitempty"`
	Files            []string        `json:"files,omitempty"`
	Counts           []GrepFileCount `json:"counts,omitempty"`
	TotalMatchesSeen int             `json:"total_matches_seen"`
	Returned         int             `json:"returned"`
	Offset           int             `json:"offset"`
	Limit            int             `json:"limit"`
	Truncated        bool            `json:"truncated"`
	NextOffset       int             `json:"next_offset,omitempty"`
	FilesScanned     int             `json:"files_scanned"`
	FilesWithMatches int             `json:"files_with_matches"`
	FilesSkipped     int             `json:"files_skipped"`
}

// Glob delegates to the managed file search service.
func (o *Operations) Glob(ctx context.Context, request GlobRequest) (GlobResult, error) {
	managed, err := o.service.Glob(ctx, files.GlobRequest{
		Pattern:           request.Pattern,
		Path:              request.Path,
		CurrentWorkingDir: request.CurrentWorkingDir,
		IncludeDirs:       request.IncludeDirs,
		IncludeFiles:      request.IncludeFiles,
		IncludeHidden:     request.IncludeHidden,
		IncludeIgnored:    request.IncludeIgnored,
		Ignore:            append([]string(nil), request.Ignore...),
		Limit:             request.Limit,
		Offset:            request.Offset,
		Sort:              request.Sort,
		ApprovalRequired:  request.ApprovalRequired,
	})
	if err != nil {
		return GlobResult{}, err
	}
	return adaptGlobResult(managed), nil
}

// Grep delegates to the managed file search service.
func (o *Operations) Grep(ctx context.Context, request GrepRequest) (GrepResult, error) {
	managed, err := o.service.Grep(ctx, files.GrepRequest{
		Regex:             request.Regex,
		TargetPaths:       append([]string(nil), request.TargetPaths...),
		CurrentWorkingDir: request.CurrentWorkingDir,
		Glob:              request.Glob,
		OutputMode:        request.OutputMode,
		CaseSensitive:     request.CaseSensitive,
		Before:            request.Before,
		After:             request.After,
		Context:           request.Context,
		Limit:             request.Limit,
		Offset:            request.Offset,
		MaxLineBytes:      request.MaxLineBytes,
		IncludeHidden:     request.IncludeHidden,
		IncludeIgnored:    request.IncludeIgnored,
		ApprovalRequired:  request.ApprovalRequired,
	})
	if err != nil {
		return GrepResult{}, err
	}
	return adaptGrepResult(managed), nil
}

func adaptGlobResult(managed files.GlobResult) GlobResult {
	return GlobResult{
		CommonResult: CommonResult{
			Operation:    OperationGlob,
			Success:      managed.Status == files.GlobStatusOK,
			Status:       string(managed.Status),
			Path:         managed.Root,
			ResolvedPath: managed.ResolvedRoot,
			Error:        managed.Error,
		},
		Pattern:      managed.Pattern,
		Root:         managed.Root,
		ResolvedRoot: managed.ResolvedRoot,
		Matches:      append([]string(nil), managed.Matches...),
		TotalMatches: managed.TotalMatches,
		Returned:     managed.Returned,
		Offset:       managed.Offset,
		Limit:        managed.Limit,
		Truncated:    managed.Truncated,
		FilesSkipped: managed.FilesSkipped,
	}
}

func adaptGrepResult(managed files.GrepResult) GrepResult {
	return GrepResult{
		CommonResult: CommonResult{
			Operation: OperationGrep,
			Success:   managed.Status == files.GrepStatusOK,
			Status:    string(managed.Status),
			Error:     managed.Error,
		},
		Regex:            managed.Regex,
		OutputMode:       managed.OutputMode,
		Matches:          adaptGrepMatches(managed.Matches),
		Files:            append([]string(nil), managed.Files...),
		Counts:           adaptGrepCounts(managed.Counts),
		TotalMatchesSeen: managed.TotalMatchesSeen,
		Returned:         managed.Returned,
		Offset:           managed.Offset,
		Limit:            managed.Limit,
		Truncated:        managed.Truncated,
		NextOffset:       managed.NextOffset,
		FilesScanned:     managed.FilesScanned,
		FilesWithMatches: managed.FilesWithMatches,
		FilesSkipped:     managed.FilesSkipped,
	}
}

func adaptGrepMatches(matches []files.GrepMatch) []GrepMatch {
	out := make([]GrepMatch, 0, len(matches))
	for _, match := range matches {
		out = append(out, GrepMatch{
			Path:          match.Path,
			Line:          match.Line,
			Text:          match.Text,
			ContextBefore: append([]string(nil), match.ContextBefore...),
			ContextAfter:  append([]string(nil), match.ContextAfter...),
		})
	}
	return out
}

func adaptGrepCounts(counts []files.GrepFileCount) []GrepFileCount {
	out := make([]GrepFileCount, 0, len(counts))
	for _, count := range counts {
		out = append(out, GrepFileCount{
			Path:    count.Path,
			Matches: count.Matches,
		})
	}
	return out
}
