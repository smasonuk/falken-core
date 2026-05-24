package falken

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/smasonuk/falken-core/internal/files"
	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

type policyCheckingWorkspaceOperations struct {
	delegate workspacefiles.Operations
	policy   *runtimepolicy.Evaluator
	state    *runtimeexec.ExecutionState
	events   EventSink
}

func newPolicyCheckingWorkspaceOperations(delegate workspacefiles.Operations, evaluator *runtimepolicy.Evaluator, state *runtimeexec.ExecutionState, events ...EventSink) workspacefiles.Operations {
	if delegate == nil {
		return nil
	}
	var sink EventSink
	if len(events) != 0 {
		sink = events[0]
	}
	return &policyCheckingWorkspaceOperations{
		delegate: delegate,
		policy:   evaluator,
		state:    state,
		events:   sink,
	}
}

func (o *policyCheckingWorkspaceOperations) Start(ctx context.Context) error {
	if starter, ok := o.delegate.(workspaceFilesStarter); ok {
		return starter.Start(ctx)
	}
	return nil
}

func (o *policyCheckingWorkspaceOperations) Close(ctx context.Context) error {
	if closer, ok := o.delegate.(workspaceFilesCloser); ok {
		return closer.Close(ctx)
	}
	return nil
}

func (o *policyCheckingWorkspaceOperations) ReadFile(ctx context.Context, request workspacefiles.ReadFileRequest) (workspacefiles.ReadFileResult, error) {
	if failure, err := o.checkFilePolicy(ctx, request.CurrentWorkingDir, request.Path, policy.FileAccessRead, request.ApprovalRequired); err != nil {
		return workspacefiles.ReadFileResult{}, err
	} else if failure != nil {
		return workspacefiles.ReadFileResult{CommonResult: failure.common(workspacefiles.OperationReadFile, request.Path, readFailureStatus(failure))}, nil
	}
	return o.delegate.ReadFile(ctx, request)
}

func (o *policyCheckingWorkspaceOperations) ReadFiles(ctx context.Context, request workspacefiles.ReadFilesRequest) (workspacefiles.ReadFilesResult, error) {
	for _, file := range request.Files {
		if failure, err := o.checkFilePolicy(ctx, file.CurrentWorkingDir, file.Path, policy.FileAccessRead, file.ApprovalRequired); err != nil {
			return workspacefiles.ReadFilesResult{}, err
		} else if failure != nil {
			read := workspacefiles.ReadFileResult{CommonResult: failure.common(workspacefiles.OperationReadFile, file.Path, readFailureStatus(failure))}
			return workspacefiles.ReadFilesResult{
				Operation: workspacefiles.OperationReadFiles,
				Success:   false,
				Status:    "failed",
				Files:     []workspacefiles.ReadFileResult{read},
				Total:     len(request.Files),
				Failed:    1,
				Error:     failure.err,
			}, nil
		}
	}
	return o.delegate.ReadFiles(ctx, request)
}

func (o *policyCheckingWorkspaceOperations) Glob(ctx context.Context, request workspacefiles.GlobRequest) (workspacefiles.GlobResult, error) {
	root := strings.TrimSpace(request.Path)
	if root == "" {
		root = "."
	}
	if failure, err := o.checkFilePolicy(ctx, request.CurrentWorkingDir, root, policy.FileAccessRead, request.ApprovalRequired); err != nil {
		return workspacefiles.GlobResult{}, err
	} else if failure != nil {
		return workspacefiles.GlobResult{CommonResult: failure.common(workspacefiles.OperationGlob, root, globFailureStatus(failure)), Root: root, Pattern: request.Pattern}, nil
	}
	return o.delegate.Glob(ctx, request)
}

func (o *policyCheckingWorkspaceOperations) Grep(ctx context.Context, request workspacefiles.GrepRequest) (workspacefiles.GrepResult, error) {
	targets := request.TargetPaths
	if len(targets) == 0 {
		targets = []string{"."}
	}
	for _, target := range targets {
		if failure, err := o.checkFilePolicy(ctx, request.CurrentWorkingDir, target, policy.FileAccessRead, request.ApprovalRequired); err != nil {
			return workspacefiles.GrepResult{}, err
		} else if failure != nil {
			return workspacefiles.GrepResult{CommonResult: failure.common(workspacefiles.OperationGrep, target, grepFailureStatus(failure)), Regex: request.Regex}, nil
		}
	}
	return o.delegate.Grep(ctx, request)
}

