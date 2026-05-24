package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// MatchKind controls how a rule compares its target to a request target.
type MatchKind string

const (
	MatchExact  MatchKind = "exact"
	MatchPrefix MatchKind = "prefix"
	MatchSuffix MatchKind = "suffix"
)

// ApprovalScope describes how an approval decision should be applied.
type ApprovalScope string

const (
	ApprovalScopeOnce    ApprovalScope = "once"
	ApprovalScopeSession ApprovalScope = "session"
	ApprovalScopeProject ApprovalScope = "project"
	ApprovalScopeDeny    ApprovalScope = "deny"
)

// DecisionSource explains how an access decision was reached.
type DecisionSource string

const (
	DecisionSourceBlockedRule            DecisionSource = "blocked_rule"
	DecisionSourceAllowedRule            DecisionSource = "allowed_rule"
	DecisionSourceProjectApproval        DecisionSource = "project_approval"
	DecisionSourceSessionApproval        DecisionSource = "session_approval"
	DecisionSourceStrictAllowlistDeny    DecisionSource = "strict_allowlist_deny"
	DecisionSourceMissingApprovalHandler DecisionSource = "missing_approval_handler_deny"
	DecisionSourceApprovalDenied         DecisionSource = "approval_denied"
	DecisionSourceApprovalGrantedOnce    DecisionSource = "approval_granted_once"
	DecisionSourceApprovalGrantedSession DecisionSource = "approval_granted_session"
	DecisionSourceApprovalGrantedProject DecisionSource = "approval_granted_project"
	DecisionSourceDefaultAllow           DecisionSource = "default_allow"
)

// RequestKind identifies the type of access request being evaluated.
type RequestKind string

const (
	RequestKindFile    RequestKind = "file"
	RequestKindShell   RequestKind = "shell"
	RequestKindNetwork RequestKind = "network"
)

// FileAccessMode captures the requested file access operation.
type FileAccessMode string

const (
	FileAccessRead   FileAccessMode = "read"
	FileAccessWrite  FileAccessMode = "write"
	FileAccessCreate FileAccessMode = "create"
)

// FileRule configures file access matching.
type FileRule struct {
	Path  string           `json:"path"`
	Match MatchKind        `json:"match"`
	Modes []FileAccessMode `json:"modes,omitempty"`
}

// ShellRule configures shell access matching.
type ShellRule struct {
	Command string    `json:"command"`
	Match   MatchKind `json:"match"`
}

// NetworkRule configures network access matching.
type NetworkRule struct {
	Host  string    `json:"host"`
	Match MatchKind `json:"match"`
	Ports []int     `json:"ports,omitempty"`
}

// Config is the canonical serializable policy configuration model.
type Config struct {
	BlockedFiles         []FileRule `json:"blocked_files,omitempty"`
	AllowedFiles         []FileRule `json:"allowed_files,omitempty"`
	ProjectApprovedFiles []FileRule `json:"project_approved_files,omitempty"`

	BlockedShell          []ShellRule `json:"blocked_shell,omitempty"`
	ApprovalRequiredShell []ShellRule `json:"approval_required_shell,omitempty"`
	AllowedShell          []ShellRule `json:"allowed_shell,omitempty"`
	ProjectApprovedShell  []ShellRule `json:"project_approved_shell,omitempty"`

	BlockedNetwork     []NetworkRule `json:"blocked_network,omitempty"`
	AllowedNetwork     []NetworkRule `json:"allowed_network,omitempty"`
	ProjectApprovedNet []NetworkRule `json:"project_approved_network,omitempty"`

	StrictFileAllowlist    bool `json:"strict_file_allowlist,omitempty"`
	StrictCommandAllowlist bool `json:"strict_command_allowlist,omitempty"`
	StrictNetworkAllowlist bool `json:"strict_network_allowlist,omitempty"`
}

// MarshalJSON exists to keep Config round-trippable and explicit if fields evolve.
func (c Config) MarshalJSON() ([]byte, error) {
	type alias Config
	return json.Marshal(alias(c))
}

