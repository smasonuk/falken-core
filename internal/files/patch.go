package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/workspace"
)

// PatchStatus identifies the aggregate outcome of a patch application request.
type PatchStatus string

const (
	PatchStatusApplied PatchStatus = "applied"
	PatchStatusFailed  PatchStatus = "failed"
)

// PatchOperation identifies the file operation represented by one patch file.
type PatchOperation string

const (
	PatchOperationCreate PatchOperation = "create"
	PatchOperationModify PatchOperation = "modify"
	PatchOperationDelete PatchOperation = "delete"
	PatchOperationRename PatchOperation = "rename"
)

// PatchRequest describes a unified git-style patch application request.
type PatchRequest struct {
	Patch            string
	ApprovalRequired bool
}

// PatchResult reports the structured outcome of a patch application request.
type PatchResult struct {
	Status   PatchStatus
	Applied  bool
	Created  []PatchFileResult
	Modified []PatchFileResult
	Deleted  []PatchFileResult
	// Renamed is reserved for future rename support; rename patches currently fail preflight.
	Renamed           []PatchFileResult
	Failed            []PatchFileResult
	Error             string
	FilesRolledBack   int
	RollbackAttempted bool
	RollbackSucceeded bool
	RollbackError     string
}

// PatchFileResult reports one file-level patch outcome.
type PatchFileResult struct {
	Operation     PatchOperation
	Path          string
	OldPath       string
	NewPath       string
	ResolvedPath  string
	BackupCreated bool
	BackupPath    string
	Write         WriteResult
	Delete        DeleteResult
	Error         string
}

type patchFile struct {
	oldPath         string
	newPath         string
	oldDevNull      bool
	newDevNull      bool
	newMode         os.FileMode
	hasNewMode      bool
	sawModeMetadata bool
	hunks           []patchHunk
}

type patchHunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	lines    []patchLine
}

type patchLine struct {
	kind byte
	text string
}

type patchPlan struct {
	files []patchPlanFile
}

type patchPlanFile struct {
	operation    PatchOperation
	path         string
	oldPath      string
	newPath      string
	resolvedPath string
	content      string
	mode         os.FileMode
}

type patchRollbackEntry struct {
	path              string
	oldPath           string
	newPath           string
	workspaceRoot     string
	existed           bool
	content           []byte
	mode              os.FileMode
	scopeID           string
	createdParentDirs []string
	committed         bool
}

type patchCommitOutcome struct {
	Result                  PatchFileResult
	MutationMayHaveOccurred bool
	Committed               bool
}

var hunkHeaderPattern = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

// ApplyPatch applies a unified git-style patch through the managed mutation layer.
func (s *Service) ApplyPatch(ctx context.Context, request PatchRequest) (PatchResult, error) {
	result := PatchResult{
		Status: PatchStatusFailed,
	}

	plan, failure, err := s.planPatch(ctx, request)
	if err != nil {
		return PatchResult{}, err
	}
	if failure.Error != "" {
		result.Error = failure.Error
		if failure.Path != "" || failure.OldPath != "" || failure.NewPath != "" {
			result.Failed = append(result.Failed, failure)
		}
		return result, nil
	}

	rollback, err := s.preparePatchRollback(plan)
	if err != nil {
		return PatchResult{}, err
	}
	for i, file := range plan.files {
		outcome, commitErr := s.commitPatchFile(ctx, request, file)
		if outcome.Committed {
			rollback[i].committed = true
		}
		if commitErr != nil {
			result.Error = commitErr.Error()
			result.Failed = append(result.Failed, PatchFileResult{
				Operation:    file.operation,
				Path:         file.path,
				OldPath:      file.oldPath,
				NewPath:      file.newPath,
				ResolvedPath: file.resolvedPath,
				Error:        commitErr.Error(),
			})
			s.rollbackPatch(&result, rollback)
			//nolint:nilerr // Patch commit errors are surfaced as structured PatchResult failures.
			return result, nil
		}
		fileResult := outcome.Result
		if fileResult.Error != "" {
			result.Error = fileResult.Error
			result.Failed = append(result.Failed, fileResult)
			s.rollbackPatch(&result, rollback)
			return result, nil
		}
		result.Applied = true
		switch file.operation {
		case PatchOperationCreate:
			result.Created = append(result.Created, fileResult)
		case PatchOperationModify:
			result.Modified = append(result.Modified, fileResult)
		case PatchOperationDelete:
			result.Deleted = append(result.Deleted, fileResult)
		case PatchOperationRename:
			result.Renamed = append(result.Renamed, fileResult)
		}
	}

	result.Status = PatchStatusApplied
	return result, nil
}

