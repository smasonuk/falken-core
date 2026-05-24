package falken

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/smasonuk/falken-core/internal/persist"
	"github.com/smasonuk/falken-core/internal/policy"
	"github.com/smasonuk/falken-core/internal/state"
)

const projectApprovalsFileName = "project_approvals.json"

var projectApprovalsFileMu sync.Mutex

// persistedProjectApprovals represents the serialized form of project-scoped policy approvals.
type persistedProjectApprovals struct {
	Files   []policy.FileRule    `json:"files,omitempty"`
	Shell   []policy.ShellRule   `json:"shell,omitempty"`
	Network []policy.NetworkRule `json:"network,omitempty"`
}

// newSessionPolicyEngine initializes a policy engine for the session, merging config and persisted rules.
func newSessionPolicyEngine(layout state.Layout, config SessionConfig) (*policy.Engine, error) {
	policyConfig := toInternalPolicyConfig(config.Policy)
	persisted, err := readProjectApprovals(layout)
	if err != nil {
		return nil, err
	}
	mergeProjectApprovals(&policyConfig, persisted)
	addDefaultApprovalRequiredShellRules(&policyConfig)

	engine := policy.NewEngine(policyConfig, approvalHandlerAdapter{handler: config.ApprovalHandler})
	engine.SetProjectApprovalPersister(func(current policy.Config) error {
		return mergeAndWriteProjectApprovals(layout, current)
	})
	return engine, nil
}

// readProjectApprovals reads and deserializes project approvals from the state directory.
func readProjectApprovals(layout state.Layout) (persistedProjectApprovals, error) {
	projectApprovalsFileMu.Lock()
	defer projectApprovalsFileMu.Unlock()
	return readProjectApprovalsUnlocked(layout)
}

func readProjectApprovalsUnlocked(layout state.Layout) (persistedProjectApprovals, error) {
	var approvals persistedProjectApprovals
	found, err := persist.ReadJSON(projectApprovalsPath(layout), &approvals)
	if err != nil {
		return persistedProjectApprovals{}, fmt.Errorf("read project approvals: %w", err)
	}
	if !found {
		return persistedProjectApprovals{}, nil
	}
	return approvals, nil
}

// writeProjectApprovals serializes and writes project approvals back to the state directory.
func writeProjectApprovals(layout state.Layout, config policy.Config) error {
	projectApprovalsFileMu.Lock()
	defer projectApprovalsFileMu.Unlock()
	return writeProjectApprovalsUnlocked(layout, config)
}

func writeProjectApprovalsUnlocked(layout state.Layout, config policy.Config) error {
	approvals := persistedProjectApprovals{
		Files:   append([]policy.FileRule(nil), config.ProjectApprovedFiles...),
		Shell:   append([]policy.ShellRule(nil), config.ProjectApprovedShell...),
		Network: append([]policy.NetworkRule(nil), config.ProjectApprovedNet...),
	}
	if err := persist.WriteJSONAtomic(projectApprovalsPath(layout), approvals, 0o600); err != nil {
		return fmt.Errorf("write project approvals: %w", err)
	}
	return nil
}

func mergeAndWriteProjectApprovals(layout state.Layout, config policy.Config) error {
	projectApprovalsFileMu.Lock()
	defer projectApprovalsFileMu.Unlock()

	existing, err := readProjectApprovalsUnlocked(layout)
	if err != nil {
		return err
	}
	merged := policy.Config{
		ProjectApprovedFiles: mergeFileRules(existing.Files, config.ProjectApprovedFiles),
		ProjectApprovedShell: mergeShellRules(existing.Shell, config.ProjectApprovedShell),
		ProjectApprovedNet:   mergeNetworkRules(existing.Network, config.ProjectApprovedNet),
	}
	return writeProjectApprovalsUnlocked(layout, merged)
}

// projectApprovalsPath returns the absolute path to the project approvals JSON file.
func projectApprovalsPath(layout state.Layout) string {
	return filepath.Join(layout.StateRoot, projectApprovalsFileName)
}

// mergeProjectApprovals combines persisted approval rules into the active policy configuration.
func mergeProjectApprovals(config *policy.Config, persisted persistedProjectApprovals) {
	config.ProjectApprovedFiles = mergeFileRules(config.ProjectApprovedFiles, persisted.Files)
	config.ProjectApprovedShell = mergeShellRules(config.ProjectApprovedShell, persisted.Shell)
	config.ProjectApprovedNet = mergeNetworkRules(config.ProjectApprovedNet, persisted.Network)
}

// mergeFileRules merges an extra set of file rules into a base set, omitting duplicates.
func mergeFileRules(base, extra []policy.FileRule) []policy.FileRule {
	merged := append([]policy.FileRule(nil), base...)
	for _, rule := range extra {
		if !hasFileRule(merged, rule) {
			merged = append(merged, rule)
		}
	}
	return merged
}

// mergeShellRules merges an extra set of shell rules into a base set, omitting duplicates.
func mergeShellRules(base, extra []policy.ShellRule) []policy.ShellRule {
	merged := append([]policy.ShellRule(nil), base...)
	for _, rule := range extra {
		if !hasShellRule(merged, rule) {
			merged = append(merged, rule)
		}
	}
	return merged
}

// mergeNetworkRules merges an extra set of network rules into a base set, omitting duplicates.
func mergeNetworkRules(base, extra []policy.NetworkRule) []policy.NetworkRule {
	merged := append([]policy.NetworkRule(nil), base...)
	for _, rule := range extra {
		if !hasNetworkRule(merged, rule) {
			merged = append(merged, rule)
		}
	}
	return merged
}

// hasFileRule returns true if the specified file rule already exists in the given rules.
func hasFileRule(rules []policy.FileRule, want policy.FileRule) bool {
	for _, rule := range rules {
		if rule.Path == want.Path && rule.Match == want.Match && samePolicyModes(rule.Modes, want.Modes) {
			return true
		}
	}
	return false
}

// hasShellRule returns true if the specified shell rule already exists in the given rules.
func hasShellRule(rules []policy.ShellRule, want policy.ShellRule) bool {
	for _, rule := range rules {
		if rule.Command == want.Command && rule.Match == want.Match {
			return true
		}
	}
	return false
}

// hasNetworkRule returns true if the specified network rule already exists in the given rules.
func hasNetworkRule(rules []policy.NetworkRule, want policy.NetworkRule) bool {
	for _, rule := range rules {
		if rule.Host == want.Host && rule.Match == want.Match {
			return true
		}
	}
	return false
}

// samePolicyModes returns true if two slices of file access modes are identical.
func samePolicyModes(a, b []policy.FileAccessMode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
