package runtimefiles

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/smasonuk/falken-core/internal/files"
)

// WriteFileRequest describes a runtime-facing managed create or overwrite.
type WriteFileRequest struct {
	Path              string               `json:"path"`
	CurrentWorkingDir string               `json:"working_dir,omitempty"`
	Content           string               `json:"content"`
	Operation         files.WriteOperation `json:"operation,omitempty"`
	Mode              string               `json:"mode,omitempty"`
	ApprovalRequired  bool                 `json:"approval_required,omitempty"`
}

// WriteFileResult reports a runtime-facing managed create or overwrite.
type WriteFileResult struct {
	CommonResult
	Created                 bool                     `json:"created"`
	Overwritten             bool                     `json:"overwritten"`
	BytesWritten            int                      `json:"bytes_written"`
	MutationMayHaveOccurred bool                     `json:"mutation_may_have_occurred,omitempty"`
	WorkspaceMutation       WorkspaceMutationSummary `json:"workspace_mutation"`
	Token                   files.ReadToken          `json:"-"`
	HasToken                bool                     `json:"has_token"`
	Managed                 files.WriteResult        `json:"-"`
}

// WriteFile delegates to the managed create/overwrite service.
func (o *Operations) WriteFile(ctx context.Context, request WriteFileRequest) (WriteFileResult, error) {
	mode, err := parseFileMode(request.Mode)
	if err != nil {
		return WriteFileResult{
			CommonResult: CommonResult{
				Operation: OperationWriteFile,
				Success:   false,
				Status:    "invalid_arguments",
				Path:      request.Path,
				Error:     err.Error(),
			},
		}, nil
	}
	managed, err := o.service.Write(ctx, files.WriteRequest{
		Path:              request.Path,
		CurrentWorkingDir: request.CurrentWorkingDir,
		Content:           request.Content,
		Operation:         request.Operation,
		Mode:              mode,
		ApprovalRequired:  request.ApprovalRequired,
	})
	if err != nil {
		return WriteFileResult{}, err
	}

	return adaptWriteFileResult(managed), nil
}

func adaptWriteFileResult(managed files.WriteResult) WriteFileResult {
	filesChanged := 0
	if managed.Created || managed.Overwritten || managed.MutationMayHaveOccurred {
		filesChanged = 1
	}
	result := WriteFileResult{
		CommonResult: CommonResult{
			Operation:    OperationWriteFile,
			Success:      managed.Status == files.WriteStatusCreated || managed.Status == files.WriteStatusOverwritten,
			Status:       string(managed.Status),
			Path:         managed.Path,
			ResolvedPath: managed.ResolvedPath,
			Error:        managed.Error,
			BackupPaths:  backupPaths(managed.BackupPath),
		},
		Created:                 managed.Created,
		Overwritten:             managed.Overwritten,
		BytesWritten:            managed.BytesWritten,
		MutationMayHaveOccurred: managed.MutationMayHaveOccurred,
		WorkspaceMutation: WorkspaceMutationSummary{
			Observed:        managed.Created || managed.Overwritten,
			MayHaveOccurred: managed.MutationMayHaveOccurred,
			FilesChanged:    filesChanged,
		},
		Token:    managed.Token,
		HasToken: managed.HasToken,
		Managed:  managed,
	}
	return result
}

// parseFileMode converts an octal string representation of file permissions into an os.FileMode.
func parseFileMode(value string) (os.FileMode, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	if len(value) != 4 || value[0] != '0' {
		return 0, fmt.Errorf("file mode %q must be a 4-digit octal permission string like 0644", value)
	}
	parsed, err := strconv.ParseUint(value, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("parse file mode %q as octal permissions: %w", value, err)
	}
	mode := os.FileMode(parsed)
	if mode != mode.Perm() {
		return 0, fmt.Errorf("file mode %q contains non-permission bits", value)
	}
	return mode, nil
}
