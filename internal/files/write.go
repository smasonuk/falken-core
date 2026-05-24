package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
)

// WriteOperation identifies the managed mutation intent.
type WriteOperation string

const (
	WriteOperationCreate            WriteOperation = "create"
	WriteOperationOverwrite         WriteOperation = "overwrite"
	WriteOperationCreateOrOverwrite WriteOperation = "create_or_overwrite"
)

// WriteStatus identifies the deterministic outcome of a managed write.
type WriteStatus string

const (
	WriteStatusCreated          WriteStatus = "created"
	WriteStatusOverwritten      WriteStatus = "overwritten"
	WriteStatusDenied           WriteStatus = "denied"
	WriteStatusUnsafe           WriteStatus = "unsafe_path"
	WriteStatusAlreadyExists    WriteStatus = "already_exists"
	WriteStatusNotFound         WriteStatus = "not_found"
	WriteStatusDirectory        WriteStatus = "directory"
	WriteStatusMissingReadToken WriteStatus = "missing_read_token"
	WriteStatusStaleReadToken   WriteStatus = "stale_read_token"
	WriteStatusSecretRejected   WriteStatus = "secret_rejected"
	WriteStatusInvalidOperation WriteStatus = "invalid_operation"
	WriteStatusCommitUncertain  WriteStatus = "commit_uncertain"
)

// WriteRequest describes a managed create or full-file overwrite.
type WriteRequest struct {
	Path              string
	CurrentWorkingDir string
	Content           string
	Operation         WriteOperation
	Mode              os.FileMode
	ApprovalRequired  bool
}

// WriteResult is the structured result of a managed mutation.
type WriteResult struct {
	Status                  WriteStatus
	Path                    string
	ResolvedPath            string
	Created                 bool
	Overwritten             bool
	BackupCreated           bool
	BackupPath              string
	BytesWritten            int
	Policy                  runtimepolicy.FileResult
	Token                   ReadToken
	HasToken                bool
	MutationMayHaveOccurred bool
	Error                   string
}

// Write creates a new file or overwrites an existing file through the managed mutation path.
func (s *Service) Write(ctx context.Context, request WriteRequest) (WriteResult, error) {
	operation := request.Operation
	if operation == "" {
		operation = WriteOperationCreateOrOverwrite
	}

	result := WriteResult{
		Path: request.Path,
	}
	if !isValidWriteOperation(operation) {
		result.Status = WriteStatusInvalidOperation
		result.Error = "invalid write operation"
		return result, nil
	}

	resolved, exists, stat, err := s.resolveWriteTarget(operation, request)
	if err != nil {
		result.Error = err.Error()
		if errors.Is(err, os.ErrNotExist) {
			result.Status = WriteStatusNotFound
		} else {
			result.Status = WriteStatusUnsafe
		}
		return result, nil
	}
	result.ResolvedPath = resolved

	if exists && stat.IsDir() {
		result.Status = WriteStatusDirectory
		result.Error = "path is a directory"
		return result, nil
	}

	if operation == WriteOperationCreate && exists {
		result.Status = WriteStatusAlreadyExists
		result.Error = "target already exists"
		return result, nil
	}
	if operation == WriteOperationOverwrite && !exists {
		result.Status = WriteStatusNotFound
		result.Error = "target does not exist"
		return result, nil
	}

	policyResult, err := s.policy.EvaluateFile(ctx, runtimepolicy.FileRequest{
		Path:             resolved,
		Mode:             writeAccessMode(exists),
		ApprovalRequired: request.ApprovalRequired,
	})
	if err != nil {
		return WriteResult{}, fmt.Errorf("evaluate file write policy: %w", err)
	}
	result.Policy = policyResult
	if !policyResult.Allowed {
		result.Status = WriteStatusDenied
		result.Error = policyResult.Explanation
		return result, nil
	}

	if exists {
		validation, err := s.tokens.ValidateCurrent(resolved)
		if err != nil {
			return WriteResult{}, fmt.Errorf("validate read token: %w", err)
		}
		if !validation.Found {
			result.Status = WriteStatusMissingReadToken
			result.Error = validation.Reason
			return result, nil
		}
		if !validation.Matches {
			result.Status = WriteStatusStaleReadToken
			result.Error = validation.Reason
			return result, nil
		}
		result.Token = validation.Token
	}

	if reason := scanSecretContent(request.Content); reason != "" {
		result.Status = WriteStatusSecretRejected
		result.Error = reason
		return result, nil
	}

	mode := writeMode(request.Mode, exists, stat)
	if exists {
		backupPath, err := s.backupExistingFile(resolved)
		if err != nil {
			return WriteResult{}, err
		}
		result.BackupCreated = true
		result.BackupPath = backupPath
	}

	noReplace := operation == WriteOperationCreate || (operation == WriteOperationCreateOrOverwrite && !exists)
	var expectedToken *ReadToken
	if exists {
		expectedToken = &result.Token
	}
	if err := s.writeWorkspaceFile(resolved, []byte(request.Content), mode, noReplace, expectedToken); err != nil {
		if errors.Is(err, errWorkspaceFileAlreadyExists) {
			result.Status = WriteStatusAlreadyExists
			result.Error = "target already exists"
			return result, nil
		}
		if errors.Is(err, errMutationMayHaveOccurred) {
			result.Status = WriteStatusCommitUncertain
			result.Error = err.Error()
			result.MutationMayHaveOccurred = true
			if exists {
				result.Overwritten = true
			} else {
				result.Created = true
			}
			return result, nil
		}
		if errors.Is(err, errCommitReadTokenStale) {
			result.Status = WriteStatusStaleReadToken
			result.Error = err.Error()
			return result, nil
		}
		return WriteResult{}, err
	}

	token, err := tokenForFile(s.tokens.ScopeID(), resolved, time.Now().UTC())
	if err != nil {
		return WriteResult{}, err
	}
	s.tokens.record(token)

	result.BytesWritten = len([]byte(request.Content))
	result.Token = token
	result.HasToken = true
	if exists {
		result.Status = WriteStatusOverwritten
		result.Overwritten = true
	} else {
		result.Status = WriteStatusCreated
		result.Created = true
	}
	return result, nil
}

