package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/permissions"
)

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func TestPathResolver_AllowsParentPathInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	subdir := filepath.Join(workspace, "src", "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	shell := NewStatefulShell(workspace, t.TempDir(), permissions.NewManager(nil, nil), nil)
	shell.RealCWD = subdir

	got, err := shell.resolvePath("../README.md")
	if err != nil {
		t.Fatalf("resolvePath returned error: %v", err)
	}
	want := filepath.Join(canonicalPath(t, filepath.Join(workspace, "src")), "README.md")
	if got != want {
		t.Fatalf("resolvePath() = %q, want %q", got, want)
	}
}

func TestPathResolver_RejectsWorkspaceEscape(t *testing.T) {
	workspace := t.TempDir()
	subdir := filepath.Join(workspace, "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	shell := NewStatefulShell(workspace, t.TempDir(), permissions.NewManager(nil, nil), nil)
	shell.RealCWD = subdir

	if _, err := shell.resolvePath("../../outside.txt"); err == nil {
		t.Fatal("expected workspace escape to be rejected")
	}
}

func TestPathResolver_RejectsSymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	linkPath := filepath.Join(workspace, "link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	shell := NewStatefulShell(workspace, t.TempDir(), permissions.NewManager(nil, nil), nil)
	shell.RealCWD = workspace

	if _, err := shell.resolvePath("link/secret.txt"); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestStatefulShell_ExecuteRespectsCallerCancellation(t *testing.T) {
	pm := permissions.NewManager(nil, nil)
	shell := newTestShell(t, pm)
	shell.TestingMode = true

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := shell.Execute(ctx, "test", "sleep 5", "test", []string{"sleep"}, false, nil)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if ctx.Err() == nil {
		t.Fatalf("expected caller context to be canceled, got err=%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("expected command to stop promptly after cancellation")
	}
}
