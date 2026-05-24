package workspace

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const defaultSandboxMountPath = "/workspace"

var (
	// ErrPathRequired indicates the caller did not provide a path to resolve.
	ErrPathRequired = errors.New("path is required")
	// ErrUNCPath indicates the caller supplied a UNC-style path.
	ErrUNCPath = errors.New("UNC paths are not allowed")
	// ErrPathNotAbsolute indicates a helper that expects an absolute path was given a relative one.
	ErrPathNotAbsolute = errors.New("path must be absolute")
	// ErrPathOutsideWorkspace indicates the path would escape the workspace boundary.
	ErrPathOutsideWorkspace = errors.New("path escapes workspace")
	// ErrCurrentWorkingDirNotDir indicates the supplied current working directory is not a directory.
	ErrCurrentWorkingDirNotDir = errors.New("current working directory must be a directory")
)

type rootInfo struct {
	lexicalRoot string
	realRoot    string
}

// Resolve safely resolves an existing path within the workspace.
func Resolve(root, cwd, path string) (string, error) {
	return ResolveExisting(root, cwd, path)
}

// ResolveExisting safely resolves an existing path within the workspace and rejects lexical or symlink escapes.
func ResolveExisting(root, cwd, path string) (string, error) {
	return ResolveExistingWithSandboxMount(root, cwd, path, defaultSandboxMountPath)
}

// ResolveExistingWithSandboxMount safely resolves an existing path within the workspace using a configured sandbox mount path.
func ResolveExistingWithSandboxMount(root, cwd, path, sandboxMountPath string) (string, error) {
	info, err := newRootInfo(root)
	if err != nil {
		return "", err
	}

	lexicalBase, err := resolveCurrentDir(info, cwd, sandboxMountPath)
	if err != nil {
		return "", err
	}

	_, realPath, err := resolveExistingFromBase(info, lexicalBase, path, sandboxMountPath)
	if err != nil {
		return "", err
	}

	return realPath, nil
}

// ResolveForCreate safely resolves a path for future creation by evaluating symlinks on existing parents only.
func ResolveForCreate(root, cwd, path string) (string, error) {
	return ResolveForCreateWithSandboxMount(root, cwd, path, defaultSandboxMountPath)
}

// ResolveForCreateWithSandboxMount safely resolves a future creation path using a configured sandbox mount path.
func ResolveForCreateWithSandboxMount(root, cwd, path, sandboxMountPath string) (string, error) {
	info, err := newRootInfo(root)
	if err != nil {
		return "", err
	}

	lexicalBase, err := resolveCurrentDir(info, cwd, sandboxMountPath)
	if err != nil {
		return "", err
	}

	lexicalPath, err := lexicalResolve(info.lexicalRoot, lexicalBase, path, sandboxMountPath)
	if err != nil {
		return "", err
	}

	resolved, err := resolveCreatePath(info, lexicalPath)
	if err != nil {
		return "", err
	}

	return resolved, nil
}

// IsInside reports whether an absolute path is inside the canonical workspace root.
func IsInside(root, absPath string) (bool, error) {
	if absPath == "" {
		return false, ErrPathRequired
	}
	if isUNCPath(absPath) {
		return false, ErrUNCPath
	}
	if !filepath.IsAbs(absPath) {
		return false, ErrPathNotAbsolute
	}

	info, err := newRootInfo(root)
	if err != nil {
		return false, err
	}

	cleanPath, err := cleanAbs(absPath)
	if err != nil {
		return false, err
	}

	infoInside, err := isWithin(info.lexicalRoot, cleanPath)
	if err != nil || !infoInside {
		return infoInside, err
	}

	realPath, err := filepath.EvalSymlinks(cleanPath)
	switch {
	case err == nil:
		realPath, err = cleanAbs(realPath)
		if err != nil {
			return false, err
		}

		return isWithin(info.realRoot, realPath)
	case os.IsNotExist(err):
		resolved, err := ResolveForCreate(root, "", cleanPath)
		if err != nil {
			if errors.Is(err, ErrPathOutsideWorkspace) {
				return false, nil
			}
			return false, err
		}
		return isWithin(info.realRoot, resolved)
	default:
		return false, fmt.Errorf("resolve path %q: %w", absPath, err)
	}
}

func newRootInfo(root string) (rootInfo, error) {
	if isUNCPath(root) {
		return rootInfo{}, ErrUNCPath
	}

	lexicalRoot, err := NormalizeRoot(root)
	if err != nil {
		return rootInfo{}, err
	}

	realRoot, err := filepath.EvalSymlinks(lexicalRoot)
	if err != nil {
		return rootInfo{}, fmt.Errorf("resolve workspace root symlinks: %w", err)
	}

	realRoot, err = cleanAbs(realRoot)
	if err != nil {
		return rootInfo{}, err
	}

	return rootInfo{
		lexicalRoot: lexicalRoot,
		realRoot:    realRoot,
	}, nil
}

func resolveCurrentDir(info rootInfo, cwd, sandboxMountPath string) (string, error) {
	if cwd == "" {
		return info.lexicalRoot, nil
	}
	var err error
	cwd, err = normalizeSandboxMountPath(info.lexicalRoot, cwd, sandboxMountPath)
	if err != nil {
		return "", err
	}

	lexicalPath, realPath, err := resolveExistingFromBase(info, info.lexicalRoot, cwd, sandboxMountPath)
	if err != nil {
		return "", fmt.Errorf("resolve current working directory %q: %w", cwd, err)
	}

	stat, err := os.Stat(realPath)
	if err != nil {
		return "", fmt.Errorf("stat current working directory %q: %w", cwd, err)
	}
	if !stat.IsDir() {
		return "", ErrCurrentWorkingDirNotDir
	}

	return lexicalPath, nil
}