func (s *Service) resolveWriteTarget(operation WriteOperation, request WriteRequest) (string, bool, os.FileInfo, error) {
	var resolved string
	var err error
	switch operation {
	case WriteOperationOverwrite:
		resolved, err = s.resolveExisting(request.CurrentWorkingDir, request.Path)
	default:
		resolved, err = s.resolveForCreate(request.CurrentWorkingDir, request.Path)
	}
	if err != nil {
		return "", false, nil, err
	}

	stat, err := os.Stat(resolved)
	switch {
	case err == nil:
		return resolved, true, stat, nil
	case errors.Is(err, os.ErrNotExist):
		return resolved, false, nil, nil
	default:
		return "", false, nil, fmt.Errorf("stat write target: %w", err)
	}
}

func writeAccessMode(exists bool) policy.FileAccessMode {
	if exists {
		return policy.FileAccessWrite
	}
	return policy.FileAccessCreate
}

func writeMode(requested os.FileMode, exists bool, stat os.FileInfo) os.FileMode {
	if requested != 0 {
		return requested.Perm()
	}
	if exists && stat != nil {
		return stat.Mode().Perm()
	}
	return 0o644
}

func (s *Service) commitReadTokenValidator(expected *ReadToken) func() error {
	if expected == nil {
		return nil
	}
	return func() error {
		current, err := tokenForFile(expected.ScopeID, expected.Path, time.Time{})
		if err != nil {
			return fmt.Errorf("validate target before commit: %w", err)
		}
		if !sameFileVersion(*expected, current) {
			return errCommitReadTokenStale
		}
		return nil
	}
}

func (s *Service) writeWorkspaceFile(path string, data []byte, perm os.FileMode, noReplace bool, expectedToken *ReadToken) error {
	if s.writeFileAtomic != nil {
		return s.writeFileAtomic(path, data, perm, noReplace)
	}
	return writeWorkspaceFileAtomicModeWithValidation(path, data, perm, noReplace, s.commitReadTokenValidator(expectedToken), s.realWorkspaceRoot)
}

// writeWorkspaceFileAtomic is a low-level helper for tests and rollback paths
// that trust only the destination's immediate parent directory.
func writeWorkspaceFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeWorkspaceFileAtomicMode(path, data, perm, false, filepath.Dir(path))
}

// writeWorkspaceFileNoReplaceAtomic is a low-level helper for tests and
// rollback paths that trust only the destination's immediate parent directory.
func writeWorkspaceFileNoReplaceAtomic(path string, data []byte, perm os.FileMode) error {
	return writeWorkspaceFileAtomicMode(path, data, perm, true, filepath.Dir(path))
}

// writeWorkspaceFileAtomicMode is the managed workspace mutation primitive.
// It opens and commits through directory file descriptors so TOCTOU and
// symlink escapes are rejected relative to trustedRoots. All workspace
// mutations should enter through Service write/edit/delete/patch operations,
// which layer policy checks, read-token validation, and backups over this path.
func writeWorkspaceFileAtomicMode(path string, data []byte, perm os.FileMode, noReplace bool, trustedRoots ...string) error {
	return writeWorkspaceFileAtomicModeWithValidation(path, data, perm, noReplace, nil, trustedRoots...)
}

var syncManagedParentDir = syncParentFile

var (
	managedWriteBeforeCreateTempHook func(string)
	managedWriteBeforeCommitHook     func(string)
	managedBackupBeforeReadHook      func(string)
	managedDeleteBeforeUnlinkHook    func(string)
)

func isValidWriteOperation(operation WriteOperation) bool {
	switch operation {
	case WriteOperationCreate, WriteOperationOverwrite, WriteOperationCreateOrOverwrite:
		return true
	default:
		return false
	}
}