func (s *Service) preparePatchRollback(plan patchPlan) ([]patchRollbackEntry, error) {
	entries := make([]patchRollbackEntry, 0, len(plan.files))
	for _, file := range plan.files {
		entry := patchRollbackEntry{
			path:          file.resolvedPath,
			oldPath:       file.resolvedPath,
			newPath:       file.resolvedPath,
			workspaceRoot: s.realWorkspaceRoot,
			scopeID:       s.tokens.ScopeID(),
		}
		content, info, err := readManagedExistingFileSnapshot(file.resolvedPath, s.realWorkspaceRoot)
		switch {
		case err == nil:
			entry.existed = true
			entry.content = content
			entry.mode = info.Mode().Perm()
		case errors.Is(err, os.ErrNotExist):
			entry.existed = false
			if file.operation == PatchOperationCreate {
				dirs, err := missingPatchParentDirs(file.resolvedPath, s.realWorkspaceRoot)
				if err != nil {
					return nil, err
				}
				entry.createdParentDirs = dirs
			}
		default:
			return nil, fmt.Errorf("prepare patch rollback snapshot %q: %w", file.resolvedPath, err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *Service) rollbackPatch(result *PatchResult, entries []patchRollbackEntry) {
	if !hasCommittedPatch(entries) {
		return
	}
	result.RollbackAttempted = true
	rolledBack := 0
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if !entry.committed {
			continue
		}
		if err := s.rollbackPatchEntry(entry); err != nil {
			result.RollbackSucceeded = false
			result.RollbackError = err.Error()
			result.Error = result.Error + " (rollback failed: " + err.Error() + ")"
			return
		}
		rolledBack++
	}
	result.RollbackSucceeded = true
	result.Applied = false
	result.FilesRolledBack = rolledBack
	result.Created = nil
	result.Modified = nil
	result.Deleted = nil
	result.Renamed = nil
	if result.Error == "" {
		result.Error = "prior patch changes were rolled back"
	} else {
		result.Error += " (prior patch changes were rolled back)"
	}
}

func hasCommittedPatch(entries []patchRollbackEntry) bool {
	for _, entry := range entries {
		if entry.committed {
			return true
		}
	}
	return false
}

func (s *Service) rollbackPatchEntry(entry patchRollbackEntry) error {
	if s.patchRollbackHook != nil {
		if err := s.patchRollbackHook(entry); err != nil {
			return err
		}
	}
	restorePath := entry.oldPath
	if restorePath == "" {
		restorePath = entry.path
	}
	createdPath := entry.newPath
	if createdPath == "" {
		createdPath = entry.path
	}
	if createdPath != "" && restorePath != "" && createdPath != restorePath {
		if err := deleteManagedExistingFile(createdPath, entry.workspaceRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove renamed file %q: %w", createdPath, err)
		}
		s.tokens.forget(createdPath)
		if err := removeCreatedParentDirs(entry.createdParentDirs, entry.workspaceRoot); err != nil {
			return err
		}
	}
	if entry.existed {
		if err := writeWorkspaceFileAtomicMode(restorePath, entry.content, entry.mode, false, entry.workspaceRoot); err != nil {
			return fmt.Errorf("restore %q: %w", restorePath, err)
		}
		token, err := tokenForFile(entry.scopeID, restorePath, time.Now())
		if err != nil {
			return fmt.Errorf("restore token %q: %w", restorePath, err)
		}
		s.tokens.record(token)
		return nil
	}
	if err := deleteManagedExistingFile(createdPath, entry.workspaceRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove created file %q: %w", createdPath, err)
	}
	s.tokens.forget(createdPath)
	if err := removeCreatedParentDirs(entry.createdParentDirs, entry.workspaceRoot); err != nil {
		return err
	}
	return nil
}

func missingPatchParentDirs(path, root string) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	root = filepath.Clean(root)
	dir := filepath.Dir(filepath.Clean(path))
	var missing []string
	for {
		dir = filepath.Clean(dir)
		if dir == root || dir == "." || dir == string(filepath.Separator) {
			break
		}
		inside, err := workspace.IsInside(root, dir)
		if err != nil {
			return nil, err
		}
		if !inside {
			return nil, fmt.Errorf("%w: patch parent %q escaped trusted root", workspace.ErrPathOutsideWorkspace, dir)
		}
		info, err := os.Lstat(dir)
		switch {
		case err == nil:
			if !info.IsDir() {
				return nil, fmt.Errorf("patch parent %q is not a directory", dir)
			}
			dir = root
		case errors.Is(err, os.ErrNotExist):
			missing = append(missing, dir)
			dir = filepath.Dir(dir)
			continue
		default:
			return nil, fmt.Errorf("stat patch parent %q: %w", dir, err)
		}
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing, nil
}

func removeCreatedParentDirs(dirs []string, root string) error {
	root = filepath.Clean(root)
	for i := len(dirs) - 1; i >= 0; i-- {
		dir := dirs[i]
		dir = filepath.Clean(dir)
		if dir == root || dir == "." || dir == string(filepath.Separator) {
			continue
		}
		inside, err := workspace.IsInside(root, dir)
		if err != nil {
			return err
		}
		if !inside {
			return fmt.Errorf("%w: rollback parent %q escaped trusted root", workspace.ErrPathOutsideWorkspace, dir)
		}
		info, err := os.Lstat(dir)
		switch {
		case err == nil:
			if !info.IsDir() {
				return fmt.Errorf("cleanup rollback parent %q: not a directory", dir)
			}
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return fmt.Errorf("stat rollback parent %q: %w", dir, err)
		}
		err = os.Remove(dir)
		switch {
		case err == nil:
			continue
		case errors.Is(err, os.ErrNotExist):
			continue
		case rollbackParentNotEmpty(err):
			return nil
		default:
			return fmt.Errorf("remove rollback parent %q: %w", dir, err)
		}
	}
	return nil
}

func removeEmptyParents(dir, root string) error {
	return removeCreatedParentDirs([]string{dir}, root)
}

// planPatch parses a unified patch and prepares a comprehensive plan for applying all of its file changes.
func (s *Service) planPatch(ctx context.Context, request PatchRequest) (patchPlan, PatchFileResult, error) {
	parsed, failure := parseUnifiedPatch(request.Patch)
	if failure.Error != "" {
		return patchPlan{}, failure, nil
	}

	plan := patchPlan{
		files: make([]patchPlanFile, 0, len(parsed)),
	}
	seen := make(map[string]string)

	for _, file := range parsed {
		planned, failure, err := s.planPatchFile(ctx, request, file)
		if err != nil {
			return patchPlan{}, PatchFileResult{}, err
		}
		if failure.Error != "" {
			return patchPlan{}, failure, nil
		}
		if original, ok := seen[planned.resolvedPath]; ok {
			return patchPlan{}, PatchFileResult{
				Operation: planned.operation,
				Path:      planned.path,
				OldPath:   planned.oldPath,
				NewPath:   planned.newPath,
				Error:     "patch contains multiple file entries for " + original,
			}, nil
		}
		seen[planned.resolvedPath] = planned.path
		plan.files = append(plan.files, planned)
	}

	return plan, PatchFileResult{}, nil
}

func (s *Service) planPatchFile(ctx context.Context, request PatchRequest, file patchFile) (patchPlanFile, PatchFileResult, error) {
	operation, failure := classifyPatchFileOperation(file)
	if failure.Error != "" {
		return patchPlanFile{}, failure, nil
	}

	switch operation {
	case PatchOperationCreate:
		return s.planPatchCreate(ctx, request, file)
	case PatchOperationModify:
		return s.planPatchModify(ctx, request, file)
	case PatchOperationDelete:
		return s.planPatchDelete(ctx, request, file)
	default:
		return patchPlanFile{}, PatchFileResult{
			Operation: operation,
			OldPath:   file.oldPath,
			NewPath:   file.newPath,
			Error:     "unsupported patch operation",
		}, nil
	}
}

func (s *Service) planPatchCreate(ctx context.Context, request PatchRequest, file patchFile) (patchPlanFile, PatchFileResult, error) {
	resolved, err := s.resolveForCreate("", file.newPath)
	if err != nil {
		return patchPlanFile{}, patchFailure(PatchOperationCreate, "", file.newPath, err.Error()), nil
	}

	if stat, err := os.Stat(resolved); err == nil {
		if stat.IsDir() {
			return patchPlanFile{}, patchFailure(PatchOperationCreate, "", file.newPath, "target path is a directory"), nil
		}
		return patchPlanFile{}, patchFailure(PatchOperationCreate, "", file.newPath, "target file already exists"), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return patchPlanFile{}, PatchFileResult{}, fmt.Errorf("stat patch create target: %w", err)
	}

	content, err := applyPatchHunks("", file.hunks)
	if err != nil {
		return patchPlanFile{}, patchFailure(PatchOperationCreate, "", file.newPath, err.Error()), nil
	}
	if reason := scanSecretContent(content); reason != "" {
		return patchPlanFile{}, patchFailure(PatchOperationCreate, "", file.newPath, reason), nil
	}
	if failure, err := s.preflightPatchPolicy(ctx, PatchOperationCreate, file.newPath, resolved, policy.FileAccessCreate, request.ApprovalRequired); err != nil || failure.Error != "" {
		return patchPlanFile{}, failure, err
	}

	return patchPlanFile{
		operation:    PatchOperationCreate,
		path:         file.newPath,
		newPath:      file.newPath,
		resolvedPath: resolved,
		content:      content,
		mode:         file.newMode,
	}, PatchFileResult{}, nil
}

func (s *Service) planPatchModify(ctx context.Context, request PatchRequest, file patchFile) (patchPlanFile, PatchFileResult, error) {
	read, failure, err := s.readPatchTarget(ctx, PatchOperationModify, file.oldPath, request.ApprovalRequired)
	if err != nil || failure.Error != "" {
		return patchPlanFile{}, failure, err
	}

	content, err := applyPatchHunks(read.Content, file.hunks)
	if err != nil {
		return patchPlanFile{}, patchFailure(PatchOperationModify, file.oldPath, file.newPath, err.Error()), nil
	}
	if reason := scanSecretContent(content); reason != "" {
		return patchPlanFile{}, patchFailure(PatchOperationModify, file.oldPath, file.newPath, reason), nil
	}
	if failure, err := s.preflightPatchPolicy(ctx, PatchOperationModify, file.newPath, read.ResolvedPath, policy.FileAccessWrite, request.ApprovalRequired); err != nil || failure.Error != "" {
		return patchPlanFile{}, failure, err
	}

	return patchPlanFile{
		operation:    PatchOperationModify,
		path:         file.newPath,
		oldPath:      file.oldPath,
		newPath:      file.newPath,
		resolvedPath: read.ResolvedPath,
		content:      content,
		mode:         file.newMode,
	}, PatchFileResult{}, nil
}

func (s *Service) planPatchDelete(ctx context.Context, request PatchRequest, file patchFile) (patchPlanFile, PatchFileResult, error) {
	read, failure, err := s.readPatchTarget(ctx, PatchOperationDelete, file.oldPath, request.ApprovalRequired)
	if err != nil || failure.Error != "" {
		return patchPlanFile{}, failure, err
	}

	content, err := applyPatchHunks(read.Content, file.hunks)
	if err != nil {
		return patchPlanFile{}, patchFailure(PatchOperationDelete, file.oldPath, "", err.Error()), nil
	}
	if content != "" {
		return patchPlanFile{}, patchFailure(PatchOperationDelete, file.oldPath, "", "delete patch does not remove all file content"), nil
	}
	if failure, err := s.preflightPatchPolicy(ctx, PatchOperationDelete, file.oldPath, read.ResolvedPath, policy.FileAccessWrite, request.ApprovalRequired); err != nil || failure.Error != "" {
		return patchPlanFile{}, failure, err
	}

	return patchPlanFile{
		operation:    PatchOperationDelete,
		path:         file.oldPath,
		oldPath:      file.oldPath,
		resolvedPath: read.ResolvedPath,
	}, PatchFileResult{}, nil
}

func (s *Service) readPatchTarget(ctx context.Context, operation PatchOperation, path string, approvalRequired bool) (ReadResult, PatchFileResult, error) {
	resolved, err := s.resolveExisting("", path)
	if err != nil {
		return ReadResult{}, PatchFileResult{
			Operation: operation,
			Path:      path,
			OldPath:   path,
			Error:     "managed read failed during patch preflight: " + err.Error(),
		}, nil
	}

	validation, err := s.tokens.ValidateCurrent(resolved)
	if err != nil {
		return ReadResult{}, PatchFileResult{}, err
	}
	if !validation.Found {
		return ReadResult{}, PatchFileResult{
			Operation:    operation,
			Path:         path,
			OldPath:      path,
			ResolvedPath: resolved,
			Error:        "missing read token: " + validation.Reason,
		}, nil
	}
	if !validation.Matches {
		return ReadResult{}, PatchFileResult{
			Operation:    operation,
			Path:         path,
			OldPath:      path,
			ResolvedPath: resolved,
			Error:        "stale read token: " + validation.Reason,
		}, nil
	}

	read, err := s.readSnapshot(ctx, ReadRequest{
		Path:             path,
		ApprovalRequired: approvalRequired,
	})
	if err != nil {
		return ReadResult{}, PatchFileResult{}, err
	}

	if read.Status != ReadStatusOK {
		return ReadResult{}, PatchFileResult{
			Operation: operation,
			Path:      path,
			OldPath:   path,
			Error:     "managed read failed during patch preflight: " + read.Error,
		}, nil
	}

	return read, PatchFileResult{}, nil
}

func (s *Service) preflightPatchPolicy(ctx context.Context, operation PatchOperation, path, resolvedPath string, mode policy.FileAccessMode, approvalRequired bool) (PatchFileResult, error) {
	policyResult, err := s.policy.EvaluateFile(ctx, runtimepolicy.FileRequest{
		Path:             resolvedPath,
		Mode:             mode,
		ApprovalRequired: approvalRequired,
	})
	if err != nil {
		return PatchFileResult{}, fmt.Errorf("evaluate patch file policy: %w", err)
	}
	if !policyResult.Allowed {
		return PatchFileResult{
			Operation:    operation,
			Path:         path,
			ResolvedPath: resolvedPath,
			Error:        policyResult.Explanation,
		}, nil
	}
	return PatchFileResult{}, nil
}

func (s *Service) commitPatchFile(ctx context.Context, request PatchRequest, file patchPlanFile) (patchCommitOutcome, error) {
	result := PatchFileResult{
		Operation:    file.operation,
		Path:         file.path,
		OldPath:      file.oldPath,
		NewPath:      file.newPath,
		ResolvedPath: file.resolvedPath,
	}
	if s.patchCommitHook != nil {
		if hooked, ok := s.patchCommitHook(file); ok {
			return hooked, nil
		}
	}

	switch file.operation {
	case PatchOperationCreate:
		write, err := s.Write(ctx, WriteRequest{
			Path:             file.path,
			Content:          file.content,
			Operation:        WriteOperationCreate,
			Mode:             file.mode,
			ApprovalRequired: request.ApprovalRequired,
		})
		if err != nil {
			committed := errors.Is(err, errMutationMayHaveOccurred)
			return patchCommitOutcome{Result: result, MutationMayHaveOccurred: committed, Committed: committed}, err
		}
		result.Write = write
		if write.Status != WriteStatusCreated {
			result.Error = write.Error
			if result.Error == "" {
				result.Error = "managed create rejected patch file"
			}
			return patchCommitOutcome{Result: result, MutationMayHaveOccurred: write.MutationMayHaveOccurred, Committed: write.MutationMayHaveOccurred}, nil
		}
		return patchCommitOutcome{Result: result, MutationMayHaveOccurred: true, Committed: true}, nil
	case PatchOperationModify:
		write, err := s.Write(ctx, WriteRequest{
			Path:             file.path,
			Content:          file.content,
			Operation:        WriteOperationOverwrite,
			Mode:             file.mode,
			ApprovalRequired: request.ApprovalRequired,
		})
		if err != nil {
			committed := errors.Is(err, errMutationMayHaveOccurred)
			return patchCommitOutcome{Result: result, MutationMayHaveOccurred: committed, Committed: committed}, err
		}
		result.Write = write
		result.BackupCreated = write.BackupCreated
		result.BackupPath = write.BackupPath
		if write.Status != WriteStatusOverwritten {
			result.Error = write.Error
			if result.Error == "" {
				result.Error = "managed overwrite rejected patch file"
			}
			return patchCommitOutcome{Result: result, MutationMayHaveOccurred: write.MutationMayHaveOccurred, Committed: write.MutationMayHaveOccurred}, nil
		}
		return patchCommitOutcome{Result: result, MutationMayHaveOccurred: true, Committed: true}, nil
	case PatchOperationDelete:
		deleted, err := s.Delete(ctx, DeleteRequest{
			Path:             file.path,
			ApprovalRequired: request.ApprovalRequired,
		})
		if err != nil {
			return patchCommitOutcome{Result: result}, err
		}
		result.Delete = deleted
		result.BackupCreated = deleted.BackupCreated
		result.BackupPath = deleted.BackupPath
		if deleted.Status != DeleteStatusDeleted {
			result.Error = deleted.Error
			if result.Error == "" {
				result.Error = "managed delete rejected patch file"
			}
			return patchCommitOutcome{Result: result, MutationMayHaveOccurred: deleted.MutationMayHaveOccurred, Committed: deleted.MutationMayHaveOccurred}, nil
		}
		return patchCommitOutcome{Result: result, MutationMayHaveOccurred: true, Committed: true}, nil
	}

	return patchCommitOutcome{Result: result}, nil
}

// classifyPatchFileOperation determines the specific file operation (create, modify, delete, rename) represented by a parsed patch.
func classifyPatchFileOperation(file patchFile) (PatchOperation, PatchFileResult) {
	switch {
	case file.oldDevNull && file.newDevNull:
		return "", patchFailure("", file.oldPath, file.newPath, "patch cannot use /dev/null for both old and new paths")
	case file.oldDevNull:
		return PatchOperationCreate, PatchFileResult{}
	case file.newDevNull:
		return PatchOperationDelete, PatchFileResult{}
	case file.oldPath != file.newPath:
		return PatchOperationRename, PatchFileResult{
			Operation: PatchOperationRename,
			OldPath:   file.oldPath,
			NewPath:   file.newPath,
			Error:     "rename patches are not supported yet",
		}
	default:
		return PatchOperationModify, PatchFileResult{}
	}
}

// parseUnifiedPatch parses a raw unified git diff string into a structured representation of file and hunk changes.
func parseUnifiedPatch(patch string) ([]patchFile, PatchFileResult) {
	if strings.TrimSpace(patch) == "" {
		return nil, PatchFileResult{Error: "patch is empty"}
	}
	if strings.HasPrefix(strings.TrimSpace(patch), "*** Begin Patch") {
		return nil, PatchFileResult{Error: "unsupported patch envelope: expected unified git diff"}
	}

	lines := splitPatchTextLines(patch)
	files := make([]patchFile, 0)

	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}
		if !strings.HasPrefix(lines[i], "diff --git ") {
			return nil, PatchFileResult{Error: "unsupported patch format: expected diff --git header"}
		}

		file, next, failure := parsePatchFile(lines, i)
		if failure.Error != "" {
			return nil, failure
		}
		files = append(files, file)
		i = next
	}

	if len(files) == 0 {
		return nil, PatchFileResult{Error: "patch does not contain any file changes"}
	}

	return files, PatchFileResult{}
}

