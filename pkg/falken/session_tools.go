package falken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/store"
)

// providerToolRef associates a tool entry with its providing source.
type providerToolRef struct {
	provider  ToolProvider
	entry     tools.Entry
	namespace string
}

// sessionToolHub manages the registration and execution of external provider tools.
type sessionToolHub struct {
	providers      []ToolProvider
	builtinConfig  BuiltinToolsConfig
	builtinsByName map[string]struct{}
	toolsByName    map[string]providerToolRef
	started        []ToolProvider
	active         []tools.Entry
	host           sessionToolHost

	remoteWorkspaceMode             bool
	allowWorkspaceToolsInRemoteMode bool
}

// newSessionToolHub initializes a hub for the given tool providers.
func newSessionToolHub(builtinConfig BuiltinToolsConfig, providers ...ToolProvider) *sessionToolHub {
	filtered := make([]ToolProvider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			filtered = append(filtered, provider)
		}
	}
	return &sessionToolHub{
		providers:      filtered,
		builtinConfig:  cloneBuiltinToolsConfig(builtinConfig),
		builtinsByName: make(map[string]struct{}),
		toolsByName:    make(map[string]providerToolRef),
	}
}

// start registers both built-in and external provider tools.
func (h *sessionToolHub) start(ctx context.Context, host sessionToolHost) error {
	if h == nil {
		return nil
	}
	h.host = host

	seen := make(map[string]string)
	active, err := builtinToolEntriesFor(h.builtinConfig)
	if err != nil {
		return err
	}
	for _, entry := range active {
		if err := registerToolName(seen, entry.Name, "built-in tool"); err != nil {
			return err
		}
		h.builtinsByName[entry.Name] = struct{}{}
	}

	for index, provider := range h.providers {
		providerNamespace := stableProviderNamespace(index, provider)
		startupSafety := providerStartupSafety(provider)
		if err := h.validateRemoteWorkspaceToolSafety(providerNamespace+" startup", startupSafety.ReadsWorkspace, startupSafety.MutatesWorkspace); err != nil {
			return err
		}
		providerHost := newProviderStartupToolHost(host, providerNamespace, startupSafety)
		if err := provider.Start(ctx, providerHost); err != nil {
			if closeErr := h.close(ctx); closeErr != nil {
				return errors.Join(err, fmt.Errorf("cleanup started tool providers: %w", closeErr))
			}
			return err
		}
		h.started = append(h.started, provider)

		descriptors, err := provider.Tools(ctx)
		if err != nil {
			if closeErr := h.close(ctx); closeErr != nil {
				return errors.Join(err, fmt.Errorf("cleanup started tool providers: %w", closeErr))
			}
			return err
		}
		for _, descriptor := range descriptors {
			entry, err := toolEntryFromDescriptor(descriptor)
			if err != nil {
				if closeErr := h.close(ctx); closeErr != nil {
					return errors.Join(err, fmt.Errorf("cleanup started tool providers: %w", closeErr))
				}
				return err
			}
			if err := h.validateRemoteWorkspaceToolSafety("provider tool "+entry.Name, entry.Safety.ReadsWorkspace, entry.Safety.MutatesWorkspace); err != nil {
				if closeErr := h.close(ctx); closeErr != nil {
					return errors.Join(err, fmt.Errorf("cleanup started tool providers: %w", closeErr))
				}
				return err
			}
			if err := registerToolName(seen, entry.Name, "provider tool"); err != nil {
				if closeErr := h.close(ctx); closeErr != nil {
					return errors.Join(err, fmt.Errorf("cleanup started tool providers: %w", closeErr))
				}
				return err
			}
			h.toolsByName[entry.Name] = providerToolRef{provider: provider, entry: entry, namespace: providerNamespace + "/tool:" + entry.Name}
			active = append(active, entry)
		}
	}

	h.active = tools.CloneEntries(active)
	return nil
}

func (h *sessionToolHub) validateRemoteWorkspaceToolSafety(label string, readsWorkspace, mutatesWorkspace bool) error {
	if h == nil || !h.remoteWorkspaceMode || h.allowWorkspaceToolsInRemoteMode {
		return nil
	}
	if !readsWorkspace && !mutatesWorkspace {
		return nil
	}
	return fmt.Errorf("%s declares workspace file access and cannot run in remote workspace mode; use managed workspace file operations or set AllowWorkspaceToolsInRemoteMode explicitly", label)
}

// activeTools returns the full list of currently available tools.
func (h *sessionToolHub) activeTools(context.Context) ([]tools.Entry, error) {
	if h == nil {
		return builtinToolEntries(), nil
	}
	return tools.CloneEntries(h.active), nil
}

