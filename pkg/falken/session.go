package falken

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/extensions"
	"github.com/smasonuk/falken-core/internal/host"
	"github.com/smasonuk/falken-core/internal/network"
	"github.com/smasonuk/falken-core/internal/permissions"
	"github.com/smasonuk/falken-core/internal/runtimeapi"
)

// Session is an embeddable Falken runtime instance.
type Session struct {
	cfg     Config
	paths   runtimeapi.Paths
	runner  *agent.Runner
	shell   *host.StatefulShell
	permMgr *permissions.Manager
	proxy   *network.SandboxProxy
	tools   []any
	plugins []extensions.WasmHook
	toolRT  extensions.ResourceSet
	plugRT  extensions.ResourceSet
	started bool
}

// NewSession constructs a new embeddable runtime session from cfg.
func NewSession(cfg Config) (*Session, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("client is required")
	}

	paths, err := runtimeapi.NewPaths(cfg.WorkspaceDir, cfg.StateDir)
	if err != nil {
		return nil, err
	}
	if err := paths.EnsureStateDirs(); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	if cfg.PermissionsConfig == nil {
		cfg.PermissionsConfig, err = LoadPermissionsConfigFromPath(filepath.Join(paths.WorkspaceDir, ".falken.yaml"))
		if err != nil {
			return nil, err
		}
	}
	cfg.PermissionsConfig.ensureDefaults()

	internalPermCfg := toInternalPermissionsConfig(cfg.PermissionsConfig)
	cfg.PermissionsConfig = fromInternalPermissionsConfig(internalPermCfg)

	permMgr := permissions.NewManager(internalPermCfg, cfg.InteractionHandler)
	permMgr.ConfigPath = filepath.Join(paths.WorkspaceDir, ".falken.yaml")
	shell := host.NewStatefulShell(paths.WorkspaceDir, paths.StateDir, permMgr, logger)

	toolDir := cfg.ToolDir
	if toolDir == "" {
		toolDir = filepath.Join(paths.WorkspaceDir, "tools")
	} else if !filepath.IsAbs(toolDir) {
		toolDir = filepath.Join(paths.WorkspaceDir, toolDir)
	}
	pluginDir := cfg.PluginDir
	if pluginDir == "" {
		pluginDir = filepath.Join(paths.WorkspaceDir, "plugins")
	} else if !filepath.IsAbs(pluginDir) {
		pluginDir = filepath.Join(paths.WorkspaceDir, pluginDir)
	}

	sandboxImage := cfg.SandboxImage
	if sandboxImage == "" {
		sandboxImage = cfg.PermissionsConfig.SandboxImage
	}
	if sandboxImage == "" {
		sandboxImage = "falken/sandbox:latest"
	}

	cfg.ToolDir = toolDir
	cfg.PluginDir = pluginDir
	cfg.SandboxImage = sandboxImage

	tools, toolRT, err := extensions.LoadTools(toolDir, shell)
	if err != nil {
		return nil, err
	}

	plugins, plugRT, err := extensions.LoadPlugins(pluginDir, shell)
	if err != nil {
		_ = shell.Close(context.Background())
		_ = toolRT.Close(context.Background())
		return nil, err
	}

	runner, err := agent.NewRunner(agent.RunnerOptions{
		Client:             cfg.Client,
		ModelName:          cfg.ModelName,
		Tools:              tools,
		SystemPrompt:       cfg.SystemPrompt,
		Logger:             logger,
		Shell:              shell,
		Paths:              paths,
		InteractionHandler: cfg.InteractionHandler,
		ToolDir:            toolDir,
		PluginDir:          pluginDir,
		SandboxImage:       sandboxImage,
	})
	if err != nil {
		_ = shell.Close(context.Background())
		_ = plugRT.Close(context.Background())
		_ = toolRT.Close(context.Background())
		return nil, err
	}

	return &Session{
		cfg:     cfg,
		paths:   paths,
		runner:  runner,
		shell:   shell,
		permMgr: permMgr,
		tools:   tools,
		plugins: plugins,
		toolRT:  toolRT,
		plugRT:  plugRT,
	}, nil
}