func parsePatchFile(lines []string, start int) (patchFile, int, PatchFileResult) {
	oldRaw, newRaw, err := parseDiffGitHeader(lines[start])
	if err != nil {
		return patchFile{}, start, PatchFileResult{Error: err.Error()}
	}
	oldPath, oldDevNull, err := parsePatchPath(oldRaw)
	if err != nil {
		return patchFile{}, start, PatchFileResult{Error: err.Error()}
	}
	newPath, newDevNull, err := parsePatchPath(newRaw)
	if err != nil {
		return patchFile{}, start, PatchFileResult{Error: err.Error()}
	}

	file := patchFile{
		oldPath:    oldPath,
		newPath:    newPath,
		oldDevNull: oldDevNull,
		newDevNull: newDevNull,
	}
	sawOldMarker := false
	sawNewMarker := false

	for i := start + 1; i < len(lines); {
		line := trimLineEnding(lines[i])
		switch {
		case strings.HasPrefix(lines[i], "diff --git "):
			if failure := validateParsedPatchFile(file, sawOldMarker, sawNewMarker); failure.Error != "" {
				return patchFile{}, i, failure
			}
			return file, i, PatchFileResult{}
		case strings.HasPrefix(line, "rename from ") || strings.HasPrefix(line, "rename to "):
			return patchFile{}, i, PatchFileResult{
				Operation: PatchOperationRename,
				Error:     "rename patches are not supported yet",
			}
		case strings.HasPrefix(line, "copy from ") || strings.HasPrefix(line, "copy to "):
			return patchFile{}, i, PatchFileResult{Error: "copy patches are not supported"}
		case line == "GIT binary patch" || strings.HasPrefix(line, "Binary files "):
			return patchFile{}, i, PatchFileResult{Error: "binary patches are not supported"}
		case isPatchModeMetadata(line):
			if failure := parsePatchModeMetadata(line, &file); failure.Error != "" {
				return patchFile{}, i, failure
			}
			i++
		case strings.HasPrefix(line, "--- "):
			path, devNull, err := parsePatchPath(pathMarkerValue(line[4:]))
			if err != nil {
				return patchFile{}, i, PatchFileResult{Error: err.Error()}
			}
			file.oldPath = path
			file.oldDevNull = devNull
			sawOldMarker = true
			i++
		case strings.HasPrefix(line, "+++ "):
			path, devNull, err := parsePatchPath(pathMarkerValue(line[4:]))
			if err != nil {
				return patchFile{}, i, PatchFileResult{Error: err.Error()}
			}
			file.newPath = path
			file.newDevNull = devNull
			sawNewMarker = true
			i++
		case strings.HasPrefix(line, "@@ "):
			hunk, next, failure := parsePatchHunk(lines, i)
			if failure.Error != "" {
				return patchFile{}, next, failure
			}
			file.hunks = append(file.hunks, hunk)
			i = next
		case isKnownPatchMetadata(line):
			i++
		case strings.TrimSpace(line) == "":
			i++
		default:
			return patchFile{}, i, PatchFileResult{Error: "unsupported patch line: " + line}
		}
	}

	if failure := validateParsedPatchFile(file, sawOldMarker, sawNewMarker); failure.Error != "" {
		return patchFile{}, len(lines), failure
	}
	return file, len(lines), PatchFileResult{}
}