// UnmarshalJSON exists to keep Config round-trippable and explicit if fields evolve.
func (c *Config) UnmarshalJSON(data []byte) error {
	type alias Config
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = Config(decoded)
	return nil
}

// FileRequest is a typed file-access request.
type FileRequest struct {
	Path             string
	Mode             FileAccessMode
	ApprovalRequired bool
}

// ShellRequest is a typed shell-access request.
type ShellRequest struct {
	Command          string
	ApprovalRequired bool
	ForceApproval    bool
}

// NetworkRequest is a typed network-access request.
type NetworkRequest struct {
	Host             string
	Port             int
	Method           string
	Target           string
	ApprovalRequired bool
}

// Decision is the canonical result of a policy evaluation.
type Decision struct {
	Kind    RequestKind
	Allowed bool
	Source  DecisionSource
	Scope   ApprovalScope
}

// ApprovalHandler is the narrow optional host contract for approval-required requests.
type ApprovalHandler interface {
	ApproveFile(context.Context, FileRequest) (ApprovalScope, error)
	ApproveShell(context.Context, ShellRequest) (ApprovalScope, error)
	ApproveNetwork(context.Context, NetworkRequest) (ApprovalScope, error)
}

type sessionApprovals struct {
	files   map[string]struct{}
	shell   map[string]struct{}
	network map[string]struct{}
}

// Engine evaluates access decisions against config plus session-scoped approvals.
type Engine struct {
	mu                       sync.Mutex
	config                   Config
	handler                  ApprovalHandler
	projectApprovalPersister func(Config) error
	approvals                sessionApprovals
}

// NewEngine builds a policy engine with the supplied config and optional approval handler.
func NewEngine(config Config, handler ApprovalHandler) *Engine {
	return &Engine{
		config:  cloneConfig(config),
		handler: handler,
		approvals: sessionApprovals{
			files:   make(map[string]struct{}),
			shell:   make(map[string]struct{}),
			network: make(map[string]struct{}),
		},
	}
}

// SetProjectApprovalPersister installs a callback that persists project-scoped approvals.
func (e *Engine) SetProjectApprovalPersister(persister func(Config) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.projectApprovalPersister = persister
}

// Config returns a copy of the engine's current config, including project-scoped approvals learned at runtime.
func (e *Engine) Config() Config {
	e.mu.Lock()
	defer e.mu.Unlock()

	return cloneConfig(e.config)
}

// ReplaceProjectApprovals swaps the project-scoped approvals in the active
// policy config while preserving blocked/allowed rules, strict settings, and
// session-scoped approvals.
func (e *Engine) ReplaceProjectApprovals(files []FileRule, shell []ShellRule, network []NetworkRule) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.config.ProjectApprovedFiles = cloneFileRules(files)
	e.config.ProjectApprovedShell = append([]ShellRule(nil), shell...)
	e.config.ProjectApprovedNet = append([]NetworkRule(nil), network...)
}

