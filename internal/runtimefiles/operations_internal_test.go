package runtimefiles

import (
	"testing"

	"github.com/smasonuk/falken-core/internal/files"
)

func TestAdaptDeleteFileResultPreservesCommitUncertain(t *testing.T) {
	managed := files.DeleteResult{
		Status:                  files.DeleteStatusCommitUncertain,
		Path:                    "delete.txt",
		ResolvedPath:            "/workspace/delete.txt",
		Deleted:                 true,
		BackupCreated:           true,
		BackupPath:              "/state/backups/delete.txt",
		MutationMayHaveOccurred: true,
		Error:                   "delete managed file: mutation may have occurred",
	}

	result := adaptDeleteFileResult(managed)
	if result.Success {
		t.Fatalf("success = true, want false for commit_uncertain result=%+v", result)
	}
	if result.Status != string(files.DeleteStatusCommitUncertain) || !result.Deleted || !result.MutationMayHaveOccurred {
		t.Fatalf("result = %+v, want commit_uncertain deleted mutation", result)
	}
	if len(result.BackupPaths) != 1 || result.BackupPaths[0] != managed.BackupPath {
		t.Fatalf("backup paths = %+v, want managed backup path", result.BackupPaths)
	}
	if result.Error != managed.Error {
		t.Fatalf("error = %q, want %q", result.Error, managed.Error)
	}
}