func parsePatchHunk(lines []string, start int) (patchHunk, int, PatchFileResult) {
	header := trimLineEnding(lines[start])
	matches := hunkHeaderPattern.FindStringSubmatch(header)
	if matches == nil {
		return patchHunk{}, start, PatchFileResult{Error: "malformed patch hunk header"}
	}

	oldStart, oldCount, err := parsePatchRange(matches[1], matches[2])
	if err != nil {
		return patchHunk{}, start, PatchFileResult{Error: err.Error()}
	}
	newStart, newCount, err := parsePatchRange(matches[3], matches[4])
	if err != nil {
		return patchHunk{}, start, PatchFileResult{Error: err.Error()}
	}

	hunk := patchHunk{
		oldStart: oldStart,
		oldCount: oldCount,
		newStart: newStart,
		newCount: newCount,
	}

	i := start + 1
	for i < len(lines) {
		line := lines[i]
		trimmed := trimLineEnding(line)
		if strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(trimmed, "@@ ") {
			break
		}
		if trimmed == `\ No newline at end of file` {
			if len(hunk.lines) > 0 {
				last := len(hunk.lines) - 1
				hunk.lines[last].text = trimLineEnding(hunk.lines[last].text)
			}
			i++
			continue
		}
		if len(line) == 0 {
			return patchHunk{}, i, PatchFileResult{Error: "malformed empty hunk line"}
		}

		kind := line[0]
		if kind != ' ' && kind != '-' && kind != '+' {
			return patchHunk{}, i, PatchFileResult{Error: "malformed hunk line"}
		}
		hunk.lines = append(hunk.lines, patchLine{
			kind: kind,
			text: line[1:],
		})
		i++
	}

	if failure := validatePatchHunkCounts(hunk); failure.Error != "" {
		return patchHunk{}, i, failure
	}

	return hunk, i, PatchFileResult{}
}