func (o *policyCheckingWorkspaceOperations) WriteFile(ctx context.Context, request workspacefiles.WriteFileRequest) (workspacefiles.WriteFileResult, error) {
	modes, ok := writePolicyModes(request.Operation)
	if !ok {
		return o.delegate.WriteFile(ctx, request)
	}
	for _, mode := range modes {
		if failure, err := o.checkFilePolicy(ctx, request.CurrentWorkingDir, request.Path, mode, request.ApprovalRequired); err != nil {
			return workspacefiles.WriteFileResult{}, err
		} else if failure != nil {
			result := workspacefiles.WriteFileResult{CommonResult: failure.common(workspacefiles.OperationWriteFile, request.Path, writeFailureStatus(failure))}
			o.emitWorkspaceOperation(workspacefiles.OperationWriteFile, []string{request.Path}, result.Success, result.Status, false)
			return result, nil
		}
	}
	result, err := o.delegate.WriteFile(ctx, request)
	if err == nil {
		o.emitWorkspaceOperation(workspacefiles.OperationWriteFile, []string{request.Path}, result.Success, result.Status, result.WorkspaceMutation.Observed || result.WorkspaceMutation.FilesChanged > 0)
	}
	return result, err
}

func (o *policyCheckingWorkspaceOperations) EditFile(ctx context.Context, request workspacefiles.EditFileRequest) (workspacefiles.EditFileResult, error) {
	if failure, err := o.checkFilePolicy(ctx, request.CurrentWorkingDir, request.Path, policy.FileAccessWrite, request.ApprovalRequired); err != nil {
		return workspacefiles.EditFileResult{}, err
	} else if failure != nil {
		result := workspacefiles.EditFileResult{CommonResult: failure.common(workspacefiles.OperationEditFile, request.Path, editFailureStatus(failure))}
		o.emitWorkspaceOperation(workspacefiles.OperationEditFile, []string{request.Path}, result.Success, result.Status, false)
		return result, nil
	}
	result, err := o.delegate.EditFile(ctx, request)
	if err == nil {
		o.emitWorkspaceOperation(workspacefiles.OperationEditFile, []string{request.Path}, result.Success, result.Status, result.WorkspaceMutation.Observed || result.WorkspaceMutation.FilesChanged > 0)
	}
	return result, err
}

func (o *policyCheckingWorkspaceOperations) MultiEdit(ctx context.Context, request workspacefiles.MultiEditRequest) (workspacefiles.MultiEditResult, error) {
	for _, edit := range request.Edits {
		if failure, err := o.checkFilePolicy(ctx, edit.CurrentWorkingDir, edit.Path, policy.FileAccessWrite, edit.ApprovalRequired); err != nil {
			return workspacefiles.MultiEditResult{}, err
		} else if failure != nil {
			result := workspacefiles.MultiEditResult{
				CommonResult: failure.common(workspacefiles.OperationMultiEdit, edit.Path, multiEditFailureStatus(failure)),
				TotalFiles:   len(request.Edits),
			}
			o.emitWorkspaceOperation(workspacefiles.OperationMultiEdit, multiEditPaths(request), result.Success, result.Status, false)
			return result, nil
		}
	}
	result, err := o.delegate.MultiEdit(ctx, request)
	if err == nil {
		o.emitWorkspaceOperation(workspacefiles.OperationMultiEdit, multiEditPaths(request), result.Success, result.Status, result.WorkspaceMutation.Observed || result.WorkspaceMutation.FilesChanged > 0)
	}
	return result, err
}

func (o *policyCheckingWorkspaceOperations) ApplyPatch(ctx context.Context, request workspacefiles.ApplyPatchRequest) (workspacefiles.ApplyPatchResult, error) {
	checks, err := extractPatchPolicyChecks(request.Patch)
	if err != nil {
		result := workspacefiles.ApplyPatchResult{
			CommonResult: workspacefiles.CommonResult{
				Operation: workspacefiles.OperationApplyPatch,
				Success:   false,
				Status:    string(files.PatchStatusFailed),
				Error:     err.Error(),
			},
		}
		o.emitWorkspaceOperation(workspacefiles.OperationApplyPatch, nil, result.Success, result.Status, false)
		return result, nil
	}
	for _, check := range checks {
		if failure, err := o.checkFilePolicy(ctx, "", check.path, check.mode, request.ApprovalRequired); err != nil {
			return workspacefiles.ApplyPatchResult{}, err
		} else if failure != nil {
			result := workspacefiles.ApplyPatchResult{
				CommonResult: failure.common(workspacefiles.OperationApplyPatch, check.path, string(files.PatchStatusFailed)),
				Failed: []workspacefiles.FileChangeSummary{{
					Operation: check.summaryOperation,
					Status:    patchFileFailureStatus(failure),
					Path:      check.path,
					OldPath:   check.oldPath,
					NewPath:   check.newPath,
					Error:     failure.err,
				}},
			}
			o.emitWorkspaceOperation(workspacefiles.OperationApplyPatch, patchPolicyCheckPaths(checks), result.Success, result.Status, false)
			return result, nil
		}
	}
	result, err := o.delegate.ApplyPatch(ctx, request)
	if err == nil {
		o.emitWorkspaceOperation(workspacefiles.OperationApplyPatch, patchPolicyCheckPaths(checks), result.Success, result.Status, result.WorkspaceMutation.Observed || result.WorkspaceMutation.FilesChanged > 0)
	}
	return result, err
}

