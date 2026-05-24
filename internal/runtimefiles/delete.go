package runtimefiles

import (
	"context"

	"github.com/smasonuk/falken-core/internal/files"
)

// DeleteFileRequest describes a runtime-facing managed file deletion.
type DeleteFileRequest struct {
	Path              string `json:"path"`
	CurrentWorkingDir string `json:"working_dir,omitempty"`
	ApprovalRequired  bool   `json:"approval_required,omitempty"`
}

// DeleteFileResult reports a runtime-facing managed file deletion.
type DeleteFileResult struct {
	CommonResult
	Deleted                 bool                     `json:"deleted"`
	MutationMayHaveOccurred bool                     `json:"mutation_may_have_occurred,omitempty"`
	WorkspaceMutation       WorkspaceMutationSummary `json:"workspace_mutation"`
	Managed                 files.DeleteResult       `json:"-"`
}

// DeleteFile delegates to the managed delete service.
func (o *Operations) DeleteFile(ctx context.Context, request DeleteFileRequest) (DeleteFileResult, error) {
	managed, err := o.service.Delete(ctx, files.DeleteRequest{
		Path:              request.Path,
		CurrentWorkingDir: request.CurrentWorkingDir,
		ApprovalRequired:  request.ApprovalRequired,
	})
	if err != nil {
		return DeleteFileResult{}, err
	}

	return adaptDeleteFileResult(managed), nil
}

func adaptDeleteFileResult(managed files.DeleteResult) DeleteFileResult {
	filesChanged := 0
	if managed.Deleted || managed.MutationMayHaveOccurred {
		filesChanged = 1
	}
	return DeleteFileResult{
		CommonResult: CommonResult{
			Operation:    OperationDeleteFile,
			Success:      managed.Status == files.DeleteStatusDeleted,
			Status:       string(managed.Status),
			Path:         managed.Path,
			ResolvedPath: managed.ResolvedPath,
			Error:        managed.Error,
			BackupPaths:  backupPaths(managed.BackupPath),
		},
		Deleted:                 managed.Deleted,
		MutationMayHaveOccurred: managed.MutationMayHaveOccurred,
		WorkspaceMutation: WorkspaceMutationSummary{
			Observed:        managed.Deleted,
			MayHaveOccurred: managed.MutationMayHaveOccurred,
			FilesChanged:    filesChanged,
		},
		Managed: managed,
	}
}
