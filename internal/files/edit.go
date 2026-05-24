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

	"github.com/agnivade/levenshtein"
)

// EditStatus identifies the deterministic outcome of a targeted edit operation.
type EditStatus string

const (
	EditStatusChanged          EditStatus = "changed"
	EditStatusUnchanged        EditStatus = "unchanged"
	EditStatusUnsafe           EditStatus = "unsafe_path"
	EditStatusNotFound         EditStatus = "not_found"
	EditStatusDirectory        EditStatus = "directory"
	EditStatusMissingReadToken EditStatus = "missing_read_token"
	EditStatusStaleReadToken   EditStatus = "stale_read_token"
	EditStatusEmptyOldString   EditStatus = "empty_old_string"
	EditStatusNoMatch          EditStatus = "no_match"
	EditStatusAmbiguousMatch   EditStatus = "ambiguous_match"
	EditStatusMutationRejected EditStatus = "mutation_rejected"
	EditStatusInvalidStrategy  EditStatus = "invalid_match_strategy"
)

// MatchStrategy controls how edit old-string matching is performed.
type MatchStrategy string

const (
	MatchStrategyExact MatchStrategy = "exact"
	MatchClose         MatchStrategy = "close_match"
)

// EditRequest describes a targeted exact-string replacement in an existing file.
type EditRequest struct {
	Path              string
	CurrentWorkingDir string
	Old               string
	New               string
	ReplaceAll        bool
	MatchStrategy     MatchStrategy
	ApprovalRequired  bool
}

// EditResult reports the outcome of a targeted edit.
type EditResult struct {
	Status        EditStatus
	Path          string
	ResolvedPath  string
	ReplaceAll    bool
	Replacements  int
	Changed       bool
	UsedFuzzy     bool
	MatchStrategy MatchStrategy
	Write         WriteResult
	Error         string
	Summary       string
}

// MultiEditStatus identifies the aggregate outcome of a multi-edit request.
type MultiEditStatus string

const (
	MultiEditStatusApplied   MultiEditStatus = "applied"
	MultiEditStatusPartial   MultiEditStatus = "partial"
	MultiEditStatusFailed    MultiEditStatus = "failed"
	MultiEditStatusNoChanges MultiEditStatus = "no_changes"
)

// MultiEditRequest describes a deterministic list of exact-string edits.
type MultiEditRequest struct {
	Edits []EditRequest
}

// MultiEditResult reports per-file and aggregate edit outcomes.
type MultiEditResult struct {
	Status            MultiEditStatus
	Files             []MultiEditFileResult
	TotalFiles        int
	FilesChanged      int
	FilesRolledBack   int
	TotalReplacements int
	RollbackAttempted bool
	RollbackSucceeded bool
	RollbackError     string
	Error             string
}

// MultiEditFileResult reports the outcome for one canonical file group.
type MultiEditFileResult struct {
	Status       EditStatus
	Path         string
	ResolvedPath string
	Edits        []EditResult
	Replacements int
	Changed      bool
	Write        WriteResult
	Error        string
	Summary      string
}

type editFile struct {
	resolvedPath string
	content      string
}

type editGroup struct {
	path              string
	currentWorkingDir string
	resolvedPath      string
	approvalRequired  bool
	edits             []EditRequest
}

type multiEditRollbackEntry struct {
	path      string
	content   []byte
	mode      os.FileMode
	scopeID   string
	committed bool
}