func validateParsedPatchFile(file patchFile, sawOldMarker, sawNewMarker bool) PatchFileResult {
	if !sawOldMarker || !sawNewMarker {
		return PatchFileResult{Error: "patch file is missing ---/+++ path markers"}
	}
	if len(file.hunks) == 0 {
		if file.sawModeMetadata {
			return PatchFileResult{Error: "mode-only patches are not supported"}
		}
		return PatchFileResult{Error: "patch file does not contain any hunks"}
	}
	return PatchFileResult{}
}

func validatePatchHunkCounts(hunk patchHunk) PatchFileResult {
	oldLines := 0
	newLines := 0
	for _, line := range hunk.lines {
		switch line.kind {
		case ' ':
			oldLines++
			newLines++
		case '-':
			oldLines++
		case '+':
			newLines++
		}
	}

	if oldLines != hunk.oldCount {
		return PatchFileResult{Error: "patch hunk old-line count does not match header"}
	}
	if newLines != hunk.newCount {
		return PatchFileResult{Error: "patch hunk new-line count does not match header"}
	}
	return PatchFileResult{}
}

// applyPatchHunks applies a sequence of parsed patch hunks to the provided file content.
func applyPatchHunks(content string, hunks []patchHunk) (string, error) {
	lines := splitLines(content)
	out := make([]string, 0, len(lines))
	pos := 0

	for _, hunk := range hunks {
		start := patchOldStartIndex(hunk)
		if start < pos || start > len(lines) {
			return "", errors.New("patch hunk applies outside file content")
		}

		out = append(out, lines[pos:start]...)
		pos = start

		for _, line := range hunk.lines {
			switch line.kind {
			case ' ':
				if pos >= len(lines) || lines[pos] != line.text {
					return "", errors.New("patch context does not match file content")
				}
				out = append(out, lines[pos])
				pos++
			case '-':
				if pos >= len(lines) || lines[pos] != line.text {
					return "", errors.New("patch removal does not match file content")
				}
				pos++
			case '+':
				out = append(out, line.text)
			}
		}
	}

	out = append(out, lines[pos:]...)
	var builder strings.Builder
	for _, line := range out {
		builder.WriteString(line)
	}
	return builder.String(), nil
}

