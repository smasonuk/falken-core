package falken

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/smasonuk/falken-core/internal/policy"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/workspace"
)

// ProjectPermissions is the public project-scoped permission model persisted
// in the workspace state directory.
type ProjectPermissions struct {
	Files   []FileRule    `json:"files,omitempty"`
	Shell   []ShellRule   `json:"shell,omitempty"`
	Network []NetworkRule `json:"network,omitempty"`
}

// ReadProjectPermissionsForWorkspace reads project permissions for a workspace
// without requiring a started session. When permissions defaults have not been
// initialized yet, it performs the same one-time default initialization that
// session startup would perform.
func ReadProjectPermissionsForWorkspace(workspaceDir, stateDir string) (ProjectPermissions, error) {
	layout, err := projectPermissionsLayout(workspaceDir, stateDir)
	if err != nil {
		return ProjectPermissions{}, err
	}
	if err := state.EnsureLayoutState(layout); err != nil {
		return ProjectPermissions{}, err
	}
	metadata, err := state.TouchMetadata(layout)
	if err != nil {
		return ProjectPermissions{}, err
	}
	if !metadata.ProjectPermissionsInitialized {
		if _, err := initializeProjectPermissionsDefaults(context.Background(), layout, metadata); err != nil {
			return ProjectPermissions{}, err
		}
	}

	approvals, err := readProjectApprovals(layout)
	if err != nil {
		return ProjectPermissions{}, err
	}
	return permissionsFromApprovals(approvals), nil
}

// WriteProjectPermissionsForWorkspace validates the workspace paths, persists
// the supplied project permissions, and marks permission defaults initialized so
// later session startup will not overwrite setup-time edits.
func WriteProjectPermissionsForWorkspace(workspaceDir, stateDir string, permissions ProjectPermissions) error {
	layout, err := projectPermissionsLayout(workspaceDir, stateDir)
	if err != nil {
		return err
	}
	if err := state.EnsureLayoutState(layout); err != nil {
		return err
	}
	metadata, err := state.TouchMetadata(layout)
	if err != nil {
		return err
	}
	if err := writeProjectApprovals(layout, policy.Config{
		ProjectApprovedFiles: cloneFileRulesForPermissions(permissions.Files),
		ProjectApprovedShell: append([]policy.ShellRule(nil), permissions.Shell...),
		ProjectApprovedNet:   append([]policy.NetworkRule(nil), permissions.Network...),
	}); err != nil {
		return err
	}
	metadata.ProjectPermissionsInitialized = true
	metadata.ProjectPermissionsVersion = state.ProjectPermissionsDefaultsVersion
	return state.WriteMetadata(layout, metadata)
}

// ReadProjectPermissions returns the current project-scoped permissions for
// the started session.
func (s *Session) ReadProjectPermissions() (ProjectPermissions, error) {
	layout, err := s.currentLayout()
	if err != nil {
		return ProjectPermissions{}, err
	}

	approvals, err := readProjectApprovals(layout)
	if err != nil {
		return ProjectPermissions{}, err
	}
	return permissionsFromApprovals(approvals), nil
}

// WriteProjectPermissions persists exactly the supplied project-scoped
// permissions. Defaults are not merged into the supplied rules.
func (s *Session) WriteProjectPermissions(permissions ProjectPermissions) error {
	layout, engine, err := s.currentProjectPermissionsTarget()
	if err != nil {
		return err
	}

	files := cloneFileRulesForPermissions(permissions.Files)
	shell := append([]policy.ShellRule(nil), permissions.Shell...)
	network := append([]policy.NetworkRule(nil), permissions.Network...)
	if err := writeProjectApprovals(layout, policy.Config{
		ProjectApprovedFiles: files,
		ProjectApprovedShell: shell,
		ProjectApprovedNet:   network,
	}); err != nil {
		return err
	}
	if engine != nil {
		engine.ReplaceProjectApprovals(files, shell, network)
	}
	return nil
}

// EnsureDefaultProjectPermissions writes the default project permissions when
// no project permissions file exists. It returns true when defaults were
// written, and false when a permissions file was already present.
func (s *Session) EnsureDefaultProjectPermissions() (bool, error) {
	layout, err := s.currentLayout()
	if err != nil {
		return false, err
	}
	return ensureDefaultProjectApprovals(context.Background(), layout)
}

func projectPermissionsLayout(workspaceDir, stateDir string) (state.Layout, error) {
	workspaceRoot, err := workspace.NormalizeRoot(workspaceDir)
	if err != nil {
		return state.Layout{}, err
	}
	layout, err := state.ResolveLayout(workspaceRoot, stateDir)
	if err != nil {
		return state.Layout{}, err
	}
	return layout, nil
}

func (s *Session) currentLayout() (state.Layout, error) {
	if s == nil {
		return state.Layout{}, ErrSessionClosed
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case lifecycleClosed:
		return state.Layout{}, ErrSessionClosed
	case lifecycleNew:
		return state.Layout{}, ErrSessionNotStarted
	case lifecycleStarting:
		return state.Layout{}, ErrSessionStarting
	case lifecycleClosing:
		return state.Layout{}, ErrSessionClosing
	case lifecycleStarted:
		return s.layout, nil
	default:
		return state.Layout{}, fmt.Errorf("unknown session lifecycle state: %d", s.state)
	}
}

