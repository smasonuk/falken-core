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
func (p Paths) EnsureStateDirs(migrate bool) error {
	// Create base directories first
	dirs := []string{
		p.StateDir,
		p.CacheDir(),
		p.BackupDir(),
		p.CurrentStateDir(),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	if migrate {
		// Migrate legacy paths to .falken/state/current/
		legacyMap := map[string]string{
			filepath.Join(p.StateDir, "history.jsonl"): p.HistoryPath(),
			filepath.Join(p.StateDir, "memory.json"):  p.MemoryPath(),
			filepath.Join(p.StateDir, "tasks.json"):   p.TasksPath(),
			filepath.Join(p.StateDir, "tasks"):        p.TasksDir(),
			filepath.Join(p.StateDir, "todos.json"):   p.TodosPath(),
		}

		for legacy, current := range legacyMap {
			if _, err := os.Stat(legacy); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return err
			}

			// If destination already exists, skip migration for this file/dir.
			if _, err := os.Stat(current); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return err
			}

			// Perform migration.
			if err := os.Rename(legacy, current); err != nil {
				return err
			}
		}
	}

	// Ensure subdirectories are created if they didn't exist or migrate
	subDirs := []string{
		p.TasksDir(),
		p.PluginStateDir(),
		p.RunsDir(),
		p.TruncationDir(),
	}
	for _, dir := range subDirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	return nil
}

// ResetCurrentState removes the current session's persisted state directory.
func (p Paths) ResetCurrentState() error {
	return os.RemoveAll(p.CurrentStateDir())
}

// CurrentStateDir returns the directory used for the current session's persisted state.
func (p Paths) CurrentStateDir() string {
	clean := filepath.Clean(p.StateDir)
	slash := filepath.ToSlash(clean)

	if filepath.Base(clean) == "current" && filepath.Base(filepath.Dir(clean)) == "state" {
		return clean
	}

	if strings.Contains(slash, "/state/current/runs/") {
		return clean
	}

	return filepath.Join(clean, "state", "current")
}

// HistoryPath returns the persisted conversation history path.
func (p Paths) HistoryPath() string {
	return filepath.Join(p.CurrentStateDir(), "history.jsonl")
}

// MemoryPath returns the path used for persisted agent memory.
func (p Paths) MemoryPath() string {
	return filepath.Join(p.CurrentStateDir(), "memory.json")
}

// PlanPath returns the internal runtime plan path for this runner.
// Plans are runtime state, not workspace artifacts, and should not be written
// through workspace file tools.
func (p Paths) PlanPath() string {
	return filepath.Join(p.CurrentStateDir(), "plan.md")
}

// TasksPath returns the path used for the task index file.
func (p Paths) TasksPath() string {
	return filepath.Join(p.CurrentStateDir(), "tasks.json")
}

// TasksDir returns the directory that stores task-specific artifacts.
func (p Paths) TasksDir() string {
	return filepath.Join(p.CurrentStateDir(), "tasks")
}

// TodosPath returns the path used for persisted todo items.
func (p Paths) TodosPath() string {
	return filepath.Join(p.CurrentStateDir(), "todos.json")
}

// CacheDir returns the root cache directory for transient runtime files.
func (p Paths) CacheDir() string {
	return filepath.Join(p.StateDir, "cache")
}

// TruncationDir returns the directory used for truncated content snapshots.
func (p Paths) TruncationDir() string {
	return filepath.Join(p.CurrentStateDir(), "truncations")
}

// BackupDir returns the directory used for file backup snapshots.
func (p Paths) BackupDir() string {
	return filepath.Join(p.StateDir, "backups")
}

// PluginStateDir returns the directory used for per-plugin persisted state.
func (p Paths) PluginStateDir() string {
	return filepath.Join(p.CurrentStateDir(), "plugin_states")
}

// RunsDir returns the directory used for child runs.
func (p Paths) RunsDir() string {
	return filepath.Join(p.CurrentStateDir(), "runs")
}

// MountedCachesDir returns the directory used for configured cache mounts.
func (p Paths) MountedCachesDir() string {
	return filepath.Join(p.StateDir, "caches")
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
	runID = strings.ReplaceAll(runID, "/", "_")

	return Paths{
		WorkspaceDir: p.WorkspaceDir,
		StateDir:     filepath.Join(p.RunsDir(), runID),
	}
}
