package runtimeexec

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/smasonuk/falken-core/internal/policy"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/workspace"
)

// ExecutionState is the command-execution session state owned by future runtime code.
type ExecutionState struct {
	workspaceRoot      string
	realWorkspaceRoot  string
	currentWorkingDir  string
	sandboxMountPath   string
	outputArtifactRoot string
	virtualWorkspace   bool
	envOverrides       map[string]string
}

// NewExecutionState creates execution state rooted at the canonical workspace root.
func NewExecutionState(workspaceRoot string) (*ExecutionState, error) {
	root, err := workspace.NormalizeRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}

	stat, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("workspace root %q is not a directory", root)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	realRoot = filepath.Clean(realRoot)

	return &ExecutionState{
		workspaceRoot:     root,
		realWorkspaceRoot: realRoot,
		currentWorkingDir: root,
		sandboxMountPath:  "/workspace",
		envOverrides:      make(map[string]string),
	}, nil
}

// NewVirtualExecutionState creates execution state for adapter-backed runtimes
// where the agent may not have the workspace mounted locally.
func NewVirtualExecutionState(workspaceRoot string) (*ExecutionState, error) {
	root, err := workspace.NormalizeRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}

	return &ExecutionState{
		workspaceRoot:     root,
		realWorkspaceRoot: root,
		currentWorkingDir: root,
		sandboxMountPath:  "/workspace",
		virtualWorkspace:  true,
		envOverrides:      make(map[string]string),
	}, nil
}

// NewExecutionStateForLayout creates execution state using canonical workspace and artifact paths.
func NewExecutionStateForLayout(layout state.Layout) (*ExecutionState, error) {
	execState, err := NewExecutionState(layout.WorkspaceRoot)
	if err != nil {
		return nil, err
	}

	if layout.RecentArtifactRoot != "" {
		root, err := filepath.Abs(layout.RecentArtifactRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve output artifact root: %w", err)
		}
		execState.outputArtifactRoot = filepath.Clean(root)
	}

	return execState, nil
}

// NewVirtualExecutionStateForLayout creates non-statting execution state using
// canonical workspace and artifact paths.
func NewVirtualExecutionStateForLayout(layout state.Layout) (*ExecutionState, error) {
	execState, err := NewVirtualExecutionState(layout.WorkspaceRoot)
	if err != nil {
		return nil, err
	}

	if layout.RecentArtifactRoot != "" {
		root, err := filepath.Abs(layout.RecentArtifactRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve output artifact root: %w", err)
		}
		execState.outputArtifactRoot = filepath.Clean(root)
	}

	return execState, nil
}

// WorkspaceRoot returns the canonical workspace root for execution.
func (s *ExecutionState) WorkspaceRoot() string {
	return s.workspaceRoot
}

// OutputArtifactRoot returns the canonical root for persisted command output artifacts.
func (s *ExecutionState) OutputArtifactRoot() string {
	return s.outputArtifactRoot
}

// VirtualWorkspace reports whether working directories are resolved
// lexically without requiring a local workspace mount.
func (s *ExecutionState) VirtualWorkspace() bool {
	return s != nil && s.virtualWorkspace
}

// CurrentWorkingDir returns the current execution working directory.
func (s *ExecutionState) CurrentWorkingDir() string {
	return s.currentWorkingDir
}

// SandboxMountPath returns the path where the workspace is mounted inside sandboxed commands.
func (s *ExecutionState) SandboxMountPath() string {
	if s == nil || s.sandboxMountPath == "" {
		return "/workspace"
	}
	return s.sandboxMountPath
}

// SetSandboxMountPath stores the visible sandbox workspace mount path.
func (s *ExecutionState) SetSandboxMountPath(path string) {
	if s == nil || strings.TrimSpace(path) == "" {
		return
	}
	s.sandboxMountPath = filepath.Clean(path)
	if !strings.HasPrefix(s.sandboxMountPath, string(filepath.Separator)) {
		s.sandboxMountPath = string(filepath.Separator) + s.sandboxMountPath
	}
}

// ToolPathForHostPath returns the workspace-relative path models should reuse in tool arguments.
func (s *ExecutionState) ToolPathForHostPath(hostPath string) string {
	if s == nil {
		return "."
	}
	for _, root := range []string{s.workspaceRoot, s.realWorkspaceRoot} {
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, filepath.Clean(hostPath))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				return "."
			}
			return rel
		}
	}
	return "."
}

// SandboxPathForHostPath returns the equivalent path visible inside sandbox command output.
func (s *ExecutionState) SandboxPathForHostPath(hostPath string) string {
	toolPath := s.ToolPathForHostPath(hostPath)
	if toolPath == "." {
		return s.SandboxMountPath()
	}
	parts := strings.Split(filepath.ToSlash(toolPath), "/")
	return filepath.ToSlash(filepath.Join(append([]string{s.SandboxMountPath()}, parts...)...))
}

// SetCurrentWorkingDir updates the execution cwd after resolving it safely inside the workspace.
func (s *ExecutionState) SetCurrentWorkingDir(path string) error {
	resolved, err := s.ResolveWorkingDir(path)
	if err != nil {
		return err
	}

	s.currentWorkingDir = resolved
	return nil
}

// ResolveWorkingDir resolves a cwd override relative to the current execution cwd.
func (s *ExecutionState) ResolveWorkingDir(path string) (string, error) {
	if s.virtualWorkspace {
		return s.resolveVirtualWorkingDir(path)
	}
	resolved, err := workspace.ResolveExistingWithSandboxMount(s.workspaceRoot, s.currentWorkingDir, path, s.SandboxMountPath())
	if err != nil {
		return "", err
	}

	stat, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat working directory: %w", err)
	}
	if !stat.IsDir() {
		return "", workspace.ErrCurrentWorkingDirNotDir
	}

	return filepath.Clean(resolved), nil
}

