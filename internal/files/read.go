package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
)

// ReadStatus identifies the deterministic outcome of a managed file read.
type ReadStatus string

const (
	ReadStatusOK        ReadStatus = "ok"
	ReadStatusNotFound  ReadStatus = "not_found"
	ReadStatusDirectory ReadStatus = "directory"
	ReadStatusDenied    ReadStatus = "denied"
	ReadStatusUnsafe    ReadStatus = "unsafe_path"
)

// ReadRequest describes a canonical managed file read.
type ReadRequest struct {
	Path              string
	CurrentWorkingDir string
	StartLine         int
	EndLine           int
	ApprovalRequired  bool
}

// ReadResult is the structured result of a managed file read.
type ReadResult struct {
	Status       ReadStatus
	Path         string
	ResolvedPath string
	Content      string
	StartLine    int
	EndLine      int
	TotalLines   int
	Policy       runtimepolicy.FileResult
	Token        ReadToken
	HasToken     bool
	Error        string
}

type readOptions struct {
	IssueToken bool
}

// Read is the model/user-facing managed read operation.
// It issues a read token that authorizes later managed mutations, provided the
// file has not changed.
//
// Internal service code that needs file contents for planning, preflight,
// rollback, or verification must not call Read unless it intentionally wants
// to grant mutation authority. Use readSnapshot instead.
func (s *Service) Read(ctx context.Context, request ReadRequest) (ReadResult, error) {
	return s.read(ctx, request, readOptions{IssueToken: true})
}

// readSnapshot reads file content through the same path and policy gates as
// Read, but does not issue or refresh a read token. Use this for internal
// preflight reads.
func (s *Service) readSnapshot(ctx context.Context, request ReadRequest) (ReadResult, error) {
	return s.read(ctx, request, readOptions{IssueToken: false})
}

func (s *Service) read(ctx context.Context, request ReadRequest, options readOptions) (ReadResult, error) {
	if err := validateLineRange(request.StartLine, request.EndLine); err != nil {
		return ReadResult{}, err
	}

	result := ReadResult{
		Path:      request.Path,
		StartLine: normalizedStartLine(request.StartLine),
		EndLine:   request.EndLine,
	}

	resolved, err := s.resolveExisting(request.CurrentWorkingDir, request.Path)
	if err != nil {
		result.Error = err.Error()
		if errors.Is(err, os.ErrNotExist) {
			result.Status = ReadStatusNotFound
			return result, nil
		}
		result.Status = ReadStatusUnsafe
		return result, nil
	}
	result.ResolvedPath = resolved

	stat, err := os.Stat(resolved)
	if err != nil {
		return ReadResult{}, fmt.Errorf("stat read path: %w", err)
	}
	if stat.IsDir() {
		result.Status = ReadStatusDirectory
		result.Error = "path is a directory"
		return result, nil
	}

	policyResult, err := s.policy.EvaluateFile(ctx, runtimepolicy.FileRequest{
		Path:             resolved,
		Mode:             policy.FileAccessRead,
		ApprovalRequired: request.ApprovalRequired,
	})
	if err != nil {
		return ReadResult{}, fmt.Errorf("evaluate file read policy: %w", err)
	}
	result.Policy = policyResult
	if !policyResult.Allowed {
		result.Status = ReadStatusDenied
		result.Error = policyResult.Explanation
		return result, nil
	}

	data, stat, err := readManagedExistingFileSnapshot(resolved, s.realWorkspaceRoot)
	if err != nil {
		return ReadResult{}, fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	totalLines := len(splitLines(content))
	result.TotalLines = totalLines
	result.Content = selectLineRange(content, request.StartLine, request.EndLine)

	result.Status = ReadStatusOK
	if options.IssueToken {
		token := tokenForSnapshot(s.tokens.ScopeID(), resolved, data, stat, time.Now().UTC())
		if token.ID == "" {
			return ReadResult{}, fmt.Errorf("create read token for %q: unavailable file identity", resolved)
		}
		s.tokens.record(token)
		result.Token = token
		result.HasToken = true
	}
	return result, nil
}

func validateLineRange(start, end int) error {
	if start < 0 || end < 0 {
		return ErrInvalidLineRange
	}
	if start > 0 && end > 0 && end < start {
		return ErrInvalidLineRange
	}
	return nil
}

func normalizedStartLine(start int) int {
	if start == 0 {
		return 1
	}
	return start
}

func selectLineRange(content string, start, end int) string {
	if start == 0 && end == 0 {
		return content
	}

	lines := splitLines(content)
	if len(lines) == 0 {
		return ""
	}

	if start == 0 {
		start = 1
	}
	if start > len(lines) {
		return ""
	}
	if end == 0 || end > len(lines) {
		end = len(lines)
	}

	var selected strings.Builder
	for _, line := range lines[start-1 : end] {
		selected.WriteString(line)
	}
	return selected.String()
}

func splitLines(content string) []string {
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