func (o *policyCheckingWorkspaceOperations) DeleteFile(ctx context.Context, request workspacefiles.DeleteFileRequest) (workspacefiles.DeleteFileResult, error) {
	if failure, err := o.checkFilePolicy(ctx, request.CurrentWorkingDir, request.Path, policy.FileAccessWrite, request.ApprovalRequired); err != nil {
		return workspacefiles.DeleteFileResult{}, err
	} else if failure != nil {
		result := workspacefiles.DeleteFileResult{CommonResult: failure.common(workspacefiles.OperationDeleteFile, request.Path, deleteFailureStatus(failure))}
		o.emitWorkspaceOperation(workspacefiles.OperationDeleteFile, []string{request.Path}, result.Success, result.Status, false)
		return result, nil
	}
	result, err := o.delegate.DeleteFile(ctx, request)
	if err == nil {
		o.emitWorkspaceOperation(workspacefiles.OperationDeleteFile, []string{request.Path}, result.Success, result.Status, result.WorkspaceMutation.Observed || result.WorkspaceMutation.FilesChanged > 0)
	}
	return result, err
}

func (o *policyCheckingWorkspaceOperations) emitWorkspaceOperation(operation workspacefiles.Operation, paths []string, success bool, status string, mutated bool) {
	if o == nil || o.events == nil {
		return
	}
	o.events(Event{
		Type: EventWorkspaceOperation,
		WorkspaceOperation: &WorkspaceOperationEvent{
			Operation: string(operation),
			Paths:     compactWorkspaceOperationPaths(paths),
			Mutated:   mutated,
			Success:   success,
			Status:    status,
			Remote:    true,
		},
	})
}

func compactWorkspaceOperationPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func multiEditPaths(request workspacefiles.MultiEditRequest) []string {
	paths := make([]string, 0, len(request.Edits))
	for _, edit := range request.Edits {
		paths = append(paths, edit.Path)
	}
	return paths
}

func patchPolicyCheckPaths(checks []patchPolicyCheck) []string {
	paths := make([]string, 0, len(checks))
	for _, check := range checks {
		paths = append(paths, check.path)
	}
	return paths
}

func (o *policyCheckingWorkspaceOperations) checkFilePolicy(ctx context.Context, cwd, path string, mode policy.FileAccessMode, approvalRequired bool) (*workspacePolicyFailure, error) {
	if o.state == nil {
		return nil, fmt.Errorf("workspace file policy execution state is required")
	}
	if o.policy == nil {
		return nil, fmt.Errorf("workspace file policy evaluator is required")
	}
	resolved, err := o.state.ResolvePathForPolicy(cwd, path, mode)
	if err != nil {
		return &workspacePolicyFailure{
			kind: workspacePolicyFailureUnsafe,
			path: path,
			err:  err.Error(),
		}, nil
	}
	result, err := o.policy.EvaluateFile(ctx, runtimepolicy.FileRequest{
		Path:             resolved,
		Mode:             mode,
		ApprovalRequired: approvalRequired,
	})
	if err != nil {
		return nil, fmt.Errorf("evaluate workspace file policy: %w", err)
	}
	if result.Allowed {
		return nil, nil
	}
	return &workspacePolicyFailure{
		kind:     workspacePolicyFailureDenied,
		path:     path,
		resolved: resolved,
		err:      result.Explanation,
	}, nil
}

type workspacePolicyFailureKind string

const (
	workspacePolicyFailureDenied workspacePolicyFailureKind = "denied"
	workspacePolicyFailureUnsafe workspacePolicyFailureKind = "unsafe"
)

type workspacePolicyFailure struct {
	kind     workspacePolicyFailureKind
	path     string
	resolved string
	err      string
}

