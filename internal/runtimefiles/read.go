package runtimefiles

import (
	"context"

	"github.com/smasonuk/falken-core/internal/files"
)

// ReadFileRequest describes a runtime-facing managed file read.
type ReadFileRequest struct {
	Path              string `json:"path"`
	CurrentWorkingDir string `json:"working_dir,omitempty"`
	StartLine         int    `json:"start_line,omitempty"`
	EndLine           int    `json:"end_line,omitempty"`
	ApprovalRequired  bool   `json:"approval_required,omitempty"`
}

// ReadFileResult reports a runtime-facing managed file read.
type ReadFileResult struct {
	CommonResult
	Content    string           `json:"content,omitempty"`
	BytesRead  int              `json:"bytes_read"`
	StartLine  int              `json:"start_line,omitempty"`
	EndLine    int              `json:"end_line,omitempty"`
	TotalLines int              `json:"total_lines"`
	Token      files.ReadToken  `json:"-"`
	HasToken   bool             `json:"has_token"`
	Managed    files.ReadResult `json:"-"`
}

// ReadFilesRequest describes a batch of managed file reads.
type ReadFilesRequest struct {
	Files []ReadFileRequest `json:"files"`
}

// ReadFilesResult reports a batch of managed file reads.
type ReadFilesResult struct {
	Operation Operation        `json:"operation"`
	Success   bool             `json:"success"`
	Status    string           `json:"status"`
	Files     []ReadFileResult `json:"files"`
	Total     int              `json:"total"`
	Failed    int              `json:"failed"`
	Error     string           `json:"error,omitempty"`
}

// ReadFile delegates to the managed file read service.
func (o *Operations) ReadFile(ctx context.Context, request ReadFileRequest) (ReadFileResult, error) {
	managed, err := o.service.Read(ctx, files.ReadRequest{
		Path:              request.Path,
		CurrentWorkingDir: request.CurrentWorkingDir,
		StartLine:         request.StartLine,
		EndLine:           request.EndLine,
		ApprovalRequired:  request.ApprovalRequired,
	})
	if err != nil {
		return ReadFileResult{}, err
	}

	return adaptReadFileResult(managed), nil
}

// ReadFiles delegates each requested read to the managed file read service.
func (o *Operations) ReadFiles(ctx context.Context, request ReadFilesRequest) (ReadFilesResult, error) {
	result := ReadFilesResult{
		Operation: OperationReadFiles,
		Files:     make([]ReadFileResult, 0, len(request.Files)),
		Total:     len(request.Files),
		Success:   true,
	}

	for _, file := range request.Files {
		read, err := o.ReadFile(ctx, file)
		if err != nil {
			return ReadFilesResult{}, err
		}
		result.Files = append(result.Files, read)
		if !read.Success {
			result.Success = false
			result.Failed++
			if result.Error == "" {
				result.Error = read.Error
			}
		}
	}
	result.Status = readFilesStatus(result.Total, result.Failed)

	return result, nil
}

func adaptReadFileResult(managed files.ReadResult) ReadFileResult {
	return ReadFileResult{
		CommonResult: CommonResult{
			Operation:    OperationReadFile,
			Success:      managed.Status == files.ReadStatusOK,
			Status:       string(managed.Status),
			Path:         managed.Path,
			ResolvedPath: managed.ResolvedPath,
			Error:        managed.Error,
		},
		Content:    managed.Content,
		BytesRead:  len([]byte(managed.Content)),
		StartLine:  managed.StartLine,
		EndLine:    managed.EndLine,
		TotalLines: managed.TotalLines,
		Token:      managed.Token,
		HasToken:   managed.HasToken,
		Managed:    managed,
	}
}

func readFilesStatus(total, failed int) string {
	if failed == 0 {
		return "ok"
	}
	if failed == total {
		return "failed"
	}
	return "partial"
}
