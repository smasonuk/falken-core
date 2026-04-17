package workspacepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Resolve(workspaceRoot, currentCWD, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	base := currentCWD
	if base == "" {
		base = workspaceRoot
	}
	if workspaceRoot == "" {
		workspaceRoot = base
	}
	if base == "" || workspaceRoot == "" {
		if filepath.IsAbs(path) {
			return filepath.Clean(path), nil
		}
		return filepath.Clean(path), nil
	}

	resolved := path
	if !filepath.IsAbs(path) {
		resolved = filepath.Join(base, path)
	}
	resolved = filepath.Clean(resolved)

	evalRoot, err := evalSymlinksWithMissingLeaves(workspaceRoot)
	if err != nil {
		return "", err
	}
	evalResolved, err := evalSymlinksWithMissingLeaves(resolved)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(evalRoot, evalResolved)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes workspace root: %s", path)
	}

	return evalResolved, nil
}

func evalSymlinksWithMissingLeaves(path string) (string, error) {
	path = filepath.Clean(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	var missing []string
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