// EvaluateFile evaluates a file-access request.
func (e *Engine) EvaluateFile(ctx context.Context, request FileRequest) (Decision, error) {
	normalized := normalizeFilePath(request.Path)

	e.mu.Lock()
	if matchesFileRule(e.config.BlockedFiles, normalized, request.Mode) {
		e.mu.Unlock()
		return Decision{Kind: RequestKindFile, Allowed: false, Source: DecisionSourceBlockedRule}, nil
	}

	if _, ok := e.approvals.files[fileApprovalKey(normalized, request.Mode)]; ok {
		e.mu.Unlock()
		return Decision{Kind: RequestKindFile, Allowed: true, Source: DecisionSourceSessionApproval, Scope: ApprovalScopeSession}, nil
	}

	if matchesFileRule(e.config.ProjectApprovedFiles, normalized, request.Mode) {
		e.mu.Unlock()
		return Decision{Kind: RequestKindFile, Allowed: true, Source: DecisionSourceProjectApproval, Scope: ApprovalScopeProject}, nil
	}

	if matchesFileRule(e.config.AllowedFiles, normalized, request.Mode) {
		e.mu.Unlock()
		return Decision{Kind: RequestKindFile, Allowed: true, Source: DecisionSourceAllowedRule}, nil
	}

	if e.config.StrictFileAllowlist {
		e.mu.Unlock()
		return Decision{Kind: RequestKindFile, Allowed: false, Source: DecisionSourceStrictAllowlistDeny}, nil
	}

	if request.ApprovalRequired {
		handler := e.handler
		e.mu.Unlock()
		if handler == nil {
			return Decision{Kind: RequestKindFile, Allowed: false, Source: DecisionSourceMissingApprovalHandler}, nil
		}

		scope, err := handler.ApproveFile(ctx, FileRequest{
			Path:             normalized,
			Mode:             request.Mode,
			ApprovalRequired: request.ApprovalRequired,
		})
		if err != nil {
			return Decision{}, fmt.Errorf("request file approval: %w", err)
		}

		e.mu.Lock()
		decision := e.applyFileApproval(normalized, request.Mode, scope)
		persister, persistConfig, shouldPersist := e.projectApprovalPersistRequestLocked(decision)
		e.mu.Unlock()
		if shouldPersist {
			if err := persister(persistConfig); err != nil {
				return Decision{}, fmt.Errorf("persist project file approval: %w", err)
			}
		}
		return decision, nil
	}

	e.mu.Unlock()
	return Decision{Kind: RequestKindFile, Allowed: true, Source: DecisionSourceDefaultAllow}, nil
}

// EvaluateShell evaluates a shell-access request.
func (e *Engine) EvaluateShell(ctx context.Context, request ShellRequest) (Decision, error) {
	command := strings.TrimSpace(request.Command)

	e.mu.Lock()
	if matchesShellRule(e.config.BlockedShell, command) {
		e.mu.Unlock()
		return Decision{Kind: RequestKindShell, Allowed: false, Source: DecisionSourceBlockedRule}, nil
	}

	if _, ok := e.approvals.shell[command]; ok {
		e.mu.Unlock()
		return Decision{Kind: RequestKindShell, Allowed: true, Source: DecisionSourceSessionApproval, Scope: ApprovalScopeSession}, nil
	}

	if matchesExactShellRule(e.config.ProjectApprovedShell, command) {
		e.mu.Unlock()
		return Decision{Kind: RequestKindShell, Allowed: true, Source: DecisionSourceProjectApproval, Scope: ApprovalScopeProject}, nil
	}

	approvalRequired := request.ApprovalRequired || request.ForceApproval ||
		matchesShellRule(e.config.ApprovalRequiredShell, command)
	if approvalRequired {
		handler := e.handler
		e.mu.Unlock()
		if handler == nil {
			return Decision{Kind: RequestKindShell, Allowed: false, Source: DecisionSourceMissingApprovalHandler}, nil
		}

		scope, err := handler.ApproveShell(ctx, ShellRequest{
			Command:          command,
			ApprovalRequired: true,
			ForceApproval:    request.ForceApproval,
		})
		if err != nil {
			return Decision{}, fmt.Errorf("request shell approval: %w", err)
		}

		e.mu.Lock()
		decision := e.applyShellApproval(command, scope)
		persister, persistConfig, shouldPersist := e.projectApprovalPersistRequestLocked(decision)
		e.mu.Unlock()
		if shouldPersist {
			if err := persister(persistConfig); err != nil {
				return Decision{}, fmt.Errorf("persist project shell approval: %w", err)
			}
		}
		return decision, nil
	}

	if matchesShellRule(e.config.ProjectApprovedShell, command) {
		e.mu.Unlock()
		return Decision{Kind: RequestKindShell, Allowed: true, Source: DecisionSourceProjectApproval, Scope: ApprovalScopeProject}, nil
	}

	if matchesShellRule(e.config.AllowedShell, command) {
		e.mu.Unlock()
		return Decision{Kind: RequestKindShell, Allowed: true, Source: DecisionSourceAllowedRule}, nil
	}

	if e.config.StrictCommandAllowlist {
		e.mu.Unlock()
		return Decision{Kind: RequestKindShell, Allowed: false, Source: DecisionSourceStrictAllowlistDeny}, nil
	}

	e.mu.Unlock()
	return Decision{Kind: RequestKindShell, Allowed: true, Source: DecisionSourceDefaultAllow}, nil
}

