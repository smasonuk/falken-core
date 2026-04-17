package host

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

type ApplyChangesResult struct {
	GuardrailTriggered bool
	SkippedFiles       []string
}

func GenerateDiff(realCWD, sandboxCWD string, blockedFiles []string) (string, error) {
	cmd := exec.Command("git", "diff", "--no-index", "-M", realCWD, sandboxCWD)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// Normal diff output
		} else {
			return "", fmt.Errorf("git diff failed: %v, output: %s", err, string(out))
		}
	}

	diffStr := string(out)

	// Git --no-index strips the leading slash for absolute paths.
	// We format our prefixes exactly how Git formats them to ensure clean replacement.
	realRel := strings.TrimPrefix(filepath.ToSlash(realCWD), "/") + "/"
	sandboxRel := strings.TrimPrefix(filepath.ToSlash(sandboxCWD), "/") + "/"

	// This safely turns "a/Volumes/.../main.go" into "a/main.go"
	diffStr = strings.ReplaceAll(diffStr, sandboxRel, "")
	diffStr = strings.ReplaceAll(diffStr, realRel, "")

	// Filter out ignored directories and stubbed secret files
	diffStr = FilterDiff(diffStr, blockedFiles)

	return diffStr, nil
}

func FilterDiff(diffStr string, blockedFiles []string) string {
	var filtered strings.Builder
	lines := strings.Split(diffStr, "\n")

	var currentChunk []string
	skipChunk := false

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			if len(currentChunk) > 0 && !skipChunk {
				filtered.WriteString(strings.Join(currentChunk, "\n") + "\n")
			}

			currentChunk = []string{line}
			skipChunk = false

			parts := strings.Split(line, " ")
			if len(parts) >= 4 {
				fileA := strings.TrimPrefix(parts[2], "a/")
				fileB := strings.TrimPrefix(parts[3], "b/")

				if shouldIgnore(fileA, blockedFiles) || shouldIgnore(fileB, blockedFiles) {
					skipChunk = true
				}
			}
		} else {
			currentChunk = append(currentChunk, line)
		}
	}

	if len(currentChunk) > 0 && !skipChunk {
		filtered.WriteString(strings.Join(currentChunk, "\n") + "\n")
	}

	return filtered.String()
}

func shouldIgnore(path string, blockedFiles []string) bool {
	if IsBlocked(path, blockedFiles) {
		return true
	}

	// We've added the agent's local caches to the ignore list so they don't flood the UI
	ignored := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"__pycache__": true, "dist": true, "build": true,
		".falken": true, ".venv": true, "venv": true,
		".gomodcache": true, ".gocache": true,
	}

	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		if ignored[part] {
			return true
		}
	}
	return false
}

func ParseDiffFiles(diffStr string) []string {
	var files []string
	lines := strings.Split(diffStr, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			parts := strings.Split(line, " ")
			if len(parts) >= 4 {
				// parts[2] is a/path, parts[3] is b/path
				file := strings.TrimPrefix(parts[3], "b/")
				files = append(files, file)
			}
		}
	}
	return files
}

func ApplyChanges(realCWD, sandboxCWD, diffStr string, blockedFiles []string) (ApplyChangesResult, error) {
	files, _, err := gitdiff.Parse(strings.NewReader(diffStr))
	if err != nil {
		return ApplyChangesResult{}, err
	}

	result := ApplyChangesResult{}
	for _, file := range files {
		isBlockedFile := (file.OldName != "" && IsBlocked(file.OldName, blockedFiles)) ||
			(file.NewName != "" && IsBlocked(file.NewName, blockedFiles))
		if isBlockedFile {
			result.GuardrailTriggered = true
			for _, name := range []string{file.OldName, file.NewName} {
				if name == "" || slices.Contains(result.SkippedFiles, name) {
					continue
				}
				result.SkippedFiles = append(result.SkippedFiles, name)
			}
			continue
		}

		// Apply deletions/renames
		if !file.IsNew {
			oldPath := filepath.Join(realCWD, file.OldName)
			if file.IsDelete || file.OldName != file.NewName {
				_ = os.Remove(oldPath)
			}
		}

		// Apply additions/renames
		if !file.IsDelete {
			newPath := filepath.Join(realCWD, file.NewName)
			_ = os.MkdirAll(filepath.Dir(newPath), 0755)

			// Copy file from SandboxCWD to RealCWD
			srcPath := filepath.Join(sandboxCWD, file.NewName)
			err := CopyFile(srcPath, newPath)
			if err != nil {
				return result, fmt.Errorf("failed to copy %s: %v", file.NewName, err)
			}
		}
	}

	CleanupEmptyDirs(realCWD)
	return result, nil
}

func CleanupEmptyDirs(root string) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root || !d.IsDir() {
			return nil
		}

		// This will only succeed if the directory is empty.
		_ = os.Remove(path)
		return nil
	})
}