// Start initializes session-owned runtime infrastructure such as the proxy and sandbox.
func (s *Session) Start(ctx context.Context) error {
	if s.started {
		return nil
	}

	proxy, err := network.NewSandboxProxy("0", s.permMgr, s.paths.CacheDir())
	if err != nil {
		return err
	}
	if err := proxy.Start(); err != nil {
		return err
	}
	s.shell.ProxyPort = proxy.Port

	if err := host.ResetRuntimeState(s.paths); err != nil {
		return err
	}

	imageName := s.cfg.SandboxImage

	if err := s.shell.StartSandbox(ctx, imageName); err != nil {
		return err
	}

	s.proxy = proxy
	s.started = true
	return nil
}

// Run executes a prompt against the session runtime.
func (s *Session) Run(ctx context.Context, prompt string) error {
	internalEvents := make(chan any, 100)
	done := make(chan error, 1)

	go func() {
		done <- s.runner.Run(ctx, prompt, internalEvents)
	}()

	for evt := range internalEvents {
		if translated := translateEvent(evt); translated.Type != "" {
			s.emit(translated)
		}
	}

	return <-done
}

// GenerateDiff returns the current diff between the workspace and sandbox state.
func (s *Session) GenerateDiff() (string, error) {
	blockedFiles := []string{}
	if s.cfg.PermissionsConfig != nil {
		blockedFiles = s.cfg.PermissionsConfig.GlobalBlockedFiles
	}
	return host.GenerateDiff(s.paths.WorkspaceDir, s.shell.SandboxCWD, blockedFiles)
}

// ApplyDiff applies a reviewed diff from the sandbox back to the workspace.
func (s *Session) ApplyDiff(diff string) (DiffApplyResult, error) {
	blockedFiles := []string{}
	if s.cfg.PermissionsConfig != nil {
		blockedFiles = s.cfg.PermissionsConfig.GlobalBlockedFiles
	}
	result, err := host.ApplyChanges(s.paths.WorkspaceDir, s.shell.SandboxCWD, diff, blockedFiles)
	if err != nil {
		return DiffApplyResult{}, err
	}
	return DiffApplyResult{
		Partial:      result.GuardrailTriggered,
		SkippedFiles: result.SkippedFiles,
	}, nil
}

// Close releases session-owned resources.
func (s *Session) Close(ctx context.Context) error {
	s.started = false
	var errs []error
	if s.proxy != nil {
		errs = append(errs, s.proxy.Close(ctx))
	}
	if s.shell != nil {
		errs = append(errs, s.shell.Close(ctx))
	}
	errs = append(errs, closeResourceSet(ctx, s.toolRT))
	errs = append(errs, closeResourceSet(ctx, s.plugRT))
	return errors.Join(errs...)
}

// Paths returns the resolved path configuration for the session.
func (s *Session) Paths() Paths {
	return s.paths
}

// ClearHistory clears the session's conversation history.
func (s *Session) ClearHistory() {
	if s.runner != nil {
		s.runner.ClearHistory()
	}
}

