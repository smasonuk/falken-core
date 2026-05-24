package falken_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/smasonuk/falken-core/pkg/falken"
)

func TestNewPathsNormalizesWorkspaceRoot(t *testing.T) {
	setStateHomeEnv(t)

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	relative, err := filepath.Rel(cwd, filepath.Join(workspace, ".", "nested", ".."))
	if err != nil {
		t.Fatalf("relative path: %v", err)
	}

	paths, err := falken.NewPaths(relative, "")
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	want, err := filepath.Abs(workspace)
	if err != nil {
		t.Fatalf("abs workspace: %v", err)
	}

	if paths.WorkspaceRoot != filepath.Clean(want) {
		t.Fatalf("workspace root = %q, want %q", paths.WorkspaceRoot, filepath.Clean(want))
	}
}

func TestDerivedStateRootIsDeterministic(t *testing.T) {
	setStateHomeEnv(t)

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	first, err := falken.NewPaths(workspace, "")
	if err != nil {
		t.Fatalf("first NewPaths: %v", err)
	}

	second, err := falken.NewPaths(workspace, "")
	if err != nil {
		t.Fatalf("second NewPaths: %v", err)
	}

	if first.StateRoot != second.StateRoot {
		t.Fatalf("state root mismatch: %q != %q", first.StateRoot, second.StateRoot)
	}
}

func TestExplicitStateDirOverridesDerivedRoot(t *testing.T) {
	setStateHomeEnv(t)

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	explicit := filepath.Join(t.TempDir(), "state", "..", "chosen-state")
	paths, err := falken.NewPaths(workspace, explicit)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	want := filepath.Clean(explicit)
	if paths.StateRoot != want {
		t.Fatalf("state root = %q, want %q", paths.StateRoot, want)
	}
}

func TestLayoutSubpathsAreStableAndConsistent(t *testing.T) {
	setStateHomeEnv(t)

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	paths, err := falken.NewPaths(workspace, "")
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	assertPath := func(name, got, want string) {
		t.Helper()
		if got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("%s is not absolute: %q", name, got)
		}
		if filepath.Clean(got) != got {
			t.Fatalf("%s is not clean: %q", name, got)
		}
	}

	assertPath("workspace root", paths.WorkspaceRoot, filepath.Clean(workspace))
	assertPath("state root", paths.StateRoot, filepath.Clean(paths.StateRoot))
	assertPath("metadata path", paths.MetadataPath, filepath.Join(paths.StateRoot, "metadata.json"))
	assertPath("current conversation root", paths.CurrentConversationRoot, filepath.Join(paths.StateRoot, "conversations", "current"))
	assertPath("history path", paths.HistoryPath, filepath.Join(paths.CurrentConversationRoot, "history.json"))
	assertPath("memory path", paths.MemoryPath, filepath.Join(paths.CurrentConversationRoot, "memory.json"))
	assertPath("todos path", paths.TodosPath, filepath.Join(paths.CurrentConversationRoot, "todos.json"))
	assertPath("plan path", paths.PlanPath, filepath.Join(paths.CurrentConversationRoot, "plan.json"))
	assertPath("command evidence path", paths.CommandEvidencePath, filepath.Join(paths.CurrentConversationRoot, "command_evidence.json"))
	assertPath("legacy verification path", paths.VerificationPath, filepath.Join(paths.CurrentConversationRoot, "verification.json"))
	assertPath("cache root", paths.CacheRoot, filepath.Join(paths.StateRoot, "cache"))
	assertPath("backup root", paths.BackupRoot, filepath.Join(paths.StateRoot, "backups"))
	assertPath("truncation root", paths.TruncationRoot, filepath.Join(paths.StateRoot, "truncations"))
	assertPath("recent truncation root", paths.RecentTruncationRoot, filepath.Join(paths.TruncationRoot, "recent"))
	assertPath("artifact root", paths.ArtifactRoot, filepath.Join(paths.StateRoot, "artifacts"))
	assertPath("recent artifact root", paths.RecentArtifactRoot, filepath.Join(paths.ArtifactRoot, "recent"))
	assertPath("plugin state root", paths.PluginStateRoot, filepath.Join(paths.StateRoot, "plugins"))
	assertPath("project permissions path", paths.ProjectPermissionsPath, filepath.Join(paths.StateRoot, "project_approvals.json"))
}

func TestPathDerivationIgnoresLegacyWorkspaceContents(t *testing.T) {
	setStateHomeEnv(t)

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	before, err := falken.NewPaths(workspace, "")
	if err != nil {
		t.Fatalf("NewPaths before legacy marker: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(workspace, ".falken"), 0o755); err != nil {
		t.Fatalf("mkdir legacy marker: %v", err)
	}

	after, err := falken.NewPaths(workspace, "")
	if err != nil {
		t.Fatalf("NewPaths after legacy marker: %v", err)
	}

	if before.StateRoot != after.StateRoot {
		t.Fatalf("state root changed after adding legacy marker: %q != %q", before.StateRoot, after.StateRoot)
	}
}

func TestLayoutVersionIsExposed(t *testing.T) {
	setStateHomeEnv(t)

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	paths, err := falken.NewPaths(workspace, "")
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	if paths.LayoutVersion != falken.LayoutVersion {
		t.Fatalf("layout version = %d, want %d", paths.LayoutVersion, falken.LayoutVersion)
	}

	if paths.LayoutVersion <= 0 {
		t.Fatalf("layout version must be positive, got %d", paths.LayoutVersion)
	}
}

func setStateHomeEnv(t *testing.T) {
	t.Helper()

	stateHome := filepath.Join(t.TempDir(), "state-home")
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", stateHome)
		t.Setenv("XDG_STATE_HOME", "")
		return
	}

	t.Setenv("XDG_STATE_HOME", stateHome)
}
