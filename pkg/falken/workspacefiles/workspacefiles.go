// Package workspacefiles exposes Falken's public workspace file operation
// contract for local and remote implementations.
package workspacefiles

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/smasonuk/falken-core/internal/files"
	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/workspace"
)

// Operations is the public workspace file operations API shared by Core-local
// managed file operations and remote workspace clients.
type Operations interface {
	ReadFile(context.Context, ReadFileRequest) (ReadFileResult, error)
	ReadFiles(context.Context, ReadFilesRequest) (ReadFilesResult, error)
	Glob(context.Context, GlobRequest) (GlobResult, error)
	Grep(context.Context, GrepRequest) (GrepResult, error)
	WriteFile(context.Context, WriteFileRequest) (WriteFileResult, error)
	EditFile(context.Context, EditFileRequest) (EditFileResult, error)
	MultiEdit(context.Context, MultiEditRequest) (MultiEditResult, error)
	ApplyPatch(context.Context, ApplyPatchRequest) (ApplyPatchResult, error)
	DeleteFile(context.Context, DeleteFileRequest) (DeleteFileResult, error)
}

// Operation identifies a workspace file operation.
type Operation = runtimefiles.Operation

const (
	OperationReadFile   = runtimefiles.OperationReadFile
	OperationReadFiles  = runtimefiles.OperationReadFiles
	OperationGlob       = runtimefiles.OperationGlob
	OperationGrep       = runtimefiles.OperationGrep
	OperationWriteFile  = runtimefiles.OperationWriteFile
	OperationEditFile   = runtimefiles.OperationEditFile
	OperationMultiEdit  = runtimefiles.OperationMultiEdit
	OperationApplyPatch = runtimefiles.OperationApplyPatch
	OperationDeleteFile = runtimefiles.OperationDeleteFile
)

// Public request/result shapes are aliases of Core's existing runtime file
// DTOs so JSON compatibility stays exact.
type (
	CommonResult             = runtimefiles.CommonResult
	FileChangeSummary        = runtimefiles.FileChangeSummary
	WorkspaceMutationSummary = runtimefiles.WorkspaceMutationSummary

	ReadFileRequest  = runtimefiles.ReadFileRequest
	ReadFileResult   = runtimefiles.ReadFileResult
	ReadFilesRequest = runtimefiles.ReadFilesRequest
	ReadFilesResult  = runtimefiles.ReadFilesResult

	GlobRequest = runtimefiles.GlobRequest
	GlobResult  = runtimefiles.GlobResult
	GrepRequest = runtimefiles.GrepRequest
	GrepMatch   = runtimefiles.GrepMatch
	GrepResult  = runtimefiles.GrepResult

	WriteFileRequest = runtimefiles.WriteFileRequest
	WriteFileResult  = runtimefiles.WriteFileResult

	EditFileRequest  = runtimefiles.EditFileRequest
	EditFileResult   = runtimefiles.EditFileResult
	MultiEditRequest = runtimefiles.MultiEditRequest
	MultiEditResult  = runtimefiles.MultiEditResult

	ApplyPatchRequest = runtimefiles.ApplyPatchRequest
	ApplyPatchResult  = runtimefiles.ApplyPatchResult

	DeleteFileRequest = runtimefiles.DeleteFileRequest
	DeleteFileResult  = runtimefiles.DeleteFileResult
)

// LocalPolicyMode selects the policy behavior used by NewLocalOperations.
type LocalPolicyMode string

const (
	// LocalPolicyAllowAll allows all file operations while preserving workspace
	// rooting, path safety, backups, secret scanning, and read-before-write tokens.
	LocalPolicyAllowAll LocalPolicyMode = "allow_all"
)

// LocalOperationsConfig configures local managed workspace file operations.
type LocalOperationsConfig struct {
	WorkspaceRoot    string
	StateRoot        string
	SandboxMountPath string
	ScopeID          string
	PolicyMode       LocalPolicyMode
}

// NewLocalOperations creates local managed workspace operations using Core's
// existing managed file service. When StateRoot is empty, runner-local state is
// stored under <workspace>/.falken-runner-state.
func NewLocalOperations(config LocalOperationsConfig) (Operations, error) {
	if config.PolicyMode == "" {
		config.PolicyMode = LocalPolicyAllowAll
	}
	if config.PolicyMode != LocalPolicyAllowAll {
		return nil, fmt.Errorf("unsupported local workspace file policy mode %q", config.PolicyMode)
	}

	workspaceRoot, err := workspace.NormalizeRoot(config.WorkspaceRoot)
	if err != nil {
		return nil, err
	}

	stateRoot := strings.TrimSpace(config.StateRoot)
	if stateRoot == "" {
		stateRoot = filepath.Join(workspaceRoot, ".falken-runner-state")
	}
	layout, err := state.ResolveLayout(workspaceRoot, stateRoot)
	if err != nil {
		return nil, err
	}

	engine := policy.NewEngine(policy.Config{}, allowAllApprovalHandler{})
	evaluator := runtimepolicy.NewEvaluator(engine)
	service, err := files.NewServiceForLayout(layout, evaluator, config.ScopeID)
	if err != nil {
		return nil, fmt.Errorf("initialize managed file service: %w", err)
	}
	if config.SandboxMountPath != "" {
		service.SetSandboxMountPath(config.SandboxMountPath)
	}
	operations, err := runtimefiles.NewOperations(service)
	if err != nil {
		return nil, fmt.Errorf("initialize runtime file operations: %w", err)
	}
	return operations, nil
}

type allowAllApprovalHandler struct{}

func (allowAllApprovalHandler) ApproveFile(context.Context, policy.FileRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeOnce, nil
}

func (allowAllApprovalHandler) ApproveShell(context.Context, policy.ShellRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeOnce, nil
}

func (allowAllApprovalHandler) ApproveNetwork(context.Context, policy.NetworkRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeOnce, nil
}