// Edit applies one exact-string replacement and commits through the managed overwrite layer.
func (s *Service) Edit(ctx context.Context, request EditRequest) (EditResult, error) {
	result := EditResult{
		Path:          request.Path,
		ReplaceAll:    request.ReplaceAll,
		MatchStrategy: normalizedMatchStrategy(request.MatchStrategy),
	}
	if !isValidMatchStrategy(result.MatchStrategy) {
		result.Status = EditStatusInvalidStrategy
		result.Error = "invalid match strategy"
		result.Summary = result.Error
		return result, nil
	}

	file, status, reason, err := s.prepareEditFile(request.Path, request.CurrentWorkingDir)
	if err != nil {
		return EditResult{}, err
	}
	result.ResolvedPath = file.resolvedPath
	if status != "" {
		result.Status = status
		result.Error = reason
		result.Summary = reason
		return result, nil
	}

	updated, editResult := applyExactEdit(file.content, request, 0)
	result.Replacements = editResult.Replacements
	result.Changed = editResult.Changed
	result.UsedFuzzy = editResult.UsedFuzzy
	if editResult.Status != EditStatusChanged && editResult.Status != EditStatusUnchanged {
		result.Status = editResult.Status
		result.Error = editResult.Error
		result.Summary = editResult.Summary
		return result, nil
	}
	if !editResult.Changed {
		result.Status = EditStatusUnchanged
		result.Summary = editSummary(file.resolvedPath, result.Replacements, false, result.UsedFuzzy)
		return result, nil
	}

	writeResult, err := s.Write(ctx, WriteRequest{
		Path:              request.Path,
		CurrentWorkingDir: request.CurrentWorkingDir,
		Content:           updated,
		Operation:         WriteOperationOverwrite,
		ApprovalRequired:  request.ApprovalRequired,
	})
	if err != nil {
		return EditResult{}, err
	}
	result.Write = writeResult
	if writeResult.Status != WriteStatusOverwritten {
		result.Status = EditStatusMutationRejected
		result.Error = writeResult.Error
		result.Summary = writeResult.Error
		return result, nil
	}

	result.Status = EditStatusChanged
	result.Summary = editSummary(file.resolvedPath, result.Replacements, true, result.UsedFuzzy)
	return result, nil
}

// MultiEdit applies edits grouped by canonical file path and rolls back prior
// file changes if any later file group fails.
func (s *Service) MultiEdit(ctx context.Context, request MultiEditRequest) (MultiEditResult, error) {
	groups, preflightResults, err := s.groupEditRequests(request.Edits)
	if err != nil {
		return MultiEditResult{}, err
	}

	result := MultiEditResult{
		Files:      append([]MultiEditFileResult(nil), preflightResults...),
		TotalFiles: len(preflightResults) + len(groups),
	}
	if len(preflightResults) > 0 {
		result.Status = summarizeMultiEditStatus(result.Files, result.FilesChanged)
		result.Error = "one or more file groups failed"
		return result, nil
	}

	rollbackEntries := make([]multiEditRollbackEntry, 0, len(groups))
	for _, group := range groups {
		rollbackEntry, err := s.captureMultiEditRollback(group.resolvedPath)
		if err != nil {
			return MultiEditResult{}, err
		}
		rollbackEntries = append(rollbackEntries, rollbackEntry)
	}

	for i, group := range groups {
		fileResult, err := s.applyEditGroup(ctx, group)
		if err != nil {
			fileResult = MultiEditFileResult{
				Status:       EditStatusMutationRejected,
				Path:         group.path,
				ResolvedPath: group.resolvedPath,
				Error:        err.Error(),
				Summary:      err.Error(),
			}
		}
		result.Files = append(result.Files, fileResult)
		if fileResult.Changed || fileResult.Write.MutationMayHaveOccurred {
			rollbackEntries[i].committed = true
			result.FilesChanged++
		}
		result.TotalReplacements += fileResult.Replacements
		if multiEditFileFailed(fileResult) {
			if hasCommittedMultiEdit(rollbackEntries) {
				s.rollbackMultiEdit(rollbackEntries, &result)
			}
			if result.RollbackSucceeded {
				result.FilesRolledBack = result.FilesChanged
				result.FilesChanged = 0
				result.Status = MultiEditStatusFailed
				result.Error = "one or more file groups failed; prior changes were rolled back"
			} else {
				result.Status = summarizeMultiEditStatus(result.Files, result.FilesChanged)
				result.Error = "one or more file groups failed"
				if result.RollbackError != "" {
					result.Error += "; rollback failed: " + result.RollbackError
				}
			}
			return result, nil
		}
	}

	result.Status = summarizeMultiEditStatus(result.Files, result.FilesChanged)
	if result.Status == MultiEditStatusFailed {
		result.Error = "no files were changed"
	}
	if result.Status == MultiEditStatusPartial {
		result.Error = "one or more file groups failed"
	}
	return result, nil
}

