package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/workspace"
)

const (
	defaultSearchLimit = 100
	maxSearchLimit     = 1000
	defaultGrepLimit   = 250
	defaultLineBytes   = 500
	maxContextLines    = 10
)

// GlobStatus identifies the deterministic outcome of a managed glob search.
type GlobStatus string

const (
	GlobStatusOK             GlobStatus = "ok"
	GlobStatusInvalidPattern GlobStatus = "invalid_pattern"
	GlobStatusUnsafe         GlobStatus = "unsafe_path"
	GlobStatusNotFound       GlobStatus = "not_found"
	GlobStatusNotDirectory   GlobStatus = "not_directory"
	GlobStatusDenied         GlobStatus = "denied"
)

// GlobRequest describes a workspace-scoped glob search.
type GlobRequest struct {
	Pattern           string
	Path              string
	CurrentWorkingDir string
	IncludeDirs       bool
	IncludeFiles      *bool
	IncludeHidden     bool
	IncludeIgnored    bool
	Ignore            []string
	Limit             int
	Offset            int
	Sort              string
	ApprovalRequired  bool
}

// GlobResult reports a workspace-scoped glob search.
type GlobResult struct {
	Status       GlobStatus
	Pattern      string
	Root         string
	ResolvedRoot string
	Matches      []string
	TotalMatches int
	Returned     int
	Offset       int
	Limit        int
	Truncated    bool
	FilesSkipped int
	Policy       runtimepolicy.FileResult
	Error        string
}

// GrepStatus identifies the deterministic outcome of a managed grep search.
type GrepStatus string

const (
	GrepStatusOK                GrepStatus = "ok"
	GrepStatusInvalidRegex      GrepStatus = "invalid_regex"
	GrepStatusInvalidPattern    GrepStatus = "invalid_pattern"
	GrepStatusInvalidOutputMode GrepStatus = "invalid_output_mode"
	GrepStatusUnsafe            GrepStatus = "unsafe_path"
	GrepStatusNotFound          GrepStatus = "not_found"
	GrepStatusDenied            GrepStatus = "denied"
)

// GrepRequest describes a policy-gated workspace content search.
type GrepRequest struct {
	Regex             string
	TargetPaths       []string
	CurrentWorkingDir string
	Glob              string
	OutputMode        string
	CaseSensitive     *bool
	Before            int
	After             int
	Context           int
	Limit             int
	Offset            int
	MaxLineBytes      int
	IncludeHidden     bool
	IncludeIgnored    bool
	ApprovalRequired  bool
}

// GrepMatch is one line-level content match.
type GrepMatch struct {
	Path          string
	Line          int
	Text          string
	ContextBefore []string
	ContextAfter  []string
}

// GrepFileCount is the line-match count for one file.
type GrepFileCount struct {
	Path    string
	Matches int
}

// GrepResult reports a policy-gated workspace content search.
type GrepResult struct {
	Status           GrepStatus
	Regex            string
	OutputMode       string
	Matches          []GrepMatch
	Files            []string
	Counts           []GrepFileCount
	TotalMatchesSeen int
	Returned         int
	Offset           int
	Limit            int
	Truncated        bool
	NextOffset       int
	FilesScanned     int
	FilesWithMatches int
	FilesSkipped     int
	Error            string
}

type searchOptions struct {
	includeHidden  bool
	includeIgnored bool
	ignore         []globMatcher
}

type globCandidate struct {
	path    string
	modTime time.Time
}

