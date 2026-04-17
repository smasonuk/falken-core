package host

import (
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/smasonuk/falken-core/internal/permissions"
)

var IgnoreDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"__pycache__": true, "dist": true, "build": true,
	".falken": true, ".venv": true, "venv": true,
}

const StubContent = "# [FALKEN: CONTENT REDACTED FOR SECURITY]\n"

func CreateSnapshot(srcRoot, sessionID string, blockedFiles []string) (string, error) {
	// Temporarily move the sandbox to the system temp directory to avoid .gitignore bugs
	tmpDir := filepath.Join(os.TempDir(), "falken_sandbox_"+sessionID)
	os.RemoveAll(tmpDir) // Clean slate

	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return "", err
	}

	// First, try fast native clone for supported OS
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		err := fastClone(srcRoot, tmpDir)
		if err == nil {
			// Fast clone succeeded, but we copied ignored dirs and secrets. We must clean/stub them.
			cleanIgnoredDirs(tmpDir)
			stubBlockedFiles(tmpDir, blockedFiles)
			return tmpDir, nil
		}
		// If it fails (e.g., file system doesn't support CoW or recursion error), clean up before fallback
		os.RemoveAll(tmpDir)
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			return "", err
		}
	}

	// Graceful Fallback: Standard recursive copy skipping ignored dirs and stubbing secrets
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(srcRoot, path)
		if relPath == "." {
			return nil
		}

		if d.IsDir() && IgnoreDirs[d.Name()] {
			return filepath.SkipDir
		}

		destPath := filepath.Join(tmpDir, relPath)
		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		// Stubbing
		if IsBlocked(relPath, blockedFiles) {
			return os.WriteFile(destPath, []byte(StubContent), 0644)
		}

		return CopyFile(path, destPath)
	})

	return tmpDir, err
}

func IsBlocked(relPath string, blockedFiles []string) bool {
	for _, blocked := range blockedFiles {
		if permissions.MatchPattern(blocked, relPath) {
			return true
		}
	}
	for _, blocked := range permissions.DefaultBlockedFiles {
		if permissions.MatchPattern(blocked, relPath) {
			return true
		}
	}
	return false
}

func fastClone(src, dest string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("cp", "-Rc", src+"/.", dest+"/")
	} else {
		cmd = exec.Command("cp", "-a", "--reflink=auto", src+"/.", dest+"/")
	}
	return cmd.Run()
}

func cleanIgnoredDirs(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if IgnoreDirs[d.Name()] {
			_ = os.RemoveAll(path)
			return filepath.SkipDir
		}
		return nil
	})
}

func stubBlockedFiles(tmpDir string, blockedFiles []string) {
	_ = filepath.WalkDir(tmpDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(tmpDir, path)
		if IsBlocked(relPath, blockedFiles) {
			_ = os.WriteFile(path, []byte(StubContent), 0644)
		}
		return nil
	})
}

func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Get original file permissions
	info, err := in.Stat()
	if err != nil {
		return err
	}

	// Create with the exact same permissions (preserves +x executable bit)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
