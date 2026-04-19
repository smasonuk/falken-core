package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smasonuk/falken-core/internal/runtimeapi"
)

func TestPrepareRuntimeState(t *testing.T) {
	workspace := t.TempDir()
	stateDir := filepath.Join(workspace, ".falken")
	paths, _ := runtimeapi.NewPaths(workspace, stateDir)

	// Pre-create some files
	backupFile := filepath.Join(paths.BackupDir(), "test.bak")
	os.MkdirAll(filepath.Dir(backupFile), 0755)
	os.WriteFile(backupFile, []byte("backup"), 0644)

	historyFile := paths.HistoryPath()
	os.MkdirAll(filepath.Dir(historyFile), 0755)
	os.WriteFile(historyFile, []byte("history"), 0644)

	if err := PrepareRuntimeState(paths); err != nil {
		t.Fatalf("PrepareRuntimeState failed: %v", err)
	}

	// Assert backups preserved
	if _, err := os.Stat(backupFile); err != nil {
		t.Errorf("expected backup file to be preserved, got %v", err)
	}

	// Assert history preserved (non-destructive)
	if _, err := os.Stat(historyFile); err != nil {
		t.Errorf("expected history file to be preserved, got %v", err)
	}
}