// Glob returns workspace-relative files and directories matching a glob pattern.
func (s *Service) Glob(ctx context.Context, request GlobRequest) (GlobResult, error) {
	limit := normalizeLimit(request.Limit, defaultSearchLimit)
	offset := normalizeOffset(request.Offset)
	root := strings.TrimSpace(request.Path)
	if root == "" {
		root = "."
	}
	result := GlobResult{
		Pattern: request.Pattern,
		Root:    root,
		Matches: []string{},
		Offset:  offset,
		Limit:   limit,
	}

	matcher, err := newGlobMatcher(request.Pattern)
	if err != nil {
		result.Status = GlobStatusInvalidPattern
		result.Error = err.Error()
		return result, nil
	}
	opts, err := newSearchOptions(request.IncludeHidden, request.IncludeIgnored, request.Ignore)
	if err != nil {
		result.Status = GlobStatusInvalidPattern
		result.Error = err.Error()
		return result, nil
	}
	if !isGlobSort(request.Sort) {
		result.Status = GlobStatusInvalidPattern
		result.Error = "sort must be one of path, modified_desc, or modified_asc"
		return result, nil
	}

	resolvedRoot, err := s.resolveExisting(request.CurrentWorkingDir, root)
	if err != nil {
		result.Error = err.Error()
		if errors.Is(err, os.ErrNotExist) {
			result.Status = GlobStatusNotFound
			return result, nil
		}
		result.Status = GlobStatusUnsafe
		return result, nil
	}
	result.ResolvedRoot = resolvedRoot
	stat, err := os.Stat(resolvedRoot)
	if err != nil {
		return GlobResult{}, fmt.Errorf("stat glob root: %w", err)
	}
	if !stat.IsDir() {
		result.Status = GlobStatusNotDirectory
		result.Error = "path is not a directory"
		return result, nil
	}
	rootPolicy, err := s.evaluateSearchRead(ctx, resolvedRoot, request.ApprovalRequired)
	if err != nil {
		return GlobResult{}, fmt.Errorf("evaluate glob root policy: %w", err)
	}
	result.Policy = rootPolicy
	if !rootPolicy.Allowed {
		result.Status = GlobStatusDenied
		result.Error = rootPolicy.Explanation
		return result, nil
	}

	includeFiles := true
	if request.IncludeFiles != nil {
		includeFiles = *request.IncludeFiles
	}
	var candidates []globCandidate
	err = filepath.WalkDir(resolvedRoot, func(candidatePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.FilesSkipped++
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if candidatePath == resolvedRoot {
			return nil
		}

		relRoot, err := slashRel(resolvedRoot, candidatePath)
		if err != nil {
			result.FilesSkipped++
			return nil
		}
		workspaceRel, err := s.workspaceRel(candidatePath)
		if err != nil {
			result.FilesSkipped++
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if opts.skipPath(relRoot, workspaceRel, entry.IsDir()) {
			result.FilesSkipped++
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		resolved, inside, err := s.resolveSearchCandidate(candidatePath)
		if err != nil {
			return err
		}
		if !inside {
			result.FilesSkipped++
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			policyResult, err := s.evaluateSearchRead(ctx, resolved, request.ApprovalRequired)
			if err != nil {
				return err
			}
			if !policyResult.Allowed {
				result.FilesSkipped++
				return fs.SkipDir
			}
			if request.IncludeDirs && matcher.Match(relRoot) {
				info, err := os.Stat(resolved)
				if err != nil {
					return err
				}
				candidates = append(candidates, globCandidate{path: workspaceRel, modTime: info.ModTime()})
			}
			return nil
		}
		if !includeFiles || !matcher.Match(relRoot) {
			return nil
		}
		policyResult, err := s.evaluateSearchRead(ctx, resolved, request.ApprovalRequired)
		if err != nil {
			return err
		}
		if !policyResult.Allowed {
			result.FilesSkipped++
			return nil
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			result.FilesSkipped++
			return nil
		}
		candidates = append(candidates, globCandidate{path: workspaceRel, modTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return GlobResult{}, err
	}

	sortGlobCandidates(candidates, request.Sort)
	result.TotalMatches = len(candidates)
	for _, candidate := range paginateGlobCandidates(candidates, offset, limit) {
		result.Matches = append(result.Matches, candidate.path)
	}
	result.Returned = len(result.Matches)
	result.Truncated = offset+result.Returned < result.TotalMatches
	result.Status = GlobStatusOK
	return result, nil
}

// Grep searches workspace file contents without issuing read tokens.
func (s *Service) Grep(ctx context.Context, request GrepRequest) (GrepResult, error) {
	mode := normalizedGrepMode(request.OutputMode)
	limit := normalizeLimit(request.Limit, defaultGrepLimit)
	offset := normalizeOffset(request.Offset)
	result := GrepResult{
		Regex:      request.Regex,
		OutputMode: mode,
		Matches:    []GrepMatch{},
		Files:      []string{},
		Counts:     []GrepFileCount{},
		Offset:     offset,
		Limit:      limit,
	}
	if mode == "" {
		result.Status = GrepStatusInvalidOutputMode
		result.Error = "output_mode must be one of content, files_with_matches, or count"
		return result, nil
	}
	pattern := strings.TrimSpace(request.Regex)
	if pattern == "" {
		result.Status = GrepStatusInvalidRegex
		result.Error = "regex is required"
		return result, nil
	}
	if request.CaseSensitive != nil && !*request.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		result.Status = GrepStatusInvalidRegex
		result.Error = err.Error()
		return result, nil
	}
	var fileMatcher globMatcher
	if strings.TrimSpace(request.Glob) != "" {
		fileMatcher, err = newGlobMatcher(request.Glob)
		if err != nil {
			result.Status = GrepStatusInvalidPattern
			result.Error = err.Error()
			return result, nil
		}
	}

	targets := request.TargetPaths
	if len(targets) == 0 {
		targets = []string{"."}
	}
	opts := searchOptions{includeHidden: request.IncludeHidden, includeIgnored: request.IncludeIgnored}
	before, after := normalizedContext(request.Before, request.After, request.Context)
	maxLineBytes := request.MaxLineBytes
	if maxLineBytes <= 0 {
		maxLineBytes = defaultLineBytes
	}
	seen := map[string]struct{}{}

	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			target = "."
		}
		resolved, err := s.resolveExisting(request.CurrentWorkingDir, target)
		if err != nil {
			result.Error = err.Error()
			if errors.Is(err, os.ErrNotExist) {
				result.Status = GrepStatusNotFound
				return result, nil
			}
			result.Status = GrepStatusUnsafe
			return result, nil
		}
		stat, err := os.Stat(resolved)
		if err != nil {
			return GrepResult{}, fmt.Errorf("stat grep target: %w", err)
		}
		if stat.IsDir() {
			policyResult, err := s.evaluateSearchRead(ctx, resolved, request.ApprovalRequired)
			if err != nil {
				return GrepResult{}, err
			}
			if !policyResult.Allowed {
				result.Status = GrepStatusDenied
				result.Error = policyResult.Explanation
				return result, nil
			}
			if err := s.walkGrepDirectory(ctx, resolved, fileMatcher, opts, request, re, before, after, maxLineBytes, seen, &result); err != nil {
				return GrepResult{}, err
			}
			continue
		}
		if err := s.grepCandidate(ctx, resolved, fileMatcher, opts, request, re, before, after, maxLineBytes, seen, &result); err != nil {
			return GrepResult{}, err
		}
	}
	result.Returned = grepReturned(result)
	if mode == "count" {
		result.Truncated = result.FilesWithMatches > offset+result.Returned
	} else {
		result.Truncated = result.TotalMatchesSeen > offset+result.Returned
	}
	if result.Truncated {
		result.NextOffset = offset + result.Returned
	}
	result.Status = GrepStatusOK
	return result, nil
}

func (s *Service) walkGrepDirectory(ctx context.Context, root string, fileMatcher globMatcher, opts searchOptions, request GrepRequest, re *regexp.Regexp, before, after, maxLineBytes int, seen map[string]struct{}, result *GrepResult) error {
	return filepath.WalkDir(root, func(candidatePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.FilesSkipped++
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if candidatePath == root {
			return nil
		}
		relRoot, err := slashRel(root, candidatePath)
		if err != nil {
			result.FilesSkipped++
			return nil
		}
		workspaceRel, err := s.workspaceRel(candidatePath)
		if err != nil {
			result.FilesSkipped++
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if opts.skipPath(relRoot, workspaceRel, entry.IsDir()) {
			result.FilesSkipped++
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			resolved, inside, err := s.resolveSearchCandidate(candidatePath)
			if err != nil {
				return err
			}
			if !inside {
				result.FilesSkipped++
				return fs.SkipDir
			}
			policyResult, err := s.evaluateSearchRead(ctx, resolved, request.ApprovalRequired)
			if err != nil {
				return err
			}
			if !policyResult.Allowed {
				result.FilesSkipped++
				return fs.SkipDir
			}
			return nil
		}
		return s.grepCandidate(ctx, candidatePath, fileMatcher, opts, request, re, before, after, maxLineBytes, seen, result)
	})
}

func (s *Service) grepCandidate(ctx context.Context, candidatePath string, fileMatcher globMatcher, opts searchOptions, request GrepRequest, re *regexp.Regexp, before, after, maxLineBytes int, seen map[string]struct{}, result *GrepResult) error {
	workspaceRel, err := s.workspaceRel(candidatePath)
	if err != nil {
		result.FilesSkipped++
		return nil
	}
	if opts.skipPath(workspaceRel, workspaceRel, false) {
		result.FilesSkipped++
		return nil
	}
	if fileMatcher != nil && !matchesFileGlob(fileMatcher, workspaceRel) {
		return nil
	}
	resolved, inside, err := s.resolveSearchCandidate(candidatePath)
	if err != nil {
		return err
	}
	if !inside {
		result.FilesSkipped++
		return nil
	}
	if _, exists := seen[resolved]; exists {
		return nil
	}
	seen[resolved] = struct{}{}

	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		result.FilesSkipped++
		return nil
	}
	policyResult, err := s.evaluateSearchRead(ctx, resolved, request.ApprovalRequired)
	if err != nil {
		return err
	}
	if !policyResult.Allowed {
		result.FilesSkipped++
		return nil
	}
	if isBinaryByExtension(resolved) {
		result.FilesSkipped++
		return nil
	}
	binary, err := fileContainsNUL(resolved)
	if err != nil {
		return err
	}
	if binary {
		result.FilesSkipped++
		return nil
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	result.FilesScanned++
	lines := splitSearchText(string(data))
	switch result.OutputMode {
	case "files_with_matches":
		for _, line := range lines {
			if re.MatchString(line) {
				result.TotalMatchesSeen++
				result.FilesWithMatches++
				if result.TotalMatchesSeen > result.Offset && len(result.Files) < result.Limit {
					result.Files = append(result.Files, workspaceRel)
				}
				return nil
			}
		}
	case "count":
		count := 0
		for _, line := range lines {
			if re.MatchString(line) {
				count++
			}
		}
		if count > 0 {
			result.TotalMatchesSeen += count
			result.FilesWithMatches++
			if result.FilesWithMatches > result.Offset && len(result.Counts) < result.Limit {
				result.Counts = append(result.Counts, GrepFileCount{Path: workspaceRel, Matches: count})
			}
		}
	default:
		matchedFile := false
		for i, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			result.TotalMatchesSeen++
			matchedFile = true
			if result.TotalMatchesSeen <= result.Offset || len(result.Matches) >= result.Limit {
				continue
			}
			result.Matches = append(result.Matches, GrepMatch{
				Path:          workspaceRel,
				Line:          i + 1,
				Text:          truncateSearchLine(line, maxLineBytes),
				ContextBefore: contextBefore(lines, i, before, maxLineBytes),
				ContextAfter:  contextAfter(lines, i, after, maxLineBytes),
			})
		}
		if matchedFile {
			result.FilesWithMatches++
		}
	}
	return nil
}

func (s *Service) evaluateSearchRead(ctx context.Context, path string, approvalRequired bool) (runtimepolicy.FileResult, error) {
	return s.policy.EvaluateFile(ctx, runtimepolicy.FileRequest{
		Path:             path,
		Mode:             policy.FileAccessRead,
		ApprovalRequired: approvalRequired,
	})
}

func (s *Service) workspaceRel(candidatePath string) (string, error) {
	real, inside, err := s.resolveSearchCandidate(candidatePath)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", workspace.ErrPathOutsideWorkspace
	}
	return slashRel(s.realWorkspaceRoot, real)
}

func (s *Service) resolveSearchCandidate(candidatePath string) (string, bool, error) {
	real, err := filepath.EvalSymlinks(candidatePath)
	if err != nil {
		return "", false, nil
	}
	real = filepath.Clean(real)
	rel, err := filepath.Rel(s.realWorkspaceRoot, real)
	if err != nil {
		return "", false, err
	}
	if rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return real, false, nil
	}
	return real, true, nil
}