func (s *Service) captureMultiEditRollback(path string) (multiEditRollbackEntry, error) {
	content, stat, err := readManagedExistingFileSnapshot(path, s.realWorkspaceRoot)
	if err != nil {
		return multiEditRollbackEntry{}, fmt.Errorf("snapshot multi-edit rollback target: %w", err)
	}
	return multiEditRollbackEntry{
		path:    path,
		content: append([]byte(nil), content...),
		mode:    stat.Mode().Perm(),
		scopeID: s.tokens.ScopeID(),
	}, nil
}

func multiEditFileFailed(result MultiEditFileResult) bool {
	return result.Status != EditStatusChanged && result.Status != EditStatusUnchanged
}

func hasCommittedMultiEdit(entries []multiEditRollbackEntry) bool {
	for _, entry := range entries {
		if entry.committed {
			return true
		}
	}
	return false
}

func (s *Service) rollbackMultiEdit(entries []multiEditRollbackEntry, result *MultiEditResult) {
	result.RollbackAttempted = true
	var rollbackErr error
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if !entry.committed {
			continue
		}
		if err := s.rollbackMultiEditEntry(entry); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	if rollbackErr != nil {
		result.RollbackError = rollbackErr.Error()
		return
	}
	result.RollbackSucceeded = true
}

func (s *Service) rollbackMultiEditEntry(entry multiEditRollbackEntry) error {
	if s.multiEditRollbackHook != nil {
		if err := s.multiEditRollbackHook(entry); err != nil {
			return err
		}
	}
	if err := writeWorkspaceFileAtomicMode(entry.path, entry.content, entry.mode, false, s.realWorkspaceRoot); err != nil {
		return fmt.Errorf("restore %q: %w", entry.path, err)
	}
	token, err := tokenForFile(entry.scopeID, entry.path, time.Now())
	if err != nil {
		return fmt.Errorf("restore token %q: %w", entry.path, err)
	}
	s.tokens.record(token)
	return nil
}

// groupEditRequests groups a flat list of edit requests by their resolved target file paths.
func (s *Service) groupEditRequests(edits []EditRequest) ([]editGroup, []MultiEditFileResult, error) {
	groups := make([]editGroup, 0)
	groupIndex := make(map[string]int)
	failures := make([]MultiEditFileResult, 0)

	for _, edit := range edits {
		file, status, reason, err := s.prepareEditFile(edit.Path, edit.CurrentWorkingDir)
		if err != nil {
			return nil, nil, err
		}
		if status != "" {
			failures = append(failures, MultiEditFileResult{
				Status:       status,
				Path:         edit.Path,
				ResolvedPath: file.resolvedPath,
				Error:        reason,
				Summary:      reason,
			})
			continue
		}

		index, ok := groupIndex[file.resolvedPath]
		if !ok {
			groupIndex[file.resolvedPath] = len(groups)
			groups = append(groups, editGroup{
				path:              edit.Path,
				currentWorkingDir: edit.CurrentWorkingDir,
				resolvedPath:      file.resolvedPath,
				approvalRequired:  edit.ApprovalRequired,
				edits:             []EditRequest{edit},
			})
			continue
		}
		if edit.ApprovalRequired {
			groups[index].approvalRequired = true
		}
		groups[index].edits = append(groups[index].edits, edit)
	}

	return groups, failures, nil
}

// applyEditGroup applies a sequence of edits to a single file sequentially, returning the aggregate result.
func (s *Service) applyEditGroup(ctx context.Context, group editGroup) (MultiEditFileResult, error) {
	file, status, reason, err := s.prepareEditFile(group.path, group.currentWorkingDir)
	if err != nil {
		return MultiEditFileResult{}, err
	}
	result := MultiEditFileResult{
		Path:         group.path,
		ResolvedPath: group.resolvedPath,
	}
	if status != "" {
		result.Status = status
		result.Error = reason
		result.Summary = reason
		return result, nil
	}

	content := file.content
	replacements := 0
	usedFuzzy := false
	for i, edit := range group.edits {
		updated, editResult := applyExactEdit(content, edit, i)
		editResult.Path = edit.Path
		editResult.ResolvedPath = group.resolvedPath
		editResult.ReplaceAll = edit.ReplaceAll
		result.Edits = append(result.Edits, editResult)
		if editResult.UsedFuzzy {
			usedFuzzy = true
		}
		if editResult.Status != EditStatusChanged && editResult.Status != EditStatusUnchanged {
			result.Status = editResult.Status
			result.Error = editResult.Error
			result.Summary = editResult.Summary
			return result, nil
		}
		content = updated
		replacements += editResult.Replacements
	}

	if content == file.content {
		result.Replacements = replacements
		result.Status = EditStatusUnchanged
		result.Summary = editSummary(group.resolvedPath, result.Replacements, false, usedFuzzy)
		return result, nil
	}

	writeResult, err := s.Write(ctx, WriteRequest{
		Path:              group.path,
		CurrentWorkingDir: group.currentWorkingDir,
		Content:           content,
		Operation:         WriteOperationOverwrite,
		ApprovalRequired:  group.approvalRequired,
	})
	if err != nil {
		return MultiEditFileResult{}, err
	}
	result.Write = writeResult
	if writeResult.Status != WriteStatusOverwritten {
		result.Status = EditStatusMutationRejected
		result.Error = writeResult.Error
		result.Summary = writeResult.Error
		return result, nil
	}

	result.Status = EditStatusChanged
	result.Changed = true
	result.Replacements = replacements
	result.Summary = editSummary(group.resolvedPath, result.Replacements, true, usedFuzzy)
	return result, nil
}

