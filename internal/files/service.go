package files

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/workspace"
)

var (
	// ErrPolicyRequired indicates the read service was created without a policy evaluator.
	ErrPolicyRequired = errors.New("runtime policy evaluator is required")
	// ErrInvalidLineRange indicates a read request supplied an invalid line range.
	ErrInvalidLineRange = errors.New("invalid line range")
	// ErrBackupRootRequired indicates mutation needs a canonical backup root.
	ErrUnsupportedPlatform        = errors.New("managed file service is not supported on this platform")
	ErrBackupRootRequired         = errors.New("backup root is required")
	errWorkspaceFileAlreadyExists = errors.New("workspace file already exists")
	errMutationMayHaveOccurred    = errors.New("mutation may have occurred")
	errCommitReadTokenStale       = errors.New("target changed before commit")
)

type mutationMayHaveOccurredError struct {
	operation string
	err       error
}

func (e mutationMayHaveOccurredError) Error() string {
	if e.operation == "" {
		return errMutationMayHaveOccurred.Error() + ": " + e.err.Error()
	}
	return e.operation + ": " + errMutationMayHaveOccurred.Error() + ": " + e.err.Error()
}

func (e mutationMayHaveOccurredError) Unwrap() error {
	return e.err
}

func (e mutationMayHaveOccurredError) Is(target error) bool {
	return target == errMutationMayHaveOccurred
}

// Service is the canonical managed file read/write service.
type Service struct {
	workspaceRoot         string
	realWorkspaceRoot     string
	sandboxMountPath      string
	backupRoot            string
	policy                *runtimepolicy.Evaluator
	tokens                *TokenRegistry
	patchCommitHook       func(patchPlanFile) (patchCommitOutcome, bool)
	patchRollbackHook     func(patchRollbackEntry) error
	multiEditRollbackHook func(multiEditRollbackEntry) error
	writeFileAtomic       func(string, []byte, os.FileMode, bool) error
}

// NewService creates a managed read service rooted at the canonical workspace.
func NewService(workspaceRoot string, policyEvaluator *runtimepolicy.Evaluator, scopeID string) (*Service, error) {
	if policyEvaluator == nil {
		return nil, ErrPolicyRequired
	}
	if !managedFileServiceSupported() {
		return nil, ErrUnsupportedPlatform
	}

	root, err := workspace.NormalizeRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root symlinks: %w", err)
	}

	return &Service{
		workspaceRoot:     root,
		realWorkspaceRoot: filepath.Clean(realRoot),
		sandboxMountPath:  "/workspace",
		policy:            policyEvaluator,
		tokens:            NewTokenRegistry(scopeID),
	}, nil
}

// NewServiceForLayout creates a managed file service from the canonical state layout.
func NewServiceForLayout(layout state.Layout, policyEvaluator *runtimepolicy.Evaluator, scopeID string) (*Service, error) {
	service, err := NewService(layout.WorkspaceRoot, policyEvaluator, scopeID)
	if err != nil {
		return nil, err
	}

	if layout.BackupRoot != "" {
		backupRoot, err := filepath.Abs(layout.BackupRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve backup root: %w", err)
		}
		service.backupRoot = filepath.Clean(backupRoot)
		if err := os.MkdirAll(service.backupRoot, 0o755); err != nil {
			return nil, fmt.Errorf("create backup root: %w", err)
		}
	}

	return service, nil
}

// WorkspaceRoot returns the canonical workspace root used for reads.
func (s *Service) WorkspaceRoot() string {
	return s.workspaceRoot
}

// SetSandboxMountPath configures the sandbox-visible workspace mount path accepted by file tools.
func (s *Service) SetSandboxMountPath(path string) {
	if s == nil || strings.TrimSpace(path) == "" {
		return
	}
	s.sandboxMountPath = path
}

// BackupRoot returns the canonical root used for managed overwrite backups.
func (s *Service) BackupRoot() string {
	return s.backupRoot
}

// Tokens returns the run-scoped in-memory token registry.
func (s *Service) Tokens() *TokenRegistry {
	return s.tokens
}

func (s *Service) resolveExisting(cwd, targetPath string) (string, error) {
	return workspace.ResolveExistingWithSandboxMount(s.workspaceRoot, cwd, targetPath, s.sandboxMountPath)
}

func (s *Service) resolveForCreate(cwd, targetPath string) (string, error) {
	return workspace.ResolveForCreateWithSandboxMount(s.workspaceRoot, cwd, targetPath, s.sandboxMountPath)
}