func slashRel(root, candidatePath string) (string, error) {
	rel, err := filepath.Rel(root, candidatePath)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return ".", nil
	}
	if strings.HasPrefix(rel, "../") || rel == ".." || filepath.IsAbs(rel) {
		return "", workspace.ErrPathOutsideWorkspace
	}
	return rel, nil
}

func newSearchOptions(includeHidden, includeIgnored bool, ignores []string) (searchOptions, error) {
	opts := searchOptions{includeHidden: includeHidden, includeIgnored: includeIgnored}
	for _, pattern := range ignores {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		matcher, err := newGlobMatcher(pattern)
		if err != nil {
			return searchOptions{}, err
		}
		opts.ignore = append(opts.ignore, matcher)
	}
	return opts, nil
}

func (o searchOptions) skipPath(relRoot, workspaceRel string, isDir bool) bool {
	base := path.Base(filepath.ToSlash(workspaceRel))
	if !o.includeHidden && (hasHiddenPathPart(relRoot) || hasHiddenPathPart(workspaceRel)) {
		return true
	}
	if !o.includeIgnored && isDir && defaultIgnoredDir(base) {
		return true
	}
	for _, matcher := range o.ignore {
		if matcher.Match(relRoot) || matcher.Match(workspaceRel) || matcher.Match(base) {
			return true
		}
	}
	return false
}