// ResetConversationState clears in-memory and on-disk conversation state so the next run
// starts fresh. This intentionally removes history, memory, todos, and task queue state
// because all of them can affect the next prompt or workflow.
func (s *Session) ResetConversationState() error {
	s.ClearHistory()

	var errs []error
	for _, path := range []string{
		s.paths.HistoryPath(),
		s.paths.MemoryPath(),
		s.paths.TodosPath(),
		s.paths.TasksPath(),
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	if err := os.RemoveAll(s.paths.TasksDir()); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// ForcePlanMode puts the underlying runner into plan mode.
// This is primarily intended for host frontends such as the TUI.
func (s *Session) ForcePlanMode(userInitiated bool) {
	s.runner.Mode = agent.ModePlan
	if userInitiated {
		s.runner.PlanInitiator = agent.PlanInitiatorUser
		return
	}
	s.runner.PlanInitiator = agent.PlanInitiatorAgent
}

// PermissionsConfig returns the active permissions configuration for the session.
func (s *Session) PermissionsConfig() *PermissionsConfig {
	return s.cfg.PermissionsConfig
}

// ToolInfos returns metadata for the tools loaded into the session.
func (s *Session) ToolInfos() []ToolInfo {
	infos := make([]ToolInfo, 0, len(s.tools))
	for _, tool := range s.tools {
		if named, ok := tool.(interface{ Name() string }); ok {
			infos = append(infos, ToolInfo{Name: named.Name()})
		}
	}
	return infos
}

// PluginInfos returns metadata for the plugins discovered for the session.
func (s *Session) PluginInfos() []PluginInfo {
	type aggregate struct {
		info         PluginInfo
		networkSet   map[string]struct{}
		shellSet     map[string]struct{}
		filePermsSet map[string]struct{}
	}

	byName := make(map[string]*aggregate, len(s.plugins))
	for _, plugin := range s.plugins {
		entry, ok := byName[plugin.PluginName]
		if !ok {
			entry = &aggregate{
				info: PluginInfo{
					Name:        plugin.PluginName,
					Description: plugin.Description,
					Internal:    plugin.Internal,
				},
				networkSet:   make(map[string]struct{}),
				shellSet:     make(map[string]struct{}),
				filePermsSet: make(map[string]struct{}),
			}
			byName[plugin.PluginName] = entry
		}

		for _, rule := range plugin.Permissions.Network {
			if rule.URL != "" {
				entry.networkSet[rule.URL] = struct{}{}
			}
			if rule.Domain != "" {
				entry.networkSet[rule.Domain] = struct{}{}
			}
		}
		for _, command := range plugin.Permissions.Shell {
			entry.shellSet[command] = struct{}{}
		}
		for _, rule := range plugin.Permissions.Files {
			entry.filePermsSet[fmt.Sprintf("%s (%s)", rule.Path, rule.Access)] = struct{}{}
		}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	infos := make([]PluginInfo, 0, len(byName))
	for _, name := range names {
		entry := byName[name]
		entry.info.NetworkTargets = sortedKeys(entry.networkSet)
		entry.info.ShellCommands = sortedKeys(entry.shellSet)
		entry.info.FilePermissions = sortedKeys(entry.filePermsSet)
		infos = append(infos, entry.info)
	}
	return infos
}

func sortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

func closeResourceSet(ctx context.Context, resources extensions.ResourceSet) error {
	if resources == nil {
		return nil
	}
	return resources.Close(ctx)
}

func (s *Session) emit(event Event) {
	if s.cfg.EventHandler != nil {
		s.cfg.EventHandler.OnEvent(event)
	}
}

func translateEvent(msg any) Event {
	switch m := msg.(type) {
	case agent.AgentThoughtMsg:
		return Event{Type: EventTypeThought, Thought: &ThoughtEvent{Text: m.Text}}
	case agent.AgentTextMsg:
		return Event{Type: EventTypeAssistantText, AssistantText: &AssistantTextEvent{Text: m.Text}}
	case agent.ToolCallMsg:
		return Event{Type: EventTypeToolCall, ToolCall: &ToolCallEvent{Name: m.Name, Args: m.Args}}
	case agent.ToolResultMsg:
		return Event{Type: EventTypeToolResult, ToolResult: &ToolResultEvent{Name: m.Name, Result: m.Result}}
	case agent.CommandStreamMsg:
		return Event{Type: EventTypeCommandChunk, CommandChunk: &CommandChunkEvent{Chunk: m.Chunk}}
	case agent.AgentDoneMsg:
		if m.Error != nil && !errors.Is(m.Error, context.Canceled) {
			return Event{Type: EventTypeRunFailed, RunFailed: &RunFailedEvent{Error: m.Error}}
		}
		return Event{Type: EventTypeRunCompleted, RunCompleted: &RunCompletedEvent{}}
	default:
		return Event{}
	}
}
