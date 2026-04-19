package falken

import (
	"path/filepath"
	"testing"
)

func TestNewPathsDefaultsStateDirUnderWorkspace(t *testing.T) {
	workspace := t.TempDir()

	paths, err := NewPaths(workspace, "")
	if err != nil {
		t.Fatalf("NewPaths returned error: %v", err)
	}

	if paths.WorkspaceDir != workspace {
		t.Fatalf("expected workspace %q, got %q", workspace, paths.WorkspaceDir)
	}

	wantState := filepath.Join(workspace, ".falken")
	if paths.StateDir != wantState {
		t.Fatalf("expected state dir %q, got %q", wantState, paths.StateDir)
	}

	expectedTasks := filepath.Join(wantState, "state", "current", "tasks.json")
	if paths.TasksPath() != expectedTasks {
		t.Fatalf("unexpected tasks path: %s", paths.TasksPath())
	}
}

func TestNewPathsSupportsCustomStateDir(t *testing.T) {
	workspace := t.TempDir()
	stateDir := t.TempDir()

	paths, err := NewPaths(workspace, stateDir)
	if err != nil {
		t.Fatalf("NewPaths returned error: %v", err)
	}

	if paths.StateDir != stateDir {
		t.Fatalf("expected custom state dir %q, got %q", stateDir, paths.StateDir)
	}
	if paths.BackupDir() != filepath.Join(stateDir, "backups") {
		t.Fatalf("unexpected backup dir: %s", paths.BackupDir())
	}
}