func (s *Session) currentProjectPermissionsTarget() (state.Layout, *policy.Engine, error) {
	if s == nil {
		return state.Layout{}, nil, ErrSessionClosed
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case lifecycleClosed:
		return state.Layout{}, nil, ErrSessionClosed
	case lifecycleNew:
		return state.Layout{}, nil, ErrSessionNotStarted
	case lifecycleStarting:
		return state.Layout{}, nil, ErrSessionStarting
	case lifecycleClosing:
		return state.Layout{}, nil, ErrSessionClosing
	case lifecycleStarted:
		var engine *policy.Engine
		if s.resources.runtime != nil {
			engine = s.resources.runtime.policyEngine
		}
		return s.layout, engine, nil
	default:
		return state.Layout{}, nil, fmt.Errorf("unknown session lifecycle state: %d", s.state)
	}
}

func ensureDefaultProjectApprovals(ctx context.Context, layout state.Layout) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	exists, err := projectApprovalsFileExists(layout)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if err := writeProjectApprovals(layout, defaultProjectApprovals(layout)); err != nil {
		return false, err
	}
	return true, nil
}

func initializeProjectPermissionsDefaults(ctx context.Context, layout state.Layout, metadata state.Metadata) (state.Metadata, error) {
	if metadata.ProjectPermissionsInitialized {
		return metadata, nil
	}
	if _, err := ensureDefaultProjectApprovals(ctx, layout); err != nil {
		return state.Metadata{}, err
	}
	metadata.ProjectPermissionsInitialized = true
	metadata.ProjectPermissionsVersion = state.ProjectPermissionsDefaultsVersion
	if err := state.WriteMetadata(layout, metadata); err != nil {
		return state.Metadata{}, err
	}
	return metadata, nil
}

func projectApprovalsFileExists(layout state.Layout) (bool, error) {
	_, err := os.Stat(projectApprovalsPath(layout))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat project approvals: %w", err)
}

func defaultProjectApprovals(layout state.Layout) policy.Config {
	return policy.Config{
		ProjectApprovedFiles: []policy.FileRule{{
			Path:  layout.WorkspaceRoot,
			Match: policy.MatchPrefix,
			Modes: []policy.FileAccessMode{
				policy.FileAccessRead,
				policy.FileAccessWrite,
				policy.FileAccessCreate,
			},
		}},
		ProjectApprovedShell: defaultProjectShellRules(),
		ProjectApprovedNet:   defaultProjectNetworkRules(),
	}
}

func defaultProjectShellRules() []policy.ShellRule {
	prefixes := []string{
		"git ",
		"go ",
		"npm ",
		"pnpm ",
		"yarn ",
		"node ",
		"npx ",
		"python ",
		"python3 ",
		"pip ",
		"pip3 ",
		"uv ",
		"cargo ",
		"rustup ",
		"make ",
		"just ",
		"cmake ",
		"pytest ",
		"ruff ",
		"mypy ",
		"tsc ",
		"eslint ",
		"golangci-lint ",
	}
	rules := make([]policy.ShellRule, 0, len(prefixes))
	for _, prefix := range prefixes {
		rules = append(rules, policy.ShellRule{Command: prefix, Match: policy.MatchPrefix})
	}
	return rules
}

func addDefaultApprovalRequiredShellRules(config *policy.Config) {
	config.ApprovalRequiredShell = mergeShellRules(
		config.ApprovalRequiredShell,
		defaultApprovalRequiredShellRules(),
	)
}

func defaultApprovalRequiredShellRules() []policy.ShellRule {
	return []policy.ShellRule{
		{Command: "rm", Match: policy.MatchExact},
		{Command: "rm ", Match: policy.MatchPrefix},
		{Command: "/bin/rm", Match: policy.MatchExact},
		{Command: "/bin/rm ", Match: policy.MatchPrefix},
		{Command: "/usr/bin/rm", Match: policy.MatchExact},
		{Command: "/usr/bin/rm ", Match: policy.MatchPrefix},
		{Command: "git clean", Match: policy.MatchExact},
		{Command: "git clean ", Match: policy.MatchPrefix},
		{Command: "find", Match: policy.MatchExact},
		{Command: "find ", Match: policy.MatchPrefix},
	}
}

func defaultProjectNetworkRules() []policy.NetworkRule {
	hosts := []string{
		"github.com",
		"objects.githubusercontent.com",
		"raw.githubusercontent.com",
		"codeload.github.com",
		"api.github.com",
		"golang.org",
		"go.dev",
		"proxy.golang.org",
		"sum.golang.org",
		"npmjs.org",
		"registry.npmjs.org",
		"pypi.org",
		"files.pythonhosted.org",
		"crates.io",
		"static.crates.io",
		"gitlab.com",
		"bitbucket.org",
	}
	suffixes := []string{
		".github.com",
		".githubusercontent.com",
		".golang.org",
		".go.dev",
		".npmjs.org",
		".pypi.org",
		".pythonhosted.org",
		".crates.io",
		".gitlab.com",
		".bitbucket.org",
	}
	rules := make([]policy.NetworkRule, 0, len(hosts)+len(suffixes))
	for _, host := range hosts {
		rules = append(rules, policy.NetworkRule{Host: host, Match: policy.MatchExact})
	}
	for _, suffix := range suffixes {
		rules = append(rules, policy.NetworkRule{Host: suffix, Match: policy.MatchSuffix})
	}
	return rules
}

func permissionsFromApprovals(approvals persistedProjectApprovals) ProjectPermissions {
	return ProjectPermissions{
		Files:   cloneFileRulesForPermissions(approvals.Files),
		Shell:   append([]policy.ShellRule(nil), approvals.Shell...),
		Network: append([]policy.NetworkRule(nil), approvals.Network...),
	}
}

func cloneFileRulesForPermissions(rules []policy.FileRule) []policy.FileRule {
	cloned := make([]policy.FileRule, len(rules))
	for i, rule := range rules {
		cloned[i] = policy.FileRule{
			Path:  rule.Path,
			Match: rule.Match,
			Modes: append([]policy.FileAccessMode(nil), rule.Modes...),
		}
	}
	return cloned
}
