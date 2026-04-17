package runtimeapi

import (
	"os"
	"path/filepath"
	"strings"
)

// Paths holds the resolved workspace and state directory roots for a session.
type Paths struct {
	WorkspaceDir string
	StateDir     string
}

// NewPaths resolves absolute workspace and state directories.
// When stateDir is empty it defaults to a `.falken` directory under workspaceDir.
func NewPaths(workspaceDir, stateDir string) (Paths, error) {
	if workspaceDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Paths{}, err
		}
		workspaceDir = cwd
	}

	workspaceDir, err := filepath.Abs(workspaceDir)
	if err != nil {
		return Paths{}, err
	}

	if stateDir == "" {
		stateDir = filepath.Join(workspaceDir, ".falken")
	}

	stateDir, err = filepath.Abs(stateDir)
	if err != nil {
		return Paths{}, err
	}

	return Paths{
		WorkspaceDir: workspaceDir,
		StateDir:     stateDir,
	}, nil
}

// EnsureStateDirs creates the directory structure expected by runtime subsystems.
func (p Paths) EnsureStateDirs() error {
	dirs := []string{
		p.StateDir,
		p.CacheDir(),
		p.TruncationDir(),
		p.BackupDir(),
		p.PluginStateDir(),
		p.TasksDir(),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

// HistoryPath returns the persisted conversation history path.
func (p Paths) HistoryPath() string {
	return filepath.Join(p.StateDir, "history.jsonl")
}

// MemoryPath returns the path used for persisted agent memory.
func (p Paths) MemoryPath() string {
	return filepath.Join(p.StateDir, "memory.json")
}

// TasksPath returns the path used for the task index file.
func (p Paths) TasksPath() string {
	return filepath.Join(p.StateDir, "tasks.json")
}

// TasksDir returns the directory that stores task-specific artifacts.
func (p Paths) TasksDir() string {
	return filepath.Join(p.StateDir, "tasks")
}

// TodosPath returns the path used for persisted todo items.
func (p Paths) TodosPath() string {
	return filepath.Join(p.StateDir, "todos.json")
}

// CacheDir returns the root cache directory for transient runtime files.
func (p Paths) CacheDir() string {
	return filepath.Join(p.StateDir, "cache")
}

// TruncationDir returns the directory used for truncated content snapshots.
func (p Paths) TruncationDir() string {
	return filepath.Join(p.StateDir, "truncations")
}

// BackupDir returns the directory used for file backup snapshots.
func (p Paths) BackupDir() string {
	return filepath.Join(p.StateDir, "backups")
}

// PluginStateDir returns the directory used for per-plugin persisted state.
func (p Paths) PluginStateDir() string {
	return filepath.Join(p.StateDir, "plugin_states")
}

// ProxyCertPath returns the path where the sandbox proxy CA certificate is written.
func (p Paths) ProxyCertPath() string {
	return filepath.Join(p.CacheDir(), "proxy-ca.crt")
}

// DebugLogPath returns the path used for runtime debug logging.
func (p Paths) DebugLogPath() string {
	return filepath.Join(p.StateDir, "debug.log")
}

// SubRunPaths returns isolated state paths for a child run that shares the same workspace.
func (p Paths) SubRunPaths(runID string) Paths {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		runID = "subrun"
	}
	runID = strings.ReplaceAll(runID, string(filepath.Separator), "_")
	return Paths{
		WorkspaceDir: p.WorkspaceDir,
		StateDir:     filepath.Join(p.StateDir, "runs", runID),
	}
}