// execute delegates a tool request to the appropriate provider or built-in executor.
func (h *sessionToolHub) execute(ctx context.Context, request agent.ToolExecutionRequest, builtins sessionToolExecutor) (agent.ToolExecutionResult, error) {
	if _, ok := h.builtinsByName[request.Name]; ok {
		return builtins.executeBuiltinTool(ctx, request)
	}
	if ref, ok := h.toolsByName[request.Name]; ok {
		invocation := ToolInvocation{
			CallID:    request.CallID,
			Name:      request.Name,
			Arguments: append(json.RawMessage(nil), request.Arguments...),
		}
		providerHost := newScopedToolHost(
			h.host,
			ref.namespace,
			toolSafetyFromInternal(ref.entry.Safety),
			combinePublicEventSinks(h.host.events, publicEventSinkFromAgent(request.Events)),
		)
		var (
			result ToolExecutionResult
			err    error
		)
		if scoped, ok := ref.provider.(ScopedToolProvider); ok {
			result, err = scoped.ExecuteToolWithHost(ctx, invocation, providerHost)
		} else {
			result, err = ref.provider.ExecuteTool(ctx, invocation)
		}
		if err != nil {
			return agent.ToolExecutionResult{}, err
		}
		return agent.ToolExecutionResult{
			Success: result.Success,
			Status:  result.Status,
			Content: result.Content,
			Payload: append(json.RawMessage(nil), result.Payload...),
			Error:   result.Error,
		}, nil
	}
	return agent.ToolExecutionResult{}, errUnknownProviderTool(request.Name)
}

// close shuts down all started tool providers.
func (h *sessionToolHub) close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	var closeErr error
	for i := len(h.started) - 1; i >= 0; i-- {
		if err := h.started[i].Close(ctx); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	h.started = nil
	return closeErr
}

// registerToolName ensures tool names are unique across all providers.
func registerToolName(seen map[string]string, name, source string) error {
	if name == "" {
		return fmt.Errorf("tool from %s has empty name", source)
	}
	if previous, ok := seen[name]; ok {
		return fmt.Errorf("duplicate tool name %q from %s conflicts with %s", name, source, previous)
	}
	seen[name] = source
	return nil
}

// toolEntryFromDescriptor converts a public ToolDescriptor into an internal tools.Entry.
func toolEntryFromDescriptor(descriptor ToolDescriptor) (tools.Entry, error) {
	descriptor = cloneToolDescriptor(descriptor)
	if err := ValidateToolDescriptor(descriptor); err != nil {
		return tools.Entry{}, err
	}
	return tools.Entry{
		Name:        descriptor.Name,
		Description: descriptor.Description,
		InputSchema: append([]byte(nil), descriptor.Parameters...),
		Category:    descriptor.Category,
		Keywords:    append([]string(nil), descriptor.Keywords...),
		AlwaysLoad:  descriptor.AlwaysLoad,
		DefaultLoad: descriptor.DefaultLoad,
		PackageName: "provider",
		PackageDesc: "Host-provided tools",
		Safety:      internalToolSafetyFromDescriptor(descriptor.Safety),
	}, nil
}

// internalToolSafetyFromDescriptor maps public ToolSafety flags to the internal tools.Safety structure.
func internalToolSafetyFromDescriptor(safety ToolSafety) tools.Safety {
	return tools.Safety{
		PlanSafe:         safety.PlanSafe,
		ReadsWorkspace:   safety.ReadsWorkspace,
		MutatesWorkspace: safety.MutatesWorkspace,
		ExecutesShell:    safety.ExecutesShell,
		UsesNetwork:      safety.UsesNetwork,
		UsesHostState:    safety.UsesHostState,
		ReadsHostState:   safety.ReadsHostState || safety.UsesHostState,
		MutatesHostState: safety.MutatesHostState || safety.UsesHostState,
	}
}

// errUnknownProviderTool generates an error for an unrecognized tool name.
func errUnknownProviderTool(name string) error {
	return fmt.Errorf("tool is not active or unknown: %s", name)
}

// stableProviderNamespace computes a deterministic identifier for a ToolProvider.
func stableProviderNamespace(index int, provider ToolProvider) string {
	if provider == nil {
		return fmt.Sprintf("provider:%d:nil", index)
	}
	return fmt.Sprintf("provider:%T:%d", provider, index)
}

func providerStartupSafety(provider any) ToolSafety {
	if scoped, ok := provider.(StartupCapabilityProvider); ok {
		return scoped.StartupSafety()
	}
	return ToolSafety{}
}

// sessionToolHost provides contextual runtime services to executing tools.
type sessionToolHost struct {
	layout           state.Layout
	runtime          *sessionRuntime
	commandEvidence  store.CommandEvidenceBackend
	events           EventSink
	namespace        string
	safety           ToolSafety
	capabilityScoped bool
}

// WorkspaceRoot returns the absolute path to the workspace root.
func (h sessionToolHost) WorkspaceRoot() string { return h.layout.WorkspaceRoot }

// StateRoot returns the absolute path to the session state root.
func (h sessionToolHost) StateRoot() string { return h.layout.StateRoot }