// EvaluateNetwork evaluates a network-access request.
func (e *Engine) EvaluateNetwork(ctx context.Context, request NetworkRequest) (Decision, error) {
	host := normalizeNetworkHost(request.Host)
	port := request.Port
	if host == "" {
		return Decision{Kind: RequestKindNetwork, Allowed: false, Source: DecisionSourceStrictAllowlistDeny}, nil
	}

	e.mu.Lock()
	if matchesNetworkRule(e.config.BlockedNetwork, host, port) {
		e.mu.Unlock()
		return Decision{Kind: RequestKindNetwork, Allowed: false, Source: DecisionSourceBlockedRule}, nil
	}

	if _, ok := e.approvals.network[networkApprovalKey(host, port)]; ok {
		e.mu.Unlock()
		return Decision{Kind: RequestKindNetwork, Allowed: true, Source: DecisionSourceSessionApproval, Scope: ApprovalScopeSession}, nil
	}

	if matchesNetworkRule(e.config.ProjectApprovedNet, host, port) {
		e.mu.Unlock()
		return Decision{Kind: RequestKindNetwork, Allowed: true, Source: DecisionSourceProjectApproval, Scope: ApprovalScopeProject}, nil
	}

	if matchesNetworkRule(e.config.AllowedNetwork, host, port) {
		e.mu.Unlock()
		return Decision{Kind: RequestKindNetwork, Allowed: true, Source: DecisionSourceAllowedRule}, nil
	}

	if e.config.StrictNetworkAllowlist {
		e.mu.Unlock()
		return Decision{Kind: RequestKindNetwork, Allowed: false, Source: DecisionSourceStrictAllowlistDeny}, nil
	}

	if request.ApprovalRequired {
		handler := e.handler
		e.mu.Unlock()
		if handler == nil {
			return Decision{Kind: RequestKindNetwork, Allowed: false, Source: DecisionSourceMissingApprovalHandler}, nil
		}

		scope, err := handler.ApproveNetwork(ctx, NetworkRequest{
			Host:             host,
			Port:             request.Port,
			Method:           request.Method,
			Target:           request.Target,
			ApprovalRequired: request.ApprovalRequired,
		})
		if err != nil {
			return Decision{}, fmt.Errorf("request network approval: %w", err)
		}

		e.mu.Lock()
		decision := e.applyNetworkApproval(host, port, scope)
		persister, persistConfig, shouldPersist := e.projectApprovalPersistRequestLocked(decision)
		e.mu.Unlock()
		if shouldPersist {
			if err := persister(persistConfig); err != nil {
				return Decision{}, fmt.Errorf("persist project network approval: %w", err)
			}
		}
		return decision, nil
	}

	e.mu.Unlock()
	return Decision{Kind: RequestKindNetwork, Allowed: true, Source: DecisionSourceDefaultAllow}, nil
}

func (e *Engine) applyFileApproval(path string, mode FileAccessMode, scope ApprovalScope) Decision {
	switch scope {
	case ApprovalScopeOnce:
		return Decision{Kind: RequestKindFile, Allowed: true, Source: DecisionSourceApprovalGrantedOnce, Scope: ApprovalScopeOnce}
	case ApprovalScopeSession:
		e.approvals.files[fileApprovalKey(path, mode)] = struct{}{}
		return Decision{Kind: RequestKindFile, Allowed: true, Source: DecisionSourceApprovalGrantedSession, Scope: ApprovalScopeSession}
	case ApprovalScopeProject:
		e.config.ProjectApprovedFiles = append(e.config.ProjectApprovedFiles, FileRule{
			Path:  path,
			Match: MatchExact,
			Modes: []FileAccessMode{mode},
		})
		return Decision{Kind: RequestKindFile, Allowed: true, Source: DecisionSourceApprovalGrantedProject, Scope: ApprovalScopeProject}
	default:
		return Decision{Kind: RequestKindFile, Allowed: false, Source: DecisionSourceApprovalDenied, Scope: ApprovalScopeDeny}
	}
}