func patchOldStartIndex(hunk patchHunk) int {
	if hunk.oldCount == 0 {
		if hunk.oldStart == 0 {
			return 0
		}
		return hunk.oldStart
	}
	return hunk.oldStart - 1
}

func parsePatchRange(start, count string) (int, int, error) {
	parsedStart, err := strconv.Atoi(start)
	if err != nil {
		return 0, 0, fmt.Errorf("parse patch hunk start: %w", err)
	}
	if count == "" {
		return parsedStart, 1, nil
	}

	parsedCount, err := strconv.Atoi(count)
	if err != nil {
		return 0, 0, fmt.Errorf("parse patch hunk count: %w", err)
	}
	return parsedStart, parsedCount, nil
}

func parsePatchPath(raw string) (string, bool, error) {
	value := pathMarkerValue(raw)
	if value == "/dev/null" {
		return "", true, nil
	}
	if value == "" {
		return "", false, errors.New("patch path is required")
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
		return "", false, errors.New("patch path is required")
	}
	return filepath.Clean(value), false, nil
}

func pathMarkerValue(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, `"`) {
		token, _, err := scanPatchToken(value)
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

func parseDiffGitHeader(line string) (string, string, error) {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "diff --git "))
	oldPath, remaining, err := scanPatchToken(rest)
	if err != nil {
		return "", "", fmt.Errorf("malformed diff --git header: %w", err)
	}
	newPath, remaining, err := scanPatchToken(remaining)
	if err != nil {
		return "", "", fmt.Errorf("malformed diff --git header: %w", err)
	}
	if strings.TrimSpace(remaining) != "" {
		return "", "", errors.New("malformed diff --git header")
	}
	return oldPath, newPath, nil
}

