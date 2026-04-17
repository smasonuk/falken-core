package runtimeapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSubRunPaths_IsolatesStateDir(t *testing.T) {
	parent, err := NewPaths(t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewPaths failed: %v", err)
	}

	child := parent.SubRunPaths("child-run")
	if child.WorkspaceDir != parent.WorkspaceDir {
		t.Fatalf("expected workspace to be shared")
	}
	if child.StateDir == parent.StateDir {
		t.Fatalf("expected child state dir to differ from parent")
	}
	if child.MemoryPath() == parent.MemoryPath() || child.TodosPath() == parent.TodosPath() || child.HistoryPath() == parent.HistoryPath() {
		t.Fatalf("expected child memory/todo/history paths to be isolated")
	}
}

func TestSubRunPaths_MemoryIsolation(t *testing.T) {
	parent, err := NewPaths(t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewPaths failed: %v", err)
	}
	child := parent.SubRunPaths("child-run")

	if err := parent.EnsureStateDirs(); err != nil {
		t.Fatalf("parent EnsureStateDirs failed: %v", err)
	}
	if err := child.EnsureStateDirs(); err != nil {
		t.Fatalf("child EnsureStateDirs failed: %v", err)
	}

	if err := os.WriteFile(parent.MemoryPath(), []byte(`{"parent":true}`), 0644); err != nil {
		t.Fatalf("write parent memory: %v", err)
	}
	if err := os.WriteFile(child.MemoryPath(), []byte(`{"child":true}`), 0644); err != nil {
		t.Fatalf("write child memory: %v", err)
	}

	parentData, err := os.ReadFile(parent.MemoryPath())
	if err != nil {
		t.Fatalf("read parent memory: %v", err)
	}
	if string(parentData) != `{"parent":true}` {
		t.Fatalf("expected parent memory untouched, got %q", string(parentData))
	}
}

func TestSubRunPaths_SanitizesRunID(t *testing.T) {
	parent, err := NewPaths(t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewPaths failed: %v", err)
	}

	child := parent.SubRunPaths(filepath.Join("nested", "run"))
	expected := filepath.Join(parent.StateDir, "runs", "nested_run")
	if child.StateDir != expected {
		t.Fatalf("expected sanitized state dir %q, got %q", expected, child.StateDir)
	}
}
