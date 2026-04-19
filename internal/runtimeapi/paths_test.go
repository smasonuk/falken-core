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

func TestEnsureStateDirs_MigratesLegacyPaths(t *testing.T) {
	stateDir := t.TempDir()
	p := Paths{
		WorkspaceDir: t.TempDir(),
		StateDir:     stateDir,
	}

	// Create legacy files
	legacyFiles := []string{
		"history.jsonl",
		"memory.json",
		"tasks.json",
		"todos.json",
	}
	for _, f := range legacyFiles {
		path := filepath.Join(stateDir, f)
		if err := os.WriteFile(path, []byte(f), 0644); err != nil {
			t.Fatalf("failed to create legacy file %s: %v", f, err)
		}
	}
	// Create legacy tasks dir
	legacyTasksDir := filepath.Join(stateDir, "tasks")
	if err := os.MkdirAll(legacyTasksDir, 0755); err != nil {
		t.Fatalf("failed to create legacy tasks dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyTasksDir, "task1.json"), []byte("task1"), 0644); err != nil {
		t.Fatalf("failed to create legacy task file: %v", err)
	}

	// Run EnsureStateDirs (which triggers migration)
	if err := p.EnsureStateDirs(); err != nil {
		t.Fatalf("EnsureStateDirs failed: %v", err)
	}

	// Verify files moved
	expectedMigrations := map[string]string{
		filepath.Join(stateDir, "history.jsonl"): p.HistoryPath(),
		filepath.Join(stateDir, "memory.json"):  p.MemoryPath(),
		filepath.Join(stateDir, "tasks.json"):   p.TasksPath(),
		filepath.Join(stateDir, "todos.json"):   p.TodosPath(),
		filepath.Join(stateDir, "tasks", "task1.json"): filepath.Join(p.TasksDir(), "task1.json"),
	}

	for legacy, current := range expectedMigrations {
		if _, err := os.Stat(legacy); err == nil {
			t.Errorf("legacy path still exists: %s", legacy)
		}
		if _, err := os.Stat(current); err != nil {
			t.Errorf("migrated path does not exist: %s (%v)", current, err)
		}
	}
}