// prepareEditFile resolves a file path, validates its read token, and loads its content for editing.
func (s *Service) prepareEditFile(path, cwd string) (editFile, EditStatus, string, error) {
	file := editFile{}

	resolved, err := s.resolveExisting(cwd, path)
	if err != nil {
		file.resolvedPath = resolved
		if errors.Is(err, os.ErrNotExist) {
			return file, EditStatusNotFound, "target file does not exist", nil
		}
		return file, EditStatusUnsafe, err.Error(), nil
	}
	file.resolvedPath = resolved

	stat, err := os.Stat(resolved)
	if err != nil {
		return editFile{}, "", "", fmt.Errorf("stat edit target: %w", err)
	}
	if stat.IsDir() {
		return file, EditStatusDirectory, "path is a directory", nil
	}

	validation, err := s.tokens.ValidateCurrent(resolved)
	if err != nil {
		return editFile{}, "", "", fmt.Errorf("validate read token: %w", err)
	}
	if !validation.Found {
		return file, EditStatusMissingReadToken, validation.Reason, nil
	}
	if !validation.Matches {
		return file, EditStatusStaleReadToken, validation.Reason, nil
	}

	data, _, err := readManagedExistingFileSnapshot(resolved, s.realWorkspaceRoot)
	if err != nil {
		return editFile{}, "", "", fmt.Errorf("read edit target: %w", err)
	}
	file.content = string(data)
	return file, "", "", nil
}

// applyExactEdit performs a single literal string replacement, falling back to fuzzy matching if necessary.
func applyExactEdit(content string, request EditRequest, index int) (string, EditResult) {
	result := EditResult{
		Path:          request.Path,
		ReplaceAll:    request.ReplaceAll,
		MatchStrategy: normalizedMatchStrategy(request.MatchStrategy),
	}
	if !isValidMatchStrategy(result.MatchStrategy) {
		result.Status = EditStatusInvalidStrategy
		result.Error = "invalid match strategy"
		result.Summary = result.Error
		return content, result
	}
	if request.Old == "" {
		result.Status = EditStatusEmptyOldString
		result.Error = "old string is required"
		result.Summary = result.Error
		return content, result
	}

	actualOld := request.Old
	usedFuzzy := false
	if strings.Count(content, actualOld) == 0 && result.MatchStrategy != MatchStrategyExact {
		if match := fuzzyMatchWhitespace(content, actualOld); match != "" {
			actualOld = match
			usedFuzzy = true
		} else {
			if strings.Count(content, actualOld) == 0 && result.MatchStrategy == MatchClose {
				if match := fuzzyMatchLevenshtein(content, actualOld); match != "" {
					actualOld = match
					usedFuzzy = true
				}
			}
		}
	}

	count := strings.Count(content, actualOld)
	result.Replacements = count
	result.UsedFuzzy = usedFuzzy
	switch {
	case count == 0:
		result.Status = EditStatusNoMatch
		result.Error = "old string was not found"
		result.Summary = result.Error
		return content, result
	case count > 1 && !request.ReplaceAll:
		result.Status = EditStatusAmbiguousMatch
		result.Error = "old string matched multiple locations"
		result.Summary = result.Error
		return content, result
	}

	limit := 1
	if request.ReplaceAll {
		limit = -1
	}
	newString := request.New
	if usedFuzzy {
		newString = matchIndentation(actualOld, newString)
	}
	updated := strings.Replace(content, actualOld, newString, limit)
	result.Changed = updated != content
	if !result.Changed {
		result.Status = EditStatusUnchanged
		result.Summary = "edit " + strconv.Itoa(index) + " made no content change"
		return updated, result
	}

	result.Status = EditStatusChanged
	result.Summary = "edit " + strconv.Itoa(index) + " replaced " + strconv.Itoa(result.Replacements) + " occurrence(s)"
	if usedFuzzy {
		result.Summary += " (fuzzy match)"
	}
	return updated, result
}