func (f workspacePolicyFailure) common(operation workspacefiles.Operation, requestPath, status string) workspacefiles.CommonResult {
	return workspacefiles.CommonResult{
		Operation:    operation,
		Success:      false,
		Status:       status,
		Path:         requestPath,
		ResolvedPath: f.resolved,
		Error:        f.err,
	}
}

func readFailureStatus(failure *workspacePolicyFailure) string {
	if failure.kind == workspacePolicyFailureDenied {
		return string(files.ReadStatusDenied)
	}
	return string(files.ReadStatusUnsafe)
}

func globFailureStatus(failure *workspacePolicyFailure) string {
	if failure.kind == workspacePolicyFailureDenied {
		return string(files.GlobStatusDenied)
	}
	return string(files.GlobStatusUnsafe)
}

func grepFailureStatus(failure *workspacePolicyFailure) string {
	if failure.kind == workspacePolicyFailureDenied {
		return string(files.GrepStatusDenied)
	}
	return string(files.GrepStatusUnsafe)
}

func writeFailureStatus(failure *workspacePolicyFailure) string {
	if failure.kind == workspacePolicyFailureDenied {
		return string(files.WriteStatusDenied)
	}
	return string(files.WriteStatusUnsafe)
}

func editFailureStatus(failure *workspacePolicyFailure) string {
	if failure.kind == workspacePolicyFailureDenied {
		return string(files.EditStatusMutationRejected)
	}
	return string(files.EditStatusUnsafe)
}

func multiEditFailureStatus(*workspacePolicyFailure) string {
	return string(files.MultiEditStatusFailed)
}

func deleteFailureStatus(failure *workspacePolicyFailure) string {
	if failure.kind == workspacePolicyFailureDenied {
		return string(files.DeleteStatusDenied)
	}
	return string(files.DeleteStatusUnsafe)
}

func patchFileFailureStatus(failure *workspacePolicyFailure) string {
	if failure.kind == workspacePolicyFailureDenied {
		return string(files.WriteStatusDenied)
	}
	return string(files.WriteStatusUnsafe)
}

func writePolicyModes(operation files.WriteOperation) ([]policy.FileAccessMode, bool) {
	switch operation {
	case files.WriteOperationCreate:
		return []policy.FileAccessMode{policy.FileAccessCreate}, true
	case files.WriteOperationOverwrite:
		return []policy.FileAccessMode{policy.FileAccessWrite}, true
	case "", files.WriteOperationCreateOrOverwrite:
		return []policy.FileAccessMode{policy.FileAccessCreate, policy.FileAccessWrite}, true
	default:
		return nil, false
	}
}

type patchPolicyCheck struct {
	path             string
	oldPath          string
	newPath          string
	mode             policy.FileAccessMode
	summaryOperation workspacefiles.Operation
}

func extractPatchPolicyChecks(patch string) ([]patchPolicyCheck, error) {
	if strings.TrimSpace(patch) == "" {
		return nil, fmt.Errorf("patch is empty")
	}
	if strings.HasPrefix(strings.TrimSpace(patch), "*** Begin Patch") {
		return nil, fmt.Errorf("unsupported patch envelope: expected unified git diff")
	}

	lines := strings.Split(patch, "\n")
	checks := make([]patchPolicyCheck, 0)
	seenChecks := make(map[string]struct{})
	for i := 0; i < len(lines); {
		line := trimPatchLineEnding(lines[i])
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}
		if !strings.HasPrefix(line, "diff --git ") {
			return nil, fmt.Errorf("unsupported patch format: expected diff --git header")
		}

		oldRaw, newRaw, err := parsePolicyDiffGitHeader(line)
		if err != nil {
			return nil, err
		}
		oldPath, oldDevNull, err := parsePolicyPatchPath(oldRaw)
		if err != nil {
			return nil, err
		}
		newPath, newDevNull, err := parsePolicyPatchPath(newRaw)
		if err != nil {
			return nil, err
		}
		sawOldMarker := false
		sawNewMarker := false

		i++
		for i < len(lines) {
			line = trimPatchLineEnding(lines[i])
			switch {
			case strings.HasPrefix(line, "diff --git "):
				goto finishFile
			case strings.HasPrefix(line, "rename from ") || strings.HasPrefix(line, "rename to "):
				return nil, fmt.Errorf("rename patches are not supported yet")
			case strings.HasPrefix(line, "copy from ") || strings.HasPrefix(line, "copy to "):
				return nil, fmt.Errorf("copy patches are not supported")
			case line == "GIT binary patch" || strings.HasPrefix(line, "Binary files "):
				return nil, fmt.Errorf("binary patches are not supported")
			case strings.HasPrefix(line, "--- "):
				oldPath, oldDevNull, err = parsePolicyPatchPath(policyPathMarkerValue(line[4:]))
				if err != nil {
					return nil, err
				}
				sawOldMarker = true
			case strings.HasPrefix(line, "+++ "):
				newPath, newDevNull, err = parsePolicyPatchPath(policyPathMarkerValue(line[4:]))
				if err != nil {
					return nil, err
				}
				sawNewMarker = true
			}
			i++
		}

	finishFile:
		if !sawOldMarker || !sawNewMarker {
			return nil, fmt.Errorf("patch file is missing ---/+++ path markers")
		}
		check, err := classifyPatchPolicyCheck(oldPath, oldDevNull, newPath, newDevNull)
		if err != nil {
			return nil, err
		}
		key := string(check.mode) + "\x00" + check.path
		if _, ok := seenChecks[key]; !ok {
			seenChecks[key] = struct{}{}
			checks = append(checks, check)
		}
	}
	if len(checks) == 0 {
		return nil, fmt.Errorf("patch does not contain any file changes")
	}
	return checks, nil
}