func hasHiddenPathPart(value string) bool {
	value = filepath.ToSlash(value)
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func defaultIgnoredDir(name string) bool {
	switch name {
	case ".git", ".svn", ".hg", ".bzr", "node_modules", "vendor", "__pycache__", ".next", "dist", "build":
		return true
	default:
		return false
	}
}

func isGlobSort(value string) bool {
	switch value {
	case "", "path", "modified_desc", "modified_asc":
		return true
	default:
		return false
	}
}

func sortGlobCandidates(candidates []globCandidate, sortMode string) {
	switch sortMode {
	case "modified_desc":
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].modTime.Equal(candidates[j].modTime) {
				return candidates[i].path < candidates[j].path
			}
			return candidates[i].modTime.After(candidates[j].modTime)
		})
	case "modified_asc":
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].modTime.Equal(candidates[j].modTime) {
				return candidates[i].path < candidates[j].path
			}
			return candidates[i].modTime.Before(candidates[j].modTime)
		})
	default:
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].path < candidates[j].path
		})
	}
}

func paginateGlobCandidates(candidates []globCandidate, offset, limit int) []globCandidate {
	if offset >= len(candidates) {
		return nil
	}
	end := offset + limit
	if end > len(candidates) {
		end = len(candidates)
	}
	return candidates[offset:end]
}