// CurrentWorkingDir returns the runtime's current working directory.
func (h sessionToolHost) CurrentWorkingDir() string {
	if h.runtime == nil || h.runtime.executionState == nil {
		return h.layout.WorkspaceRoot
	}
	return h.runtime.executionState.CurrentWorkingDir()
}

// Emit dispatches an event to the session's event sink.
func (h sessionToolHost) Emit(event Event) {
	if h.events != nil {
		h.events(event)
	}
}

// newSessionToolHost constructs a base ToolHost with full access.
func newSessionToolHost(layout state.Layout, runtime *sessionRuntime, commandEvidence store.CommandEvidenceBackend, events EventSink) sessionToolHost {
	return sessionToolHost{
		layout:          layout,
		runtime:         runtime,
		commandEvidence: commandEvidence,
		events:          events,
	}
}

// newProviderStartupToolHost constructs a ToolHost for provider startup.
func newProviderStartupToolHost(base sessionToolHost, namespace string, safety ToolSafety) sessionToolHost {
	return sessionToolHost{
		layout:           base.layout,
		runtime:          base.runtime,
		commandEvidence:  base.commandEvidence,
		events:           base.events,
		namespace:        namespace,
		safety:           safety,
		capabilityScoped: true,
	}
}

// newScopedToolHost constructs a capability-restricted ToolHost for tool execution.
func newScopedToolHost(base sessionToolHost, namespace string, safety ToolSafety, events EventSink) sessionToolHost {
	return sessionToolHost{
		layout:           base.layout,
		runtime:          base.runtime,
		commandEvidence:  base.commandEvidence,
		events:           events,
		namespace:        namespace,
		safety:           safety,
		capabilityScoped: true,
	}
}

// combinePublicEventSinks merges two EventSinks into one.
func combinePublicEventSinks(first, second EventSink) EventSink {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func(event Event) {
		first(event)
		second(event)
	}
}

// publicEventSinkFromAgent adapts an internal agent.EventSink into a public EventSink.
func publicEventSinkFromAgent(sink agent.EventSink) EventSink {
	if sink == nil {
		return nil
	}
	return func(event Event) {
		sink(toAgentEvent(event))
	}
}

// toAgentEvent translates a public Event into an internal agent.Event.
func toAgentEvent(event Event) agent.Event {
	var toolCall *agent.ToolCall
	if event.ToolCall != nil {
		converted := toAgentToolCall(*event.ToolCall)
		toolCall = &converted
	}
	var toolResult *agent.ToolResult
	if event.ToolResult != nil {
		converted := agent.ToolResult{
			CallID:  event.ToolResult.CallID,
			Name:    event.ToolResult.Name,
			Content: event.ToolResult.Content,
			Payload: append(json.RawMessage(nil), event.ToolResult.Payload...),
			Error:   event.ToolResult.Error,
		}
		toolResult = &converted
	}
	var commandChunk *runtimeexec.StreamChunk
	if event.CommandChunk != nil {
		commandChunk = &runtimeexec.StreamChunk{
			Stream: runtimeexec.StreamName(event.CommandChunk.Stream),
			Data:   append([]byte(nil), event.CommandChunk.Data...),
		}
	}
	var planRouting *agent.PlanRoutingDecisionEvent
	if event.PlanRouting != nil {
		planRouting = &agent.PlanRoutingDecisionEvent{
			RequiresPlan:  event.PlanRouting.RequiresPlan,
			RequiresTodos: event.PlanRouting.RequiresTodos,
			Reason:        event.PlanRouting.Reason,
			Confidence:    event.PlanRouting.Confidence,
			Signals:       append([]string(nil), event.PlanRouting.Signals...),
			Source:        event.PlanRouting.Source,
		}
	}
	var runResult *agent.RunResult
	if event.RunResult != nil {
		runResult = &agent.RunResult{
			FinalOutput: event.RunResult.FinalOutput,
			Completed:   event.RunResult.Completed,
			Error:       event.RunResult.Error,
		}
	}
	return agent.Event{
		Type:         agent.EventType(event.Type),
		Text:         event.Text,
		ToolCall:     toolCall,
		ToolResult:   toolResult,
		CommandChunk: commandChunk,
		PlanRouting:  planRouting,
		RunResult:    runResult,
		Error:        event.Error,
	}
}

// toolSafetyFromInternal maps internal tools.Safety to public ToolSafety.
func toolSafetyFromInternal(safety tools.Safety) ToolSafety {
	return ToolSafety{
		PlanSafe:         safety.PlanSafe,
		ReadsWorkspace:   safety.ReadsWorkspace,
		MutatesWorkspace: safety.MutatesWorkspace,
		ExecutesShell:    safety.ExecutesShell,
		UsesNetwork:      safety.UsesNetwork,
		ReadsHostState:   safety.ReadsHostState || safety.UsesHostState,
		MutatesHostState: safety.MutatesHostState || safety.UsesHostState,
		UsesHostState:    safety.UsesHostState,
	}
}
