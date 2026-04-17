package permissions

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/smasonuk/falken-core/internal/runtimeapi"
)

// PermScope controls how long an approval decision should be retained.
type PermScope string

const (
	// ScopeOnce applies only to the in-flight request.
	ScopeOnce PermScope = "once"
	// ScopeSession applies until the current session ends.
	ScopeSession PermScope = "session"
	// ScopeProject persists the decision into the project config.
	ScopeProject PermScope = "project"
	// ScopeDeny explicitly rejects the request.
	ScopeDeny PermScope = "deny"
)

// PermResponse is the internal permission decision returned to waiting callers.
type PermResponse struct {
	Allow      bool
	Scope      PermScope
	AccessType string
}

// PermRequest describes an access request sent through the permission workflow.
type PermRequest struct {
	ReqType    string // "file", "shell", or "network"
	Target     string // The file path, shell command, or URL
	AccessType string
	Result     chan PermResponse
}

// Manager enforces file, shell, and network access policy for host and Wasm tools.
type Manager struct {
	sessionApprovals map[string]string // Maps target/path to access type
	Config           *Config
	ConfigPath       string
	Handler          runtimeapi.InteractionHandler
	mu               sync.RWMutex
}

// NewManager constructs a permission manager backed by config and an interaction handler.
func NewManager(config *Config, handler runtimeapi.InteractionHandler) *Manager {
	if config == nil {
		config = &Config{}
	}
	config.ensureDefaults()
	return &Manager{
		sessionApprovals: make(map[string]string),
		Config:           config,
		ConfigPath:       ".falken.yaml",
		Handler:          handler,
	}
}

// MatchPattern applies the project's glob-like permission matching rules.
// `*` matches within a single path segment and `**` matches across separators.
func MatchPattern(pattern, target string) bool {
	// Escape standard regex characters, then convert literal \* back to regex .*
	regexStr := regexp.QuoteMeta(pattern)
	regexStr = strings.ReplaceAll(regexStr, "\\*\\*", ".*") // Handle **
	regexStr = strings.ReplaceAll(regexStr, "\\*", "[^/]*") // Handle * (non-recursive)
	regexStr = "^" + regexStr + "$"

	matched, _ := regexp.MatchString(regexStr, target)
	return matched
}

// CheckNetworkAccess decides whether a URL may be fetched by the sandbox.
func (m *Manager) CheckNetworkAccess(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	domain := ""
	if err == nil {
		domain = parsed.Hostname()
	} else {
		domain = rawURL // fallback
	}

	m.mu.RLock()
	// Explicit Block
	for _, blocked := range m.Config.GlobalBlockedURLs {
		if MatchPattern(blocked, rawURL) || MatchPattern(blocked, domain) {
			m.mu.RUnlock()
			return false
		}
	}
	// Explicit Allow
	for _, allowed := range m.Config.GlobalAllowedURLs {
		if MatchPattern(allowed, rawURL) || MatchPattern(allowed, domain) {
			m.mu.RUnlock()
			return true
		}
	}
	for _, allowed := range m.Config.PersistentAllowedURLs {
		if MatchPattern(allowed, rawURL) || MatchPattern(allowed, domain) {
			m.mu.RUnlock()
			return true
		}
	}

	// Session Cache
	if m.sessionApprovals["net_url:"+rawURL] == "allow" || m.sessionApprovals["net_domain:"+domain] == "allow" {
		m.mu.RUnlock()
		return true
	}
	m.mu.RUnlock()

	if m.Handler == nil {
		return false // Failsafe deny
	}

	resp, err := m.Handler.RequestPermission(context.Background(), runtimeapi.PermissionRequest{
		Kind:       "network",
		Target:     rawURL,
		AccessType: domain,
	})
	if err != nil || !resp.Allowed {
		return false
	}

	targetToSave := rawURL
	cacheKeyPrefix := "net_url:"
	if resp.AccessType == "domain" {
		targetToSave = domain
		cacheKeyPrefix = "net_domain:"
	}

	switch PermScope(resp.Scope) {
	case ScopeSession:
		m.mu.Lock()
		m.sessionApprovals[cacheKeyPrefix+targetToSave] = "allow"
		m.mu.Unlock()
	case ScopeProject:
		m.persistConfig(func(cfg *Config) bool {
			return cfg.AddPersistentAllowedURL(targetToSave)
		})
	}

	return true
}

