package runtimefiles

import (
	"context"

	"github.com/smasonuk/falken-core/internal/files"
)

// ApplyPatchRequest describes a runtime-facing unified patch application.
type ApplyPatchRequest struct {
	Patch            string `json:"patch"`
	ApprovalRequired bool   `json:"approval_required,omitempty"`
}

// ApplyPatchResult reports a runtime-facing unified patch application.
type ApplyPatchResult struct {
	CommonResult
	Created  []FileChangeSummary `json:"created,omitempty"`
	Modified []FileChangeSummary `json:"modified,omitempty"`
	Deleted  []FileChangeSummary `json:"deleted,omitempty"`
	// Renamed is reserved for future rename support; rename patches currently fail preflight.
	Renamed           []FileChangeSummary      `json:"renamed,omitempty"`
	Failed            []FileChangeSummary      `json:"failed,omitempty"`
	FilesRolledBack   int                      `json:"files_rolled_back,omitempty"`
	RollbackAttempted bool                     `json:"rollback_attempted,omitempty"`
	RollbackSucceeded bool                     `json:"rollback_succeeded,omitempty"`
	RollbackError     string                   `json:"rollback_error,omitempty"`
	WorkspaceMutation WorkspaceMutationSummary `json:"workspace_mutation"`
	Managed           files.PatchResult        `json:"-"`
}

// ApplyPatch delegates to the managed patch application service.
func (o *Operations) ApplyPatch(ctx context.Context, request ApplyPatchRequest) (ApplyPatchResult, error) {
	managed, err := o.service.ApplyPatch(ctx, files.PatchRequest{
		Patch:            request.Patch,
		ApprovalRequired: request.ApprovalRequired,
	})
	if err != nil {
		return ApplyPatchResult{}, err
	}

	return adaptApplyPatchResult(managed), nil
}

func adaptApplyPatchResult(managed files.PatchResult) ApplyPatchResult {
	mayHaveOccurred := patchMutationMayHaveOccurred(managed)
	filesChanged := len(managed.Created) + len(managed.Modified) + len(managed.Deleted) + len(managed.Renamed)
	if mayHaveOccurred && filesChanged == 0 {
		filesChanged = 1
	}
	result := ApplyPatchResult{
		CommonResult: CommonResult{
			Operation: OperationApplyPatch,
			Success:   managed.Status == files.PatchStatusApplied,
			Status:    string(managed.Status),
			Error:     managed.Error,
		},
		Created:           patchFileSummaries(managed.Created),
		Modified:          patchFileSummaries(managed.Modified),
		Deleted:           patchFileSummaries(managed.Deleted),
		Renamed:           patchFileSummaries(managed.Renamed),
		Failed:            patchFileSummaries(managed.Failed),
		FilesRolledBack:   managed.FilesRolledBack,
		RollbackAttempted: managed.RollbackAttempted,
		RollbackSucceeded: managed.RollbackSucceeded,
		RollbackError:     managed.RollbackError,
		WorkspaceMutation: WorkspaceMutationSummary{
			Observed:          managed.Applied,
			MayHaveOccurred:   mayHaveOccurred,
			FilesChanged:      filesChanged,
			RollbackAttempted: managed.RollbackAttempted,
			RollbackSucceeded: managed.RollbackSucceeded,
		},
		Managed: managed,
	}

	result.BackupPaths = append(result.BackupPaths, backupPathsFromSummaries(result.Modified)...)
	result.BackupPaths = append(result.BackupPaths, backupPathsFromSummaries(result.Deleted)...)
	return result
}

func patchMutationMayHaveOccurred(managed files.PatchResult) bool {
	if managed.RollbackAttempted && !managed.RollbackSucceeded {
		return true
	}
	for _, file := range append(append(append([]files.PatchFileResult{}, managed.Created...), managed.Modified...), managed.Failed...) {
		if file.Write.MutationMayHaveOccurred || file.Delete.MutationMayHaveOccurred {
			return true
		}
	}
	for _, file := range managed.Deleted {
		if file.Delete.MutationMayHaveOccurred {
			return true
		}
	}
	return false
}

func patchFileSummaries(files []files.PatchFileResult) []FileChangeSummary {
	summaries := make([]FileChangeSummary, 0, len(files))
	for _, file := range files {
		summaries = append(summaries, FileChangeSummary{
			Operation:     patchOperation(file.Operation),
			Status:        patchFileStatus(file),
			Path:          file.Path,
			OldPath:       file.OldPath,
			NewPath:       file.NewPath,
			ResolvedPath:  file.ResolvedPath,
			BackupCreated: file.BackupCreated,
			BackupPath:    file.BackupPath,
			Error:         file.Error,
		})
	}
	return summaries
}

func patchOperation(operation files.PatchOperation) Operation {
	switch operation {
	case files.PatchOperationCreate:
		return OperationWriteFile
	case files.PatchOperationModify:
		return OperationWriteFile
	case files.PatchOperationDelete:
		return OperationDeleteFile
	case files.PatchOperationRename:
		return OperationApplyPatch
	default:
		return OperationApplyPatch
	}
}

func patchFileStatus(file files.PatchFileResult) string {
	switch file.Operation {
	case files.PatchOperationCreate:
		if file.Write.Status != "" {
			return string(file.Write.Status)
		}
	case files.PatchOperationModify:
		if file.Write.Status != "" {
			return string(file.Write.Status)
		}
	case files.PatchOperationDelete:
		if file.Delete.Status != "" {
			return string(file.Delete.Status)
		}
	}
	if file.Error != "" {
		return "failed"
	}
	return "ok"
}