func normalizedMatchStrategy(strategy MatchStrategy) MatchStrategy {
	if strategy == "" {
		return MatchStrategyExact
	}
	return strategy
}

func isValidMatchStrategy(strategy MatchStrategy) bool {
	switch strategy {
	case MatchStrategyExact, MatchClose:
		return true
	default:
		return false
	}
}

func summarizeMultiEditStatus(files []MultiEditFileResult, filesChanged int) MultiEditStatus {
	if len(files) == 0 || filesChanged == 0 {
		for _, file := range files {
			if file.Status != EditStatusUnchanged {
				return MultiEditStatusFailed
			}
		}
		return MultiEditStatusNoChanges
	}
	for _, file := range files {
		if file.Status != EditStatusChanged && file.Status != EditStatusUnchanged {
			return MultiEditStatusPartial
		}
	}
	return MultiEditStatusApplied
}

func matchIndentation(actualOld, newString string) string {
	firstLine := actualOld
	if index := strings.IndexByte(actualOld, '\n'); index >= 0 {
		firstLine = actualOld[:index]
	}
	leadingWhitespace := firstLine[:len(firstLine)-len(strings.TrimLeft(firstLine, " \t\r\n"))]
	if leadingWhitespace == "" {
		return newString
	}

	lines := strings.Split(newString, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, leadingWhitespace) {
			continue
		}
		lines[i] = leadingWhitespace + line
	}
	return strings.Join(lines, "\n")
}

// fuzzyMatchWhitespace attempts to find the target string by ignoring differences in whitespace.
func fuzzyMatchWhitespace(content, search string) string {
	words := strings.Fields(search)
	if len(words) == 0 {
		return ""
	}
	escapedWords := make([]string, 0, len(words))
	for _, word := range words {
		escapedWords = append(escapedWords, regexp.QuoteMeta(word))
	}
	pattern := strings.Join(escapedWords, `\s+`)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	matches := re.FindAllString(content, -1)
	if len(matches) != 1 {
		return ""
	}
	return matches[0]
}

func fuzzyMatchLevenshtein(content, search string) string {
	searchLines := strings.Split(search, "\n")
	if len(searchLines) < 3 {
		return ""
	}
	firstAnchor := strings.TrimSpace(searchLines[0])
	lastAnchor := strings.TrimSpace(searchLines[len(searchLines)-1])
	contentLines := strings.Split(content, "\n")
	candidates := make([]string, 0)

	for start, line := range contentLines {
		if strings.TrimSpace(line) != firstAnchor {
			continue
		}
		for end := start; end < len(contentLines); end++ {
			if strings.TrimSpace(contentLines[end]) != lastAnchor {
				continue
			}
			block := strings.Join(contentLines[start:end+1], "\n")
			dist := levenshtein.ComputeDistance(search, block)
			maxLen := max(len(search), len(block))
			if maxLen == 0 {
				continue
			}
			if 1.0-float64(dist)/float64(maxLen) > 0.85 {
				candidates = append(candidates, block)
			}
			break
		}
	}
	if len(candidates) != 1 {
		return ""
	}
	return candidates[0]
}

func editSummary(path string, replacements int, changed bool, usedFuzzy bool) string {
	if !changed {
		summary := path + " unchanged"
		if usedFuzzy {
			summary += " (fuzzy match)"
		}
		return summary
	}
	summary := filepath.Clean(path) + " changed with " + strconv.Itoa(replacements) + " replacement(s)"
	if usedFuzzy {
		summary += " (fuzzy match)"
	}
	return summary
}