// ResolveWorkspacePath resolves a file path inside the workspace. In local
// execution state, existing paths are symlink/stat checked as before. In virtual
// workspace state, paths are constrained lexically and left for the remote
// runner to validate against its real filesystem.
func (s *ExecutionState) ResolveWorkspacePath(path string, forCreate bool) (string, error) {
	if s == nil {
		return "", ErrExecutionStateRequired
	}
	mode := policy.FileAccessRead
	if forCreate {
		mode = policy.FileAccessCreate
	}
	return s.ResolvePathForPolicy("", path, mode)
}

// ResolvePathForPolicy resolves a workspace path for file policy evaluation.
// Virtual execution states resolve lexically without statting the local
// workspace so remote-runner agent pods do not need a workspace mount.
func (s *ExecutionState) ResolvePathForPolicy(cwd, path string, mode policy.FileAccessMode) (string, error) {
	if s == nil {
		return "", ErrExecutionStateRequired
	}
	if s.virtualWorkspace {
		return s.resolveVirtualWorkspacePath(cwd, path)
	}
	if cwd == "" {
		cwd = s.currentWorkingDir
	}
	if mode == policy.FileAccessCreate {
		return workspace.ResolveForCreateWithSandboxMount(s.workspaceRoot, cwd, path, s.SandboxMountPath())
	}
	return workspace.ResolveExistingWithSandboxMount(s.workspaceRoot, cwd, path, s.SandboxMountPath())
}

func (s *ExecutionState) resolveVirtualWorkingDir(path string) (string, error) {
	base := filepath.Clean(s.currentWorkingDir)
	if base == "" {
		base = s.workspaceRoot
	}
	if path == "" {
		return base, nil
	}

	candidate, err := s.virtualPathToHost(base, path)
	if err != nil {
		return "", err
	}
	if !pathInside(s.workspaceRoot, candidate) {
		return "", fmt.Errorf("%w: %q", workspace.ErrPathOutsideWorkspace, path)
	}
	return candidate, nil
}

func (s *ExecutionState) resolveVirtualWorkspacePath(cwd, path string) (string, error) {
	base := filepath.Clean(s.currentWorkingDir)
	if base == "" {
		base = s.workspaceRoot
	}
	if cwd != "" {
		var err error
		base, err = s.virtualPathToHost(s.workspaceRoot, cwd)
		if err != nil {
			return "", err
		}
		if !pathInside(s.workspaceRoot, base) {
			return "", fmt.Errorf("%w: %q", workspace.ErrPathOutsideWorkspace, cwd)
		}
	}
	candidate, err := s.virtualPathToHost(base, path)
	if err != nil {
		return "", err
	}
	if !pathInside(s.workspaceRoot, candidate) {
		return "", fmt.Errorf("%w: %q", workspace.ErrPathOutsideWorkspace, path)
	}
	return candidate, nil
}

func (s *ExecutionState) virtualPathToHost(base, value string) (string, error) {
	value = filepath.Clean(filepath.FromSlash(value))
	if filepath.IsAbs(value) {
		mountPath := filepath.Clean(filepath.FromSlash(s.SandboxMountPath()))
		if pathInside(mountPath, value) {
			rel, err := filepath.Rel(mountPath, value)
			if err != nil {
				return "", fmt.Errorf("resolve sandbox working directory: %w", err)
			}
			if rel == "." {
				return s.workspaceRoot, nil
			}
			return filepath.Clean(filepath.Join(s.workspaceRoot, rel)), nil
		}
		return filepath.Clean(value), nil
	}
	return filepath.Clean(filepath.Join(base, value)), nil
}

func pathInside(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// SetEnv stores an execution-session environment override.
func (s *ExecutionState) SetEnv(key, value string) {
	s.envOverrides[key] = value
}

// EnvOverrides returns a copy of the execution-session environment overrides.
func (s *ExecutionState) EnvOverrides() map[string]string {
	return cloneMap(s.envOverrides)
}

// MergedEnvironment merges ambient environment values with session and request overrides.
func (s *ExecutionState) MergedEnvironment(requestOverrides map[string]string) []string {
	return mergeEnvironment(os.Environ(), s.envOverrides, requestOverrides)
}

// SandboxEnvironment returns a minimal deterministic environment for sandboxed commands.
func (s *ExecutionState) SandboxEnvironment(requestOverrides map[string]string) []string {
	return mergeEnvironment(defaultSandboxEnvironment(), s.envOverrides, requestOverrides)
}

func defaultSandboxEnvironment() []string {
	values := []string{
		"HOME=/workspace",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"TERM=xterm",
	}
	if runtime.GOOS == "windows" {
		values = append(values, `PATH=C:\Windows\System32;C:\Windows`)
	} else {
		values = append(values, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}
	return values
}

func mergeEnvironment(base []string, overrides ...map[string]string) []string {
	values := make(map[string]string, len(base))
	for _, entry := range base {
		key, value, ok := splitEnv(entry)
		if ok {
			values[key] = value
		}
	}

	for _, override := range overrides {
		for key, value := range override {
			values[key] = value
		}
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	merged := make([]string, 0, len(keys))
	for _, key := range keys {
		merged = append(merged, key+"="+values[key])
	}
	return merged
}

func splitEnv(entry string) (string, string, bool) {
	return strings.Cut(entry, "=")
}

func cloneMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
