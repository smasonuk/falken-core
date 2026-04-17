package host

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateSnapshot(t *testing.T) {
	// Create a temporary project root
	tmpDir := t.TempDir()
	srcRoot, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("failed to eval symlinks: %v", err)
	}

	// Create some files
	files := map[string]string{
		"file1.txt":           "content1",
		"dir1/file2.txt":      "content2",
		"secret.txt":          "secret content",
		".git/config":         "git config",
		"node_modules/pkg/js": "node content",
		"src/vendor/mod.txt":  "vendor content",
		"deep/.falken/log":    "internal state",
	}

	for path, content := range files {
		fullPath := filepath.Join(srcRoot, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}
	}

	sessionID := "test-session"
	blockedFiles := []string{"secret.txt"}

	sandboxDir, err := CreateSnapshot(srcRoot, sessionID, blockedFiles)
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}
	defer os.RemoveAll(sandboxDir)

	// Verify sandbox location
	expectedSandboxDir := filepath.Join(os.TempDir(), "falken_sandbox_"+sessionID)
	if sandboxDir != expectedSandboxDir {
		t.Errorf("expected sandbox dir %s, got %s", expectedSandboxDir, sandboxDir)
	}

	// Verify files are copied
	verifyFile(t, sandboxDir, "file1.txt", "content1")
	verifyFile(t, sandboxDir, "dir1/file2.txt", "content2")

	// Verify blocked file is stubbed
	verifyFile(t, sandboxDir, "secret.txt", StubContent)

	// Verify ignored dirs are NOT present in sandbox
	ignoredDirs := []string{".git", "node_modules"}
	for _, dir := range ignoredDirs {
		path := filepath.Join(sandboxDir, dir)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected ignored dir %s to not exist in sandbox", dir)
		}
	}

	nestedIgnored := []string{
		filepath.Join("src", "vendor"),
		filepath.Join("deep", ".falken"),
	}
	for _, rel := range nestedIgnored {
		path := filepath.Join(sandboxDir, rel)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected nested ignored dir %s to not exist in sandbox", rel)
		}
	}

	// Verify .falken itself is not in the sandbox (it should be cleaned if fastClone copied it)
	falkenPath := filepath.Join(sandboxDir, ".falken")
	if _, err := os.Stat(falkenPath); !os.IsNotExist(err) {
		t.Errorf("expected .falken to not exist in sandbox")
	}
}

func verifyFile(t *testing.T, base, relPath, expectedContent string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(base, relPath))
	if err != nil {
		t.Errorf("failed to read %s: %v", relPath, err)
		return
	}
	if string(content) != expectedContent {
		t.Errorf("expected %s content %q, got %q", relPath, expectedContent, string(content))
	}
}