func (e *Engine) applyShellApproval(command string, scope ApprovalScope) Decision {
	switch scope {
	case ApprovalScopeOnce:
		return Decision{Kind: RequestKindShell, Allowed: true, Source: DecisionSourceApprovalGrantedOnce, Scope: ApprovalScopeOnce}
	case ApprovalScopeSession:
		e.approvals.shell[command] = struct{}{}
		return Decision{Kind: RequestKindShell, Allowed: true, Source: DecisionSourceApprovalGrantedSession, Scope: ApprovalScopeSession}
	case ApprovalScopeProject:
		e.config.ProjectApprovedShell = append(e.config.ProjectApprovedShell, ShellRule{
			Command: command,
			Match:   MatchExact,
		})
		return Decision{Kind: RequestKindShell, Allowed: true, Source: DecisionSourceApprovalGrantedProject, Scope: ApprovalScopeProject}
	default:
		return Decision{Kind: RequestKindShell, Allowed: false, Source: DecisionSourceApprovalDenied, Scope: ApprovalScopeDeny}
	}
}

func (e *Engine) applyNetworkApproval(host string, port int, scope ApprovalScope) Decision {
	switch scope {
	case ApprovalScopeOnce:
		return Decision{Kind: RequestKindNetwork, Allowed: true, Source: DecisionSourceApprovalGrantedOnce, Scope: ApprovalScopeOnce}
	case ApprovalScopeSession:
		e.approvals.network[networkApprovalKey(host, port)] = struct{}{}
		return Decision{Kind: RequestKindNetwork, Allowed: true, Source: DecisionSourceApprovalGrantedSession, Scope: ApprovalScopeSession}
	case ApprovalScopeProject:
		rule := NetworkRule{
			Host:  host,
			Match: MatchExact,
		}
		if port > 0 {
			rule.Ports = []int{port}
		}
		e.config.ProjectApprovedNet = append(e.config.ProjectApprovedNet, rule)
		return Decision{Kind: RequestKindNetwork, Allowed: true, Source: DecisionSourceApprovalGrantedProject, Scope: ApprovalScopeProject}
	default:
		return Decision{Kind: RequestKindNetwork, Allowed: false, Source: DecisionSourceApprovalDenied, Scope: ApprovalScopeDeny}
	}
}

func (e *Engine) projectApprovalPersistRequestLocked(decision Decision) (func(Config) error, Config, bool) {
	if decision.Scope != ApprovalScopeProject || e.projectApprovalPersister == nil {
		return nil, Config{}, false
	}
	return e.projectApprovalPersister, cloneConfig(e.config), true
}

func normalizeFilePath(path string) string {
	if path == "" {
		return ""
	}

	return filepath.Clean(path)
}

func fileApprovalKey(path string, mode FileAccessMode) string {
	return string(mode) + "\x00" + path
}

func networkApprovalKey(host string, port int) string {
	if port <= 0 {
		return host
	}
	return fmt.Sprintf("%s\x00%d", host, port)
}

func matchesFileRule(rules []FileRule, path string, mode FileAccessMode) bool {
	path = normalizeFileMatchPath(path)
	for _, rule := range rules {
		if !matchesFilePath(rule.Path, path, rule.Match) {
			continue
		}
		if len(rule.Modes) == 0 {
			return true
		}
		for _, allowedMode := range rule.Modes {
			if allowedMode == mode {
				return true
			}
		}
	}

	return false
}

func matchesFilePath(pattern, target string, kind MatchKind) bool {
	pattern = normalizeFileMatchPath(pattern)
	target = normalizeFileMatchPath(target)
	switch kind {
	case MatchPrefix:
		return filePrefixMatch(pattern, target)
	case MatchSuffix:
		return strings.HasSuffix(target, pattern)
	case MatchExact:
		fallthrough
	default:
		return target == pattern
	}
}

func filePrefixMatch(pattern, target string) bool {
	if pattern == "" {
		return target == ""
	}
	pattern = strings.TrimRight(pattern, "/")
	if pattern == "" {
		return strings.HasPrefix(target, "/")
	}
	return target == pattern || strings.HasPrefix(target, pattern+"/")
}

