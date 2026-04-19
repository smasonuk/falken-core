package runtimeapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPaths_Layout(t *testing.T) {
	workspace := t.TempDir()
	paths, err := NewPaths(workspace, "")
	if err != nil {
		t.Fatalf("NewPaths failed: %v", err)
	}

	expectedStateDir := filepath.Join(workspace, ".falken")
	expectedCurrentDir := filepath.Join(expectedStateDir, "state", "current")

	if paths.CurrentStateDir() != expectedCurrentDir {
		t.Errorf("expected CurrentStateDir %q, got %q", expectedCurrentDir, paths.CurrentStateDir())
	}
	if paths.HistoryPath() != filepath.Join(expectedCurrentDir, "history.jsonl") {
		t.Errorf("unexpected HistoryPath")
	}
	if paths.MemoryPath() != filepath.Join(expectedCurrentDir, "memory.json") {
		t.Errorf("unexpected MemoryPath")
	}
	if paths.PluginStateDir() != filepath.Join(expectedCurrentDir, "plugin_states") {
		t.Errorf("unexpected PluginStateDir")
	}
	if paths.RunsDir() != filepath.Join(expectedCurrentDir, "runs") {
		t.Errorf("unexpected RunsDir")
	}
	if paths.TruncationDir() != filepath.Join(expectedCurrentDir, "truncations") {
		t.Errorf("unexpected TruncationDir")
	}
	if paths.CacheDir() != filepath.Join(expectedStateDir, "cache") {
		t.Errorf("unexpected CacheDir")
	}
	if paths.BackupDir() != filepath.Join(expectedStateDir, "backups") {
		t.Errorf("unexpected BackupDir")
	}
}

func TestSubRunPaths_Robustness(t *testing.T) {
	parent, _ := NewPaths(t.TempDir(), "")
	sub := parent.SubRunPaths("abc")
	expectedSubState := filepath.Join(parent.CurrentStateDir(), "runs", "abc")

	if sub.StateDir != expectedSubState {
		t.Errorf("expected sub StateDir %q, got %q", expectedSubState, sub.StateDir)
	}

	if sub.CurrentStateDir() != expectedSubState {
		t.Errorf("expected sub CurrentStateDir %q, got %q", expectedSubState, sub.CurrentStateDir())
	}

	if sub.HistoryPath() != filepath.Join(expectedSubState, "history.jsonl") {
		t.Errorf("expected sub history path under runs/abc")
	}

	// Nested sub-run (e.g. verifier)
	verify := sub.SubRunPaths("verify_123")
	expectedVerifyState := filepath.Join(expectedSubState, "runs", "verify_123")
	if verify.StateDir != expectedVerifyState {
		t.Errorf("expected verify StateDir %q, got %q", expectedVerifyState, verify.StateDir)
	}
	if verify.CurrentStateDir() != expectedVerifyState {
		t.Errorf("expected verify CurrentStateDir %q, got %q", expectedVerifyState, verify.CurrentStateDir())
	}
}

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

	if err := parent.EnsureStateDirs(false); err != nil {
		t.Fatalf("parent EnsureStateDirs failed: %v", err)
	}
	if err := child.EnsureStateDirs(false); err != nil {
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
	expected := filepath.Join(parent.CurrentStateDir(), "runs", "nested_run")
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
	if err := p.EnsureStateDirs(true); err != nil {
		t.Fatalf("EnsureStateDirs failed: %v", err)
	}

	// Verify files moved
	expectedMigrations := map[string]string{
		filepath.Join(stateDir, "history.jsonl"):       p.HistoryPath(),
		filepath.Join(stateDir, "memory.json"):         p.MemoryPath(),
		filepath.Join(stateDir, "tasks.json"):          p.TasksPath(),
		filepath.Join(stateDir, "todos.json"):          p.TodosPath(),
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
