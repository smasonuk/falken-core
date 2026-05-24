package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
)

// DeleteStatus identifies the deterministic outcome of a managed delete.
type DeleteStatus string

const (
	DeleteStatusDeleted          DeleteStatus = "deleted"
	DeleteStatusDenied           DeleteStatus = "denied"
	DeleteStatusUnsafe           DeleteStatus = "unsafe_path"
	DeleteStatusNotFound         DeleteStatus = "not_found"
	DeleteStatusDirectory        DeleteStatus = "directory"
	DeleteStatusMissingReadToken DeleteStatus = "missing_read_token"
	DeleteStatusStaleReadToken   DeleteStatus = "stale_read_token"
	DeleteStatusCommitUncertain  DeleteStatus = "commit_uncertain"
)

// DeleteRequest describes a managed file deletion.
type DeleteRequest struct {
	Path              string
	CurrentWorkingDir string
	ApprovalRequired  bool
}

// DeleteResult is the structured result of a managed deletion.
type DeleteResult struct {
	Status                  DeleteStatus
	Path                    string
	ResolvedPath            string
	Deleted                 bool
	BackupCreated           bool
	BackupPath              string
	Policy                  runtimepolicy.FileResult
	MutationMayHaveOccurred bool
	Error                   string
}

// Delete removes an existing file through the managed mutation path.
func (s *Service) Delete(ctx context.Context, request DeleteRequest) (DeleteResult, error) {
	result := DeleteResult{
		Path: request.Path,
	}

	resolved, err := s.resolveExisting(request.CurrentWorkingDir, request.Path)
	if err != nil {
		result.Error = err.Error()
		if errors.Is(err, os.ErrNotExist) {
			result.Status = DeleteStatusNotFound
		} else {
			result.Status = DeleteStatusUnsafe
		}
		return result, nil
	}
	result.ResolvedPath = resolved

	stat, err := os.Stat(resolved)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("stat delete target: %w", err)
	}
	if stat.IsDir() {
		result.Status = DeleteStatusDirectory
		result.Error = "path is a directory"
		return result, nil
	}

	policyResult, err := s.policy.EvaluateFile(ctx, runtimepolicy.FileRequest{
		Path:             resolved,
		Mode:             policy.FileAccessWrite,
		ApprovalRequired: request.ApprovalRequired,
	})
	if err != nil {
		return DeleteResult{}, fmt.Errorf("evaluate file delete policy: %w", err)
	}
	result.Policy = policyResult
	if !policyResult.Allowed {
		result.Status = DeleteStatusDenied
		result.Error = policyResult.Explanation
		return result, nil
	}

	validation, err := s.tokens.ValidateCurrent(resolved)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("validate read token: %w", err)
	}
	if !validation.Found {
		result.Status = DeleteStatusMissingReadToken
		result.Error = validation.Reason
		return result, nil
	}
	if !validation.Matches {
		result.Status = DeleteStatusStaleReadToken
		result.Error = validation.Reason
		return result, nil
	}
	expectedToken := validation.Token

	backupPath, err := s.backupExistingFile(resolved)
	if err != nil {
		return DeleteResult{}, err
	}
	result.BackupCreated = true
	result.BackupPath = backupPath

	if managedDeleteBeforeUnlinkHook != nil {
		managedDeleteBeforeUnlinkHook(resolved)
	}
	current, err := tokenForFile(expectedToken.ScopeID, expectedToken.Path, time.Time{})
	if err != nil {
		return DeleteResult{}, fmt.Errorf("validate target before delete commit: %w", err)
	}
	if !sameFileVersion(expectedToken, current) {
		result.Status = DeleteStatusStaleReadToken
		result.Error = errCommitReadTokenStale.Error()
		return result, nil
	}
	if err := deleteManagedExistingFile(resolved, s.realWorkspaceRoot); err != nil {
		if errors.Is(err, errMutationMayHaveOccurred) {
			result.Status = DeleteStatusCommitUncertain
			result.Deleted = true
			result.MutationMayHaveOccurred = true
			result.Error = err.Error()
			return result, nil
		}
		return DeleteResult{}, fmt.Errorf("delete file: %w", err)
	}

	s.tokens.forget(resolved)
	result.Status = DeleteStatusDeleted
	result.Deleted = true
	return result, nil
}