func normalizeFileMatchPath(path string) string {
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	cleaned = strings.ReplaceAll(cleaned, "\\", "/")
	return strings.TrimRight(cleaned, "/")
}

func matchesShellRule(rules []ShellRule, command string) bool {
	for _, rule := range rules {
		if matchesString(rule.Command, command, rule.Match) {
			return true
		}
	}

	return false
}

func matchesExactShellRule(rules []ShellRule, command string) bool {
	for _, rule := range rules {
		if rule.Match == MatchExact && rule.Command == command {
			return true
		}
	}

	return false
}

func matchesNetworkRule(rules []NetworkRule, host string, port int) bool {
	host = normalizeNetworkHost(host)
	if host == "" {
		return false
	}
	for _, rule := range rules {
		if matchesNetworkRuleHost(rule.Host, host, rule.Match) && networkRulePortMatches(rule.Ports, port) {
			return true
		}
	}

	return false
}

func normalizeNetworkHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimRight(host, ".")
	return host
}

func matchesNetworkRuleHost(pattern, target string, kind MatchKind) bool {
	pattern = normalizeNetworkHost(pattern)
	target = normalizeNetworkHost(target)
	if pattern == "" || target == "" {
		return false
	}

	switch kind {
	case MatchPrefix:
		return strings.HasPrefix(target, pattern)
	case MatchSuffix:
		if strings.HasPrefix(pattern, ".") {
			return strings.HasSuffix(target, pattern)
		}
		return target == pattern || strings.HasSuffix(target, "."+pattern)
	case MatchExact:
		fallthrough
	default:
		return target == pattern
	}
}

func networkRulePortMatches(ports []int, port int) bool {
	if len(ports) == 0 {
		return true
	}
	if port == 0 {
		return false
	}
	for _, allowedPort := range ports {
		if allowedPort <= 0 || allowedPort > 65535 {
			continue
		}
		if allowedPort == port {
			return true
		}
	}
	return false
}

func matchesString(pattern, target string, kind MatchKind) bool {
	switch kind {
	case MatchPrefix:
		return strings.HasPrefix(target, pattern)
	case MatchSuffix:
		return strings.HasSuffix(target, pattern)
	case MatchExact:
		fallthrough
	default:
		return target == pattern
	}
}

func cloneConfig(config Config) Config {
	return Config{
		BlockedFiles:           cloneFileRules(config.BlockedFiles),
		AllowedFiles:           cloneFileRules(config.AllowedFiles),
		ProjectApprovedFiles:   cloneFileRules(config.ProjectApprovedFiles),
		BlockedShell:           append([]ShellRule(nil), config.BlockedShell...),
		ApprovalRequiredShell:  append([]ShellRule(nil), config.ApprovalRequiredShell...),
		AllowedShell:           append([]ShellRule(nil), config.AllowedShell...),
		ProjectApprovedShell:   append([]ShellRule(nil), config.ProjectApprovedShell...),
		BlockedNetwork:         cloneNetworkRules(config.BlockedNetwork),
		AllowedNetwork:         cloneNetworkRules(config.AllowedNetwork),
		ProjectApprovedNet:     cloneNetworkRules(config.ProjectApprovedNet),
		StrictFileAllowlist:    config.StrictFileAllowlist,
		StrictCommandAllowlist: config.StrictCommandAllowlist,
		StrictNetworkAllowlist: config.StrictNetworkAllowlist,
	}
}

func cloneFileRules(rules []FileRule) []FileRule {
	cloned := make([]FileRule, len(rules))
	for i, rule := range rules {
		cloned[i] = FileRule{
			Path:  rule.Path,
			Match: rule.Match,
			Modes: append([]FileAccessMode(nil), rule.Modes...),
		}
	}

	return cloned
}

func cloneNetworkRules(rules []NetworkRule) []NetworkRule {
	cloned := make([]NetworkRule, len(rules))
	for i, rule := range rules {
		cloned[i] = NetworkRule{
			Host:  rule.Host,
			Match: rule.Match,
			Ports: append([]int(nil), rule.Ports...),
		}
	}
	return cloned
}