// CheckShellAccess decides whether a shell command may be executed.
func (m *Manager) CheckShellAccess(toolName, command, reason string, allowedCommands []string) bool {
	baseCmd := strings.Split(strings.TrimSpace(command), " ")[0]

	m.mu.RLock()
	// Explicit Block
	for _, blocked := range m.Config.GlobalBlockedCommands {
		if MatchPattern(blocked, command) {
			m.mu.RUnlock()
			return false
		}
	}
	// Explicit Allow
	for _, allowed := range m.Config.GlobalAllowedCommands {
		if MatchPattern(allowed, command) {
			m.mu.RUnlock()
			return true
		}
	}
	for _, allowed := range m.Config.PersistentAllowedCommands {
		if MatchPattern(allowed, command) {
			m.mu.RUnlock()
			return true
		}
	}
	allowListPopulated := m.Config.StrictCommandAllowlist && len(m.Config.GlobalAllowedCommands) > 0
	sessionAllowed := m.sessionApprovals["shell:"+baseCmd] == "allow"
	m.mu.RUnlock()

	// Legacy/Plugin allowed commands
	for _, prefix := range allowedCommands {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}

	// Implicit Deny (If allow list is populated, block everything else)
	if allowListPopulated {
		return false
	}

	// Implicit Allow (Session Cache + User Prompt)
	if sessionAllowed {
		return true
	}

	if m.Handler == nil {
		return false // Failsafe deny
	}

	resp, err := m.Handler.RequestPermission(context.Background(), runtimeapi.PermissionRequest{
		Kind:       "shell",
		Target:     command,
		AccessType: "execute",
	})
	if err != nil || !resp.Allowed {
		return false
	}

	switch PermScope(resp.Scope) {
	case ScopeSession:
		m.mu.Lock()
		m.sessionApprovals["shell:"+baseCmd] = "allow"
		m.mu.Unlock()
	case ScopeProject:
		m.persistConfig(func(cfg *Config) bool {
			return cfg.AddPersistentAllowedCommand(baseCmd)
		})
	}

	return true
}

// CheckFileAccess decides whether a file operation may proceed for the given path and mode.
func (m *Manager) CheckFileAccess(toolName, path, accessType, reason string) bool {
	m.mu.RLock()
	// Explicit Block
	for _, blocked := range m.Config.GlobalBlockedFiles {
		if MatchPattern(blocked, path) {
			m.mu.RUnlock()
			return false
		}
	}
	// Default hardcoded blocked files (security)
	for _, blocked := range DefaultBlockedFiles {
		if MatchPattern(blocked, path) {
			m.mu.RUnlock()
			return false
		}
	}

	// Explicit Allow
	for _, allowed := range m.Config.GlobalAllowedFiles {
		if MatchPattern(allowed, path) {
			m.mu.RUnlock()
			return true
		}
	}
	if grantedAccess, ok := m.Config.PersistentAllowedFiles[path]; ok {
		if grantedAccess == "read/write" || accessType == "read" {
			m.mu.RUnlock()
			return true
		}
	}

	// Only exempt the agent's scratchpad.
	if strings.HasSuffix(path, ".falken/state.md") {
		m.mu.RUnlock()
		return true
	}

	// Implicit Deny (If allow list is populated, block everything else)
	allowListPopulated := m.Config.StrictFileAllowlist && len(m.Config.GlobalAllowedFiles) > 0
	if allowListPopulated {
		m.mu.RUnlock()
		return false
	}

	// Implicit Allow (Session Cache + User Prompt)
	// Session Cache
	if grantedAccess, ok := m.sessionApprovals[path]; ok {
		if grantedAccess == "read/write" || accessType == "read" {
			m.mu.RUnlock()
			return true
		}
	}
	m.mu.RUnlock()

	if m.Handler == nil {
		return false
	}

	resp, err := m.Handler.RequestPermission(context.Background(), runtimeapi.PermissionRequest{
		Kind:       "file",
		Target:     path,
		AccessType: accessType,
	})
	if err != nil || !resp.Allowed {
		return false
	}

	switch PermScope(resp.Scope) {
	case ScopeSession:
		m.mu.Lock()
		m.sessionApprovals[path] = resp.AccessType
		m.mu.Unlock()
	case ScopeProject:
		m.persistConfig(func(cfg *Config) bool {
			return cfg.SetPersistentAllowedFile(path, resp.AccessType)
		})
	}

	return true
}

func (m *Manager) persistConfig(update func(*Config) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Config == nil {
		m.Config = &Config{}
	}
	m.Config.ensureDefaults()
	if !update(m.Config) {
		return
	}
	_ = SaveConfigToPath(m.ConfigPath, m.Config)
}
