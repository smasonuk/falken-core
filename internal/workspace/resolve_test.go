package workspace_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/smasonuk/falken-core/internal/workspace"
)

func TestResolveExistingRelativeInsideWorkspaceSucceeds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	target := filepath.Join(root, "dir", "file.txt")
	mkdirAll(t, filepath.Dir(target))
	writeFile(t, target, "ok")

	got, err := workspace.ResolveExisting(root, "", filepath.Join("dir", "file.txt"))
	if err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}

	want := realPath(t, target)
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestResolveExistingFromNestedCwdSucceeds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	target := filepath.Join(root, "nested", "target.txt")
	mkdirAll(t, filepath.Join(root, "nested", "child"))
	writeFile(t, target, "ok")

	got, err := workspace.ResolveExisting(root, filepath.Join("nested", "child"), ".."+string(filepath.Separator)+"target.txt")
	if err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}

	want := realPath(t, target)
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestResolveForCreateAllowsMissingLeafInsideWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	mkdirAll(t, filepath.Join(root, "dir"))

	got, err := workspace.ResolveForCreate(root, "", filepath.Join("dir", "new.txt"))
	if err != nil {
		t.Fatalf("ResolveForCreate: %v", err)
	}

	want := filepath.Join(realPath(t, filepath.Join(root, "dir")), "new.txt")
	if got != want {
		t.Fatalf("resolved create path = %q, want %q", got, want)
	}
}

func TestResolveForCreateRejectsLexicalEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	mkdirAll(t, root)

	_, err := workspace.ResolveForCreate(root, "", filepath.Join("..", "outside.txt"))
	if !errors.Is(err, workspace.ErrPathOutsideWorkspace) {
		t.Fatalf("expected ErrPathOutsideWorkspace, got %v", err)
	}
}

func TestResolveExistingRejectsSymlinkEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	outsideRoot := filepath.Join(t.TempDir(), "outside")
	mkdirAll(t, root)
	mkdirAll(t, outsideRoot)
	writeFile(t, filepath.Join(outsideRoot, "secret.txt"), "secret")

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outsideRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := workspace.ResolveExisting(root, "", filepath.Join("escape", "secret.txt"))
	if !errors.Is(err, workspace.ErrPathOutsideWorkspace) {
		t.Fatalf("expected ErrPathOutsideWorkspace, got %v", err)
	}
}

func TestResolveForCreateRejectsSymlinkParentEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	outsideRoot := filepath.Join(t.TempDir(), "outside")
	mkdirAll(t, root)
	mkdirAll(t, outsideRoot)

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outsideRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := workspace.ResolveForCreate(root, "", filepath.Join("escape", "new.txt"))
	if !errors.Is(err, workspace.ErrPathOutsideWorkspace) {
		t.Fatalf("expected ErrPathOutsideWorkspace, got %v", err)
	}
}

func TestResolveExistingAbsoluteInsideWorkspaceSucceeds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	target := filepath.Join(root, "inside.txt")
	mkdirAll(t, root)
	writeFile(t, target, "ok")

	got, err := workspace.ResolveExisting(root, "", target)
	if err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}

	want := realPath(t, target)
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestResolveExistingSandboxMountPathSucceeds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	target := filepath.Join(root, "dir", "file.txt")
	mkdirAll(t, filepath.Dir(target))
	writeFile(t, target, "ok")

	tests := []struct {
		name string
		cwd  string
		path string
		want string
	}{
		{name: "working dir mount root", cwd: "/workspace", path: "dir/file.txt", want: target},
		{name: "path mount root", cwd: ".", path: "/workspace/dir/file.txt", want: target},
		{name: "working dir mount child", cwd: "/workspace/dir", path: "file.txt", want: target},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workspace.ResolveExisting(root, tt.cwd, tt.path)
			if err != nil {
				t.Fatalf("ResolveExisting: %v", err)
			}
			if got != realPath(t, tt.want) {
				t.Fatalf("resolved path = %q, want %q", got, realPath(t, tt.want))
			}
		})
	}
}