func normalizeLimit(value, fallback int) int {
	if value <= 0 {
		value = fallback
	}
	if value > maxSearchLimit {
		value = maxSearchLimit
	}
	return value
}

func normalizeOffset(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

type globMatcher interface {
	Match(string) bool
}

type regexpGlobMatcher struct {
	re *regexp.Regexp
}

func (m regexpGlobMatcher) Match(value string) bool {
	return m.re.MatchString(filepath.ToSlash(value))
}

func newGlobMatcher(pattern string) (globMatcher, error) {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return nil, errors.New("pattern is required")
	}
	if path.IsAbs(pattern) {
		return nil, fmt.Errorf("glob pattern %q must be relative", pattern)
	}
	source, err := globPatternRegexp(pattern)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile(source)
	if err != nil {
		return nil, fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
	}
	return regexpGlobMatcher{re: re}, nil
}

func globPatternRegexp(pattern string) (string, error) {
	var builder strings.Builder
	builder.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				for i+1 < len(pattern) && pattern[i+1] == '*' {
					i++
				}
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					builder.WriteString("(?:.*/)?")
					i++
				} else {
					builder.WriteString(".*")
				}
			} else {
				builder.WriteString("[^/]*")
			}
		case '?':
			builder.WriteString("[^/]")
		case '[':
			end := i + 1
			if end < len(pattern) && (pattern[end] == '!' || pattern[end] == '^') {
				end++
			}
			if end < len(pattern) && pattern[end] == ']' {
				end++
			}
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end >= len(pattern) {
				return "", fmt.Errorf("invalid glob pattern %q: missing closing ]", pattern)
			}
			class := pattern[i+1 : end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			builder.WriteString("[")
			builder.WriteString(class)
			builder.WriteString("]")
			i = end
		case '\\':
			if i+1 >= len(pattern) {
				builder.WriteString(regexp.QuoteMeta(`\`))
			} else {
				i++
				builder.WriteString(regexp.QuoteMeta(string(pattern[i])))
			}
		default:
			builder.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	builder.WriteString("$")
	return builder.String(), nil
}

func normalizedGrepMode(value string) string {
	switch strings.TrimSpace(value) {
	case "", "content":
		return "content"
	case "files_with_matches":
		return "files_with_matches"
	case "count":
		return "count"
	default:
		return ""
	}
}

func normalizedContext(before, after, contextLines int) (int, int) {
	before = clampContext(before)
	after = clampContext(after)
	if contextLines > 0 {
		contextLines = clampContext(contextLines)
		return contextLines, contextLines
	}
	return before, after
}

func clampContext(value int) int {
	if value < 0 {
		return 0
	}
	if value > maxContextLines {
		return maxContextLines
	}
	return value
}

func matchesFileGlob(matcher globMatcher, workspaceRel string) bool {
	workspaceRel = filepath.ToSlash(workspaceRel)
	return matcher.Match(workspaceRel) || matcher.Match(path.Base(workspaceRel))
}

func splitSearchText(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func contextBefore(lines []string, matchIndex, before, maxLineBytes int) []string {
	start := matchIndex - before
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, matchIndex-start)
	for _, line := range lines[start:matchIndex] {
		out = append(out, truncateSearchLine(line, maxLineBytes))
	}
	return out
}

func contextAfter(lines []string, matchIndex, after, maxLineBytes int) []string {
	end := matchIndex + 1 + after
	if end > len(lines) {
		end = len(lines)
	}
	out := make([]string, 0, end-matchIndex-1)
	for _, line := range lines[matchIndex+1 : end] {
		out = append(out, truncateSearchLine(line, maxLineBytes))
	}
	return out
}

func truncateSearchLine(line string, maxBytes int) string {
	line = strings.ToValidUTF8(line, "\uFFFD")
	if maxBytes <= 0 || len([]byte(line)) <= maxBytes {
		return line
	}
	data := []byte(line)
	if maxBytes > len(data) {
		maxBytes = len(data)
	}
	return strings.ToValidUTF8(string(data[:maxBytes]), "\uFFFD") + "...[TRUNCATED]"
}

func grepReturned(result GrepResult) int {
	switch result.OutputMode {
	case "files_with_matches":
		return len(result.Files)
	case "count":
		return len(result.Counts)
	default:
		return len(result.Matches)
	}
}

func isBinaryByExtension(filename string) bool {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".wasm", ".exe", ".bin", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".pdf", ".zip", ".gz", ".tar", ".tgz", ".mp4", ".mov", ".mp3":
		return true
	default:
		return false
	}
}

func fileContainsNUL(filename string) (bool, error) {
	file, err := os.Open(filename)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()

	buf := make([]byte, 8192)
	n, err := file.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return bytes.IndexByte(buf[:n], 0) >= 0, nil
}
