package runtimeexec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/runtimeexec"
	statepkg "github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/workspace"
)

func TestExecutionStateInitializesWithWorkspaceRootCWD(t *testing.T) {
	root := t.TempDir()

	state, err := runtimeexec.NewExecutionState(root)
	if err != nil {
		t.Fatalf("NewExecutionState: %v", err)
	}

	want := filepath.Clean(root)
	if state.WorkspaceRoot() != want {
		t.Fatalf("workspace root = %q, want %q", state.WorkspaceRoot(), want)
	}
	if state.CurrentWorkingDir() != want {
		t.Fatalf("current working dir = %q, want %q", state.CurrentWorkingDir(), want)
	}
}

func TestExecutionStateForLayoutUsesCanonicalArtifactRoot(t *testing.T) {
	root := t.TempDir()
	stateRoot := t.TempDir()
	layout, err := statepkg.ResolveLayout(root, stateRoot)
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}

	execState, err := runtimeexec.NewExecutionStateForLayout(layout)
	if err != nil {
		t.Fatalf("NewExecutionStateForLayout: %v", err)
	}

	if execState.OutputArtifactRoot() != layout.RecentArtifactRoot {
		t.Fatalf("output artifact root = %q, want %q", execState.OutputArtifactRoot(), layout.RecentArtifactRoot)
	}
}

func TestVirtualExecutionStateForLayoutDoesNotStatWorkspaceRoot(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "does-not-exist")
	layout, err := statepkg.ResolveLayout(missingRoot, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}

	execState, err := runtimeexec.NewVirtualExecutionStateForLayout(layout)
	if err != nil {
		t.Fatalf("NewVirtualExecutionStateForLayout: %v", err)
	}
	if !execState.VirtualWorkspace() {
		t.Fatal("VirtualWorkspace = false, want true")
	}
	if execState.CurrentWorkingDir() != filepath.Clean(missingRoot) {
		t.Fatalf("current working dir = %q, want virtual workspace root %q", execState.CurrentWorkingDir(), missingRoot)
	}
	if execState.OutputArtifactRoot() != layout.RecentArtifactRoot {
		t.Fatalf("output artifact root = %q, want %q", execState.OutputArtifactRoot(), layout.RecentArtifactRoot)
	}
}

func TestVirtualExecutionStateWorkingDirIsLexicallyConstrained(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-workspace")
	state, err := runtimeexec.NewVirtualExecutionState(root)
	if err != nil {
		t.Fatalf("NewVirtualExecutionState: %v", err)
	}
	state.SetSandboxMountPath("/workspace")

	got, err := state.ResolveWorkingDir("nested/dir")
	if err != nil {
		t.Fatalf("ResolveWorkingDir relative: %v", err)
	}
	if want := filepath.Join(root, "nested", "dir"); got != want {
		t.Fatalf("relative working dir = %q, want %q", got, want)
	}

	got, err = state.ResolveWorkingDir("/workspace/nested")
	if err != nil {
		t.Fatalf("ResolveWorkingDir sandbox path: %v", err)
	}
	if want := filepath.Join(root, "nested"); got != want {
		t.Fatalf("sandbox working dir = %q, want %q", got, want)
	}

	if _, err := state.ResolveWorkingDir("../outside"); err == nil {
		t.Fatal("expected escaping relative working dir to fail")
	} else if !strings.Contains(err.Error(), workspace.ErrPathOutsideWorkspace.Error()) {
		t.Fatalf("escape error = %v, want outside workspace", err)
	}
}

func TestVirtualExecutionStateWorkspacePathIsLexicallyConstrained(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-workspace")
	state, err := runtimeexec.NewVirtualExecutionState(root)
	if err != nil {
		t.Fatalf("NewVirtualExecutionState: %v", err)
	}
	state.SetSandboxMountPath("/workspace")

	for _, tt := range []struct {
		name string
		path string
		want string
	}{
		{name: "relative", path: "src/main.go", want: filepath.Join(root, "src", "main.go")},
		{name: "sandbox absolute", path: "/workspace/src/main.go", want: filepath.Join(root, "src", "main.go")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := state.ResolveWorkspacePath(tt.path, false)
			if err != nil {
				t.Fatalf("ResolveWorkspacePath: %v", err)
			}
			if got != tt.want {
				t.Fatalf("path = %q, want %q", got, tt.want)
			}
		})
	}

	if _, err := state.ResolveWorkspacePath("../outside.txt", false); err == nil {
		t.Fatal("expected escaping relative path to fail")
	} else if !strings.Contains(err.Error(), workspace.ErrPathOutsideWorkspace.Error()) {
		t.Fatalf("escape error = %v, want outside workspace", err)
	}
	if _, err := state.ResolveWorkspacePath("/etc/passwd", false); err == nil {
		t.Fatal("expected absolute outside path to fail")
	} else if !strings.Contains(err.Error(), workspace.ErrPathOutsideWorkspace.Error()) {
		t.Fatalf("absolute outside error = %v, want outside workspace", err)
	}
}

func TestExecutionStateWorkingDirStaysInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "nested")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	state, err := runtimeexec.NewExecutionState(root)
	if err != nil {
		t.Fatalf("NewExecutionState: %v", err)
	}

	if err := state.SetCurrentWorkingDir("nested"); err != nil {
		t.Fatalf("SetCurrentWorkingDir nested: %v", err)
	}
	wantSubdir := realPath(t, subdir)
	if state.CurrentWorkingDir() != wantSubdir {
		t.Fatalf("current working dir = %q, want %q", state.CurrentWorkingDir(), wantSubdir)
	}

	if err := state.SetCurrentWorkingDir("../.."); err == nil {
		t.Fatal("expected outside workspace cwd to fail")
	} else if !strings.Contains(err.Error(), workspace.ErrPathOutsideWorkspace.Error()) {
		t.Fatalf("expected outside workspace error, got %v", err)
	}
}

func realPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks %q: %v", path, err)
	}
	return filepath.Clean(resolved)
}

func TestExecutionStateEnvironmentOverridesMergePredictably(t *testing.T) {
	root := t.TempDir()
	state, err := runtimeexec.NewExecutionState(root)
	if err != nil {
		t.Fatalf("NewExecutionState: %v", err)
	}

	state.SetEnv("FALKEN_SHARED", "session")
	state.SetEnv("FALKEN_SESSION_ONLY", "yes")

	merged := state.MergedEnvironment(map[string]string{
		"FALKEN_SHARED":       "request",
		"FALKEN_REQUEST_ONLY": "yes",
	})
	values := envMap(merged)

	if values["FALKEN_SHARED"] != "request" {
		t.Fatalf("request env should override session env, got %q", values["FALKEN_SHARED"])
	}
	if values["FALKEN_SESSION_ONLY"] != "yes" {
		t.Fatalf("expected session env override in merged env, got %q", values["FALKEN_SESSION_ONLY"])
	}
	if values["FALKEN_REQUEST_ONLY"] != "yes" {
		t.Fatalf("expected request env override in merged env, got %q", values["FALKEN_REQUEST_ONLY"])
	}

	for i := 1; i < len(merged); i++ {
		if merged[i-1] > merged[i] {
			t.Fatalf("merged env is not sorted at %q > %q", merged[i-1], merged[i])
		}
	}
}

func envMap(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