func scanPatchToken(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", errors.New("missing path")
	}
	if !strings.HasPrefix(value, `"`) {
		if index := strings.IndexAny(value, "\t "); index >= 0 {
			return value[:index], value[index:], nil
		}
		return value, "", nil
	}
	escaped := false
	for i := 1; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case value[i] == '\\':
			escaped = true
		case value[i] == '"':
			return value[:i+1], value[i+1:], nil
		}
	}
	return "", "", errors.New("unterminated quoted path")
}

func isKnownPatchMetadata(line string) bool {
	switch {
	case strings.HasPrefix(line, "index "):
		return true
	case strings.HasPrefix(line, "similarity index "):
		return true
	case strings.HasPrefix(line, "dissimilarity index "):
		return true
	default:
		return false
	}
}

func isPatchModeMetadata(line string) bool {
	return strings.HasPrefix(line, "new file mode ") ||
		strings.HasPrefix(line, "deleted file mode ") ||
		strings.HasPrefix(line, "old mode ") ||
		strings.HasPrefix(line, "new mode ")
}

func parsePatchModeMetadata(line string, file *patchFile) PatchFileResult {
	file.sawModeMetadata = true
	switch {
	case strings.HasPrefix(line, "new file mode "):
		mode, err := parseGitFileMode(strings.TrimSpace(strings.TrimPrefix(line, "new file mode ")))
		if err != nil {
			return PatchFileResult{Error: err.Error()}
		}
		file.newMode = mode
		file.hasNewMode = true
	case strings.HasPrefix(line, "new mode "):
		mode, err := parseGitFileMode(strings.TrimSpace(strings.TrimPrefix(line, "new mode ")))
		if err != nil {
			return PatchFileResult{Error: err.Error()}
		}
		file.newMode = mode
		file.hasNewMode = true
	case strings.HasPrefix(line, "old mode "):
		if _, err := parseGitFileMode(strings.TrimSpace(strings.TrimPrefix(line, "old mode "))); err != nil {
			return PatchFileResult{Error: err.Error()}
		}
	case strings.HasPrefix(line, "deleted file mode "):
		if _, err := parseGitFileMode(strings.TrimSpace(strings.TrimPrefix(line, "deleted file mode "))); err != nil {
			return PatchFileResult{Error: err.Error()}
		}
	}
	return PatchFileResult{}
}

func parseGitFileMode(value string) (os.FileMode, error) {
	switch value {
	case "100644":
		return 0o644, nil
	case "100755":
		return 0o755, nil
	default:
		return 0, fmt.Errorf("unsupported git file mode %q: only regular file modes 100644 and 100755 are supported", value)
	}
}

func splitPatchTextLines(content string) []string {
	if content == "" {
		return nil
	}

	var lines []string
	start := 0
	for i, r := range content {
		if r == '\n' {
			lines = append(lines, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

func trimLineEnding(line string) string {
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r")
}

func patchFailure(operation PatchOperation, oldPath, newPath, reason string) PatchFileResult {
	path := newPath
	if path == "" {
		path = oldPath
	}
	return PatchFileResult{
		Operation: operation,
		Path:      path,
		OldPath:   oldPath,
		NewPath:   newPath,
		Error:     reason,
	}
}
