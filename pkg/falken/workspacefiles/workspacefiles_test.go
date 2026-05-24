package workspacefiles_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

func TestNewLocalOperationsUsesManagedFileServiceAndDefaultRunnerState(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	ops, err := workspacefiles.NewLocalOperations(workspacefiles.LocalOperationsConfig{
		WorkspaceRoot: workspace,
		ScopeID:       "external-style-test",
	})
	if err != nil {
		t.Fatalf("NewLocalOperations: %v", err)
	}

	var _ workspacefiles.Operations = ops

	read, err := ops.ReadFile(context.Background(), workspacefiles.ReadFileRequest{Path: "note.txt"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !read.Success || read.Content != "hello world\n" {
		t.Fatalf("ReadFile = %+v, want file content", read)
	}

	edit, err := ops.EditFile(context.Background(), workspacefiles.EditFileRequest{
		Path: "note.txt",
		Old:  "world",
		New:  "runner",
	})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if !edit.Success || !edit.Changed {
		t.Fatalf("EditFile = %+v, want changed", edit)
	}
	if edit.BackupPaths == nil {
		t.Fatalf("EditFile backup paths = nil, want backup under default runner state")
	}
	if _, err := os.Stat(filepath.Join(workspace, ".falken-runner-state", "backups")); err != nil {
		t.Fatalf("expected default runner backup root: %v", err)
	}
}