func TestResolveExistingWithConfiguredSandboxMountPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	target := filepath.Join(root, "sub", "file.txt")
	mkdirAll(t, filepath.Dir(target))
	writeFile(t, target, "ok")

	tests := []struct {
		name string
		cwd  string
		path string
		want string
	}{
		{name: "mount root path", cwd: ".", path: "/repo/sub/file.txt", want: target},
		{name: "mount child cwd", cwd: "/repo/sub", path: "file.txt", want: target},
		{name: "relative path", cwd: ".", path: "sub/file.txt", want: target},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workspace.ResolveExistingWithSandboxMount(root, tt.cwd, tt.path, "/repo")
			if err != nil {
				t.Fatalf("ResolveExistingWithSandboxMount: %v", err)
			}
			if got != realPath(t, tt.want) {
				t.Fatalf("resolved path = %q, want %q", got, realPath(t, tt.want))
			}
		})
	}

	if _, err := workspace.ResolveExistingWithSandboxMount(root, "", "/repo2/file.txt", "/repo"); !errors.Is(err, workspace.ErrPathOutsideWorkspace) {
		t.Fatalf("/repo2 error = %v, want ErrPathOutsideWorkspace", err)
	}
	if _, err := workspace.ResolveExistingWithSandboxMount(root, "", "/etc/passwd", "/repo"); !errors.Is(err, workspace.ErrPathOutsideWorkspace) {
		t.Fatalf("/etc/passwd error = %v, want ErrPathOutsideWorkspace", err)
	}
}

func TestResolveExistingAbsoluteOutsideWorkspaceFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	mkdirAll(t, root)
	writeFile(t, outside, "nope")

	_, err := workspace.ResolveExisting(root, "", outside)
	if !errors.Is(err, workspace.ErrPathOutsideWorkspace) {
		t.Fatalf("expected ErrPathOutsideWorkspace, got %v", err)
	}
}

func TestResolveForCreateRejectsUNCPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	mkdirAll(t, root)

	_, err := workspace.ResolveForCreate(root, "", "//server/share/file.txt")
	if !errors.Is(err, workspace.ErrUNCPath) {
		t.Fatalf("expected ErrUNCPath, got %v", err)
	}
}

func TestIsInsideWorkspaceBoundaries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	mkdirAll(t, filepath.Join(root, "child"))

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "root", path: root, want: true},
		{name: "child", path: filepath.Join(root, "child"), want: true},
		{name: "sibling", path: filepath.Join(filepath.Dir(root), "sibling"), want: false},
		{name: "parent", path: filepath.Dir(root), want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := workspace.IsInside(root, tc.path)
			if err != nil {
				t.Fatalf("IsInside: %v", err)
			}
			if got != tc.want {
				t.Fatalf("IsInside(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsInsideRejectsSymlinkEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	outsideRoot := filepath.Join(t.TempDir(), "outside")
	mkdirAll(t, root)
	mkdirAll(t, outsideRoot)

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outsideRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	inside, err := workspace.IsInside(root, link)
	if err != nil {
		t.Fatalf("IsInside: %v", err)
	}
	if inside {
		t.Fatalf("expected symlink escape path %q to be outside the workspace", link)
	}
}

func TestIsInsideRejectsMissingPathUnderSymlinkEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	outsideRoot := filepath.Join(t.TempDir(), "outside")
	mkdirAll(t, root)
	mkdirAll(t, outsideRoot)

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outsideRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	inside, err := workspace.IsInside(root, filepath.Join(link, "new.txt"))
	if err != nil {
		t.Fatalf("IsInside: %v", err)
	}
	if inside {
		t.Fatalf("expected missing path under symlink escape to be outside the workspace")
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func realPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("eval symlinks %q: %v", path, err)
	}

	return filepath.Clean(resolved)
}
