package runtimefiles

// Operation identifies the runtime-facing file operation.
type Operation string

const (
	OperationReadFile   Operation = "read_file"
	OperationReadFiles  Operation = "read_files"
	OperationGlob       Operation = "glob"
	OperationGrep       Operation = "grep"
	OperationWriteFile  Operation = "write_file"
	OperationEditFile   Operation = "edit_file"
	OperationMultiEdit  Operation = "multi_edit"
	OperationApplyPatch Operation = "apply_patch"
	OperationDeleteFile Operation = "delete_file"
)

// CommonResult is the normalized shape shared by runtime-facing file operation results.
type CommonResult struct {
	Operation    Operation `json:"operation"`
	Success      bool      `json:"success"`
	Status       string    `json:"status"`
	Path         string    `json:"path,omitempty"`
	ResolvedPath string    `json:"resolved_path,omitempty"`
	Error        string    `json:"error,omitempty"`
	BackupPaths  []string  `json:"backup_paths,omitempty"`
}

// FileChangeSummary describes one changed file for mutation-style runtime results.
type FileChangeSummary struct {
	Operation     Operation `json:"operation"`
	Status        string    `json:"status"`
	Path          string    `json:"path,omitempty"`
	OldPath       string    `json:"old_path,omitempty"`
	NewPath       string    `json:"new_path,omitempty"`
	ResolvedPath  string    `json:"resolved_path,omitempty"`
	BackupCreated bool      `json:"backup_created,omitempty"`
	BackupPath    string    `json:"backup_path,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// WorkspaceMutationSummary is the standard mutation metadata shape emitted by
// tools that can affect workspace files.
type WorkspaceMutationSummary struct {
	Observed          bool `json:"observed"`
	MayHaveOccurred   bool `json:"may_have_occurred,omitempty"`
	FilesChanged      int  `json:"files_changed,omitempty"`
	RollbackAttempted bool `json:"rollback_attempted,omitempty"`
	RollbackSucceeded bool `json:"rollback_succeeded,omitempty"`
}

func backupPaths(path string) []string {
	if path == "" {
		return nil
	}
	return []string{path}
}

func backupPathsFromSummaries(summaries []FileChangeSummary) []string {
	paths := make([]string, 0)
	for _, summary := range summaries {
		if summary.BackupPath != "" {
			paths = append(paths, summary.BackupPath)
		}
	}
	return paths
}