func classifyPatchPolicyCheck(oldPath string, oldDevNull bool, newPath string, newDevNull bool) (patchPolicyCheck, error) {
	switch {
	case oldDevNull && newDevNull:
		return patchPolicyCheck{}, fmt.Errorf("patch file cannot use /dev/null for both old and new paths")
	case oldDevNull:
		return patchPolicyCheck{
			path:             newPath,
			newPath:          newPath,
			mode:             policy.FileAccessCreate,
			summaryOperation: workspacefiles.OperationWriteFile,
		}, nil
	case newDevNull:
		return patchPolicyCheck{
			path:             oldPath,
			oldPath:          oldPath,
			mode:             policy.FileAccessWrite,
			summaryOperation: workspacefiles.OperationDeleteFile,
		}, nil
	case oldPath != newPath:
		return patchPolicyCheck{}, fmt.Errorf("rename/copy patches are not supported")
	default:
		return patchPolicyCheck{
			path:             newPath,
			oldPath:          oldPath,
			newPath:          newPath,
			mode:             policy.FileAccessWrite,
			summaryOperation: workspacefiles.OperationWriteFile,
		}, nil
	}
}

func parsePolicyPatchPath(raw string) (string, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "/dev/null" {
		return "", true, nil
	}
	if value == "" {
		return "", false, fmt.Errorf("patch path is required")
	}
	if strings.HasPrefix(value, `"`) {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", false, fmt.Errorf("parse quoted patch path: %w", err)
		}
		value = unquoted
	}
	if strings.HasPrefix(value, "a/") || strings.HasPrefix(value, "b/") {
		value = value[2:]
	}
	if value == "" {
		return "", false, fmt.Errorf("patch path is required")
	}
	return filepath.Clean(value), false, nil
}

func policyPathMarkerValue(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, `"`) {
		token, _, err := scanPolicyPatchToken(value)
		if err == nil {
			return token
		}
		return value
	}
	if index := strings.IndexAny(value, "\t "); index >= 0 {
		value = value[:index]
	}
	return value
}

func parsePolicyDiffGitHeader(line string) (string, string, error) {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "diff --git "))
	oldPath, remaining, err := scanPolicyPatchToken(rest)
	if err != nil {
		return "", "", fmt.Errorf("malformed diff --git header: %w", err)
	}
	newPath, remaining, err := scanPolicyPatchToken(remaining)
	if err != nil {
		return "", "", fmt.Errorf("malformed diff --git header: %w", err)
	}
	if strings.TrimSpace(remaining) != "" {
		return "", "", fmt.Errorf("malformed diff --git header")
	}
	return oldPath, newPath, nil
}

func scanPolicyPatchToken(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", fmt.Errorf("missing path")
	}
	if !strings.HasPrefix(value, `"`) {
		if index := strings.IndexAny(value, "\t "); index >= 0 {
			return value[:index], value[index:], nil
		}
		return value, "", nil
	}
	escaped := false
	for i := 1; i < len(value); i++ {
		switch value[i] {
		case '\\':
			escaped = !escaped
		case '"':
			if escaped {
				escaped = false
				continue
			}
			token := value[:i+1]
			unquoted, err := strconv.Unquote(token)
			if err != nil {
				return "", "", err
			}
			return unquoted, value[i+1:], nil
		default:
			escaped = false
		}
	}
	return "", "", fmt.Errorf("unterminated quoted path")
}

func trimPatchLineEnding(line string) string {
	return strings.TrimSuffix(line, "\r")
}