func resolveExistingFromBase(info rootInfo, lexicalBase, path, sandboxMountPath string) (string, string, error) {
	lexicalPath, err := lexicalResolve(info.lexicalRoot, lexicalBase, path, sandboxMountPath)
	if err != nil {
		return "", "", err
	}

	realPath, err := filepath.EvalSymlinks(lexicalPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve path %q: %w", path, err)
	}

	realPath, err = cleanAbs(realPath)
	if err != nil {
		return "", "", err
	}

	inside, err := isWithin(info.realRoot, realPath)
	if err != nil {
		return "", "", err
	}
	if !inside {
		return "", "", fmt.Errorf("%w: %q", ErrPathOutsideWorkspace, path)
	}

	return lexicalPath, realPath, nil
}

func resolveCreatePath(info rootInfo, lexicalPath string) (string, error) {
	current := lexicalPath
	var missing []string

	for {
		stat, err := os.Lstat(current)
		switch {
		case err == nil:
			if len(missing) > 0 {
				targetInfo, err := os.Stat(current)
				if err != nil {
					return "", fmt.Errorf("resolve create path %q: %w", lexicalPath, err)
				}
				if !targetInfo.IsDir() {
					return "", fmt.Errorf("resolve create path %q: existing parent %q is not a directory", lexicalPath, current)
				}
			} else if !stat.Mode().IsRegular() && !stat.IsDir() && stat.Mode()&os.ModeSymlink == 0 {
				return "", fmt.Errorf("resolve create path %q: unsupported existing path type at %q", lexicalPath, current)
			}

			realCurrent, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("resolve create path %q: %w", lexicalPath, err)
			}

			realCurrent, err = cleanAbs(realCurrent)
			if err != nil {
				return "", err
			}

			inside, err := isWithin(info.realRoot, realCurrent)
			if err != nil {
				return "", err
			}
			if !inside {
				return "", fmt.Errorf("%w: %q", ErrPathOutsideWorkspace, lexicalPath)
			}

			resolved := realCurrent
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}

			resolved, err = cleanAbs(resolved)
			if err != nil {
				return "", err
			}

			inside, err = isWithin(info.realRoot, resolved)
			if err != nil {
				return "", err
			}
			if !inside {
				return "", fmt.Errorf("%w: %q", ErrPathOutsideWorkspace, lexicalPath)
			}

			return resolved, nil
		case errors.Is(err, os.ErrNotExist):
			parent := filepath.Dir(current)
			if parent == current {
				return "", fmt.Errorf("resolve create path %q: no existing parent within workspace", lexicalPath)
			}

			missing = append(missing, filepath.Base(current))
			current = parent
		default:
			return "", fmt.Errorf("resolve create path %q: %w", lexicalPath, err)
		}
	}
}

func lexicalResolve(root, base, targetPath, sandboxMountPath string) (string, error) {
	if targetPath == "" {
		return "", ErrPathRequired
	}
	if isUNCPath(targetPath) {
		return "", ErrUNCPath
	}
	targetPath, err := normalizeSandboxMountPath(root, targetPath, sandboxMountPath)
	if err != nil {
		return "", err
	}

	var candidate string
	if filepath.IsAbs(targetPath) {
		abs, err := cleanAbs(targetPath)
		if err != nil {
			return "", err
		}
		candidate = abs
	} else {
		joined, err := cleanAbs(filepath.Join(base, targetPath))
		if err != nil {
			return "", err
		}
		candidate = joined
	}

	inside, err := isWithin(root, candidate)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", fmt.Errorf("%w: %q", ErrPathOutsideWorkspace, targetPath)
	}

	return candidate, nil
}

func isWithin(root, candidate string) (bool, error) {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, fmt.Errorf("check workspace boundary: %w", err)
	}

	if rel == "." {
		return true, nil
	}

	if rel == ".." {
		return false, nil
	}

	prefix := ".." + string(filepath.Separator)
	return !strings.HasPrefix(rel, prefix), nil
}

func cleanAbs(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}

	return filepath.Clean(abs), nil
}

func normalizeSandboxMountPath(root, value, sandboxMountPath string) (string, error) {
	if sandboxMountPath == "" {
		sandboxMountPath = defaultSandboxMountPath
	}
	if isUNCPath(sandboxMountPath) {
		return "", ErrUNCPath
	}
	sandboxMountPath = path.Clean(sandboxMountPath)
	if !strings.HasPrefix(sandboxMountPath, "/") {
		return "", ErrPathNotAbsolute
	}
	matchValue := value
	if strings.HasPrefix(matchValue, "/") {
		matchValue = path.Clean(matchValue)
	}
	if matchValue != sandboxMountPath && !strings.HasPrefix(matchValue, sandboxMountPath+"/") {
		return value, nil
	}

	suffix := strings.TrimPrefix(matchValue, sandboxMountPath)
	suffix = strings.TrimPrefix(suffix, "/")
	if suffix == "" {
		return root, nil
	}

	parts := strings.Split(path.Clean("/"+suffix), "/")
	if len(parts) > 0 && parts[0] == "" {
		parts = parts[1:]
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}

func isUNCPath(path string) bool {
	return strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//")
}
