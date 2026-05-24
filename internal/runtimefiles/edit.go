package runtimefiles

import (
	"context"

	"github.com/smasonuk/falken-core/internal/files"
)

// EditFileRequest describes a runtime-facing targeted edit.
type EditFileRequest struct {
	Path              string              `json:"path"`
	CurrentWorkingDir string              `json:"working_dir,omitempty"`
	Old               string              `json:"old"`
	New               string              `json:"new"`
	ReplaceAll        bool                `json:"replace_all,omitempty"`
	MatchStrategy     files.MatchStrategy `json:"match_strategy,omitempty"`
	ApprovalRequired  bool                `json:"approval_required,omitempty"`
}

// EditFileResult reports a runtime-facing targeted edit.
type EditFileResult struct {
	CommonResult
	ReplaceAll        bool                     `json:"replace_all"`
	Replacements      int                      `json:"replacements"`
	Changed           bool                     `json:"changed"`
	Summary           string                   `json:"summary,omitempty"`
	MatchStrategy     string                   `json:"match_strategy,omitempty"`
	WorkspaceMutation WorkspaceMutationSummary `json:"workspace_mutation"`
	Managed           files.EditResult         `json:"-"`
}

// MultiEditRequest describes runtime-facing multi-edit operations.
type MultiEditRequest struct {
	Edits []EditFileRequest `json:"edits"`
}

// MultiEditResult reports runtime-facing multi-edit operations.
type MultiEditResult struct {
	CommonResult
	Files             []FileChangeSummary      `json:"files"`
	TotalFiles        int                      `json:"total_files"`
	FilesChanged      int                      `json:"files_changed"`
	FilesRolledBack   int                      `json:"files_rolled_back,omitempty"`
	TotalReplacements int                      `json:"total_replacements"`
	RollbackAttempted bool                     `json:"rollback_attempted,omitempty"`
	RollbackSucceeded bool                     `json:"rollback_succeeded,omitempty"`
	RollbackError     string                   `json:"rollback_error,omitempty"`
	WorkspaceMutation WorkspaceMutationSummary `json:"workspace_mutation"`
	Managed           files.MultiEditResult    `json:"-"`
}

// EditFile delegates to the managed targeted edit service.
func (o *Operations) EditFile(ctx context.Context, request EditFileRequest) (EditFileResult, error) {
	managed, err := o.service.Edit(ctx, files.EditRequest{
		Path:              request.Path,
		CurrentWorkingDir: request.CurrentWorkingDir,
		Old:               request.Old,
		New:               request.New,
		ReplaceAll:        request.ReplaceAll,
		MatchStrategy:     request.MatchStrategy,
		ApprovalRequired:  request.ApprovalRequired,
	})
	if err != nil {
		return EditFileResult{}, err
	}

	return adaptEditFileResult(managed), nil
}

// MultiEdit delegates to the managed multi-edit service.
func (o *Operations) MultiEdit(ctx context.Context, request MultiEditRequest) (MultiEditResult, error) {
	managedRequest := files.MultiEditRequest{
		Edits: make([]files.EditRequest, 0, len(request.Edits)),
	}
	for _, edit := range request.Edits {
		managedRequest.Edits = append(managedRequest.Edits, files.EditRequest{
			Path:              edit.Path,
			CurrentWorkingDir: edit.CurrentWorkingDir,
			Old:               edit.Old,
			New:               edit.New,
			ReplaceAll:        edit.ReplaceAll,
			MatchStrategy:     edit.MatchStrategy,
			ApprovalRequired:  edit.ApprovalRequired,
		})
	}

	managed, err := o.service.MultiEdit(ctx, managedRequest)
	if err != nil {
		return MultiEditResult{}, err
	}

	return adaptMultiEditResult(managed), nil
}

func adaptEditFileResult(managed files.EditResult) EditFileResult {
	filesChanged := 0
	if managed.Changed || managed.Write.MutationMayHaveOccurred {
		filesChanged = 1
	}
	return EditFileResult{
		CommonResult: CommonResult{
			Operation:    OperationEditFile,
			Success:      managed.Status == files.EditStatusChanged || managed.Status == files.EditStatusUnchanged,
			Status:       string(managed.Status),
			Path:         managed.Path,
			ResolvedPath: managed.ResolvedPath,
			Error:        managed.Error,
			BackupPaths:  backupPaths(managed.Write.BackupPath),
		},
		ReplaceAll:    managed.ReplaceAll,
		Replacements:  managed.Replacements,
		Changed:       managed.Changed,
		Summary:       managed.Summary,
		MatchStrategy: string(managed.MatchStrategy),
		WorkspaceMutation: WorkspaceMutationSummary{
			Observed:        managed.Changed,
			MayHaveOccurred: managed.Write.MutationMayHaveOccurred,
			FilesChanged:    filesChanged,
		},
		Managed: managed,
	}
}

func adaptMultiEditResult(managed files.MultiEditResult) MultiEditResult {
	mayHaveOccurred := managed.RollbackAttempted && !managed.RollbackSucceeded
	result := MultiEditResult{
		CommonResult: CommonResult{
			Operation: OperationMultiEdit,
			Success:   managed.Status == files.MultiEditStatusApplied || managed.Status == files.MultiEditStatusNoChanges,
			Status:    string(managed.Status),
			Error:     managed.Error,
		},
		Files:             make([]FileChangeSummary, 0, len(managed.Files)),
		TotalFiles:        managed.TotalFiles,
		FilesChanged:      managed.FilesChanged,
		FilesRolledBack:   managed.FilesRolledBack,
		TotalReplacements: managed.TotalReplacements,
		RollbackAttempted: managed.RollbackAttempted,
		RollbackSucceeded: managed.RollbackSucceeded,
		RollbackError:     managed.RollbackError,
		WorkspaceMutation: WorkspaceMutationSummary{
			Observed:          managed.FilesChanged > 0,
			MayHaveOccurred:   mayHaveOccurred,
			FilesChanged:      managed.FilesChanged,
			RollbackAttempted: managed.RollbackAttempted,
			RollbackSucceeded: managed.RollbackSucceeded,
		},
		Managed: managed,
	}

	for _, file := range managed.Files {
		if file.Write.MutationMayHaveOccurred {
			result.WorkspaceMutation.MayHaveOccurred = true
			if result.WorkspaceMutation.FilesChanged == 0 {
				result.WorkspaceMutation.FilesChanged = 1
			}
		}
		summary := FileChangeSummary{
			Operation:     OperationEditFile,
			Status:        string(file.Status),
			Path:          file.Path,
			ResolvedPath:  file.ResolvedPath,
			BackupCreated: file.Write.BackupCreated,
			BackupPath:    file.Write.BackupPath,
			Error:         file.Error,
		}
		result.Files = append(result.Files, summary)
		if summary.BackupPath != "" {
			result.BackupPaths = append(result.BackupPaths, summary.BackupPath)
		}
	}

	return result
}
