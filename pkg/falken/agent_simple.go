package falken

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

var builtinWriteFileTools = []string{
	"write_file",
	"edit_file",
	"multi_edit",
	"apply_patch",
	"delete_file",
}

// AgentConfig configures the simple happy-path Falken agent API.
type AgentConfig struct {
	LLM LLM

	SystemPrompt string

	// ReadDirectory optionally exposes read-only file tools rooted at this directory.
	ReadDirectory string

	// Tools are optional host-owned tools.
	Tools []Tool

	// ToolProviders is an optional advanced escape hatch for custom providers.
	ToolProviders []ToolProvider

	// Events receives session-level runtime events. Nil is a no-op.
	Events EventSink

	Permissions SimplePermissions
}

// SimplePermissions controls optional built-in capabilities for NewAgent.
type SimplePermissions struct {
	AllowReadFiles  bool
	AllowWriteFiles bool
	AllowShell      bool
	AllowNetwork    bool
}

// Agent is the simple high-level wrapper around a started Session.
type Agent struct {
	session *Session
}

// NewAgent creates and starts a simple Falken agent.
func NewAgent(ctx context.Context, config AgentConfig) (*Agent, error) {
	if config.LLM == nil {
		return nil, ErrLLMRequired
	}

	workspaceDir, err := simpleAgentWorkspaceDir(config.ReadDirectory)
	if err != nil {
		return nil, err
	}
	workspaceRoot, err := filepath.Abs(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve workspace root: %w", ErrInvalidConfig, err)
	}
	workspaceRoot = filepath.Clean(workspaceRoot)

	providers := make([]ToolProvider, 0, 1+len(config.ToolProviders))
	if len(config.Tools) != 0 {
		providers = append(providers, StaticToolProvider(config.Tools...))
	}
	providers = append(providers, config.ToolProviders...)

	session, err := New(Config{
		WorkspaceDir:         workspaceRoot,
		ToolProviders:        providers,
		BuiltinTools:         simpleAgentBuiltinTools(config),
		ExecutionDetails:     ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:                  config.LLM,
		BaseSystemPrompt:     config.SystemPrompt,
		PlanRouting:          PlanRoutingDisabled,
		Events:               config.Events,
		Policy:               simpleAgentPolicy(workspaceRoot, config),
		Runtime:              nil,
		StateBackendProvider: NewInMemoryStateBackendProvider(),
	})
	if err != nil {
		return nil, err
	}
	if err := session.StartContext(ctx); err != nil {
		return nil, err
	}

	return &Agent{session: session}, nil
}

// Run executes one prompt and returns the final assistant text.
func (a *Agent) Run(ctx context.Context, prompt string) (string, error) {
	result, err := a.RunRequest(ctx, RunRequest{Prompt: prompt})
	if err != nil {
		return "", err
	}
	return result.FinalOutput, nil
}

// RunRequest delegates one request to the underlying started session.
func (a *Agent) RunRequest(ctx context.Context, request RunRequest) (RunResult, error) {
	if a == nil || a.session == nil {
		return RunResult{Error: ErrSessionClosed.Error()}, ErrSessionClosed
	}
	return a.session.Run(ctx, request)
}

// Close closes the underlying session. It is safe to call more than once.
func (a *Agent) Close(ctx context.Context) error {
	if a == nil || a.session == nil {
		return nil
	}
	return a.session.CloseContext(ctx)
}

func simpleAgentWorkspaceDir(readDirectory string) (string, error) {
	if readDirectory != "" {
		return readDirectory, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("%w: resolve current working directory: %w", ErrInvalidConfig, err)
	}
	return wd, nil
}

func simpleAgentBuiltinTools(config AgentConfig) BuiltinToolsConfig {
	names := simpleAgentBuiltinToolNames(config)
	if len(names) == 0 {
		return BuiltinToolsConfig{Mode: BuiltinToolsNone}
	}
	return BuiltinToolsConfig{Mode: BuiltinToolsSelected, Names: names}
}

func simpleAgentBuiltinToolNames(config AgentConfig) []string {
	permissions := config.Permissions
	readFiles := permissions.AllowReadFiles || config.ReadDirectory != ""
	writeFiles := permissions.AllowWriteFiles
	shell := permissions.AllowShell

	var names []string
	if readFiles {
		names = append(names, BuiltinReadOnlyFileTools...)
	}
	if writeFiles {
		names = append(names, builtinWriteFileTools...)
	}
	if shell {
		names = append(names, "execute_command")
	}
	return names
}

func simpleAgentPolicy(workspaceRoot string, config AgentConfig) PolicyConfig {
	permissions := config.Permissions
	readFiles := permissions.AllowReadFiles || config.ReadDirectory != ""
	writeFiles := permissions.AllowWriteFiles
	shell := permissions.AllowShell

	policy := PolicyConfig{
		StrictNetworkAllowlist: !permissions.AllowNetwork,
	}

	var blockedModes []FileAccessMode
	if !readFiles {
		blockedModes = append(blockedModes, FileAccessRead)
	}
	if !writeFiles {
		blockedModes = append(blockedModes, FileAccessWrite, FileAccessCreate)
	}
	if len(blockedModes) != 0 {
		policy.BlockedFiles = []FileRule{{
			Path:  workspaceRoot,
			Match: MatchPrefix,
			Modes: blockedModes,
		}}
	}
	if !shell {
		policy.BlockedShell = []ShellRule{{
			Command: "",
			Match:   MatchPrefix,
		}}
	}
	return policy
}
