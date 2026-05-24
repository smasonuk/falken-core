package falken

import (
	"context"
	"fmt"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools"
	"github.com/smasonuk/falken-core/internal/conversation"
	"github.com/smasonuk/falken-core/internal/extensions/manifest"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/toolvalidation"
)

// init validates the built-in tool registry on startup.
func init() {
	if err := validateBuiltinToolEntries(builtinToolEntries()); err != nil {
		panic(err)
	}
}

// builtinToolEntries converts the builtintools registry into the internal
// tools.Entry format used by the agent layer.
func builtinToolEntries() []tools.Entry {
	all := builtintools.All()
	entries := make([]tools.Entry, 0, len(all))
	for _, t := range all {
		entries = append(entries, entryFromBuiltin(t))
	}
	return entries
}

// builtinToolEntriesFor returns built-in entries selected by config in registry order.
func builtinToolEntriesFor(config BuiltinToolsConfig) ([]tools.Entry, error) {
	switch config.Mode {
	case BuiltinToolsDefault, BuiltinToolsAll:
		return builtinToolEntries(), nil
	case BuiltinToolsNone:
		return nil, nil
	case BuiltinToolsSelected:
		return selectedBuiltinToolEntries(config.Names)
	default:
		return nil, fmt.Errorf("%w: unsupported built-in tools mode %q", ErrInvalidConfig, config.Mode)
	}
}

func selectedBuiltinToolEntries(names []string) ([]tools.Entry, error) {
	requested := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, exists := requested[name]; exists {
			return nil, fmt.Errorf("%w: duplicate built-in tool name %q", ErrInvalidConfig, name)
		}
		if !builtintools.IsBuiltin(name) {
			return nil, fmt.Errorf("%w: unknown built-in tool name %q", ErrInvalidConfig, name)
		}
		requested[name] = struct{}{}
	}

	all := builtintools.All()
	entries := make([]tools.Entry, 0, len(requested))
	for _, t := range all {
		if _, ok := requested[t.Descriptor().Name]; ok {
			entries = append(entries, entryFromBuiltin(t))
		}
	}
	return entries, nil
}

// entryFromBuiltin converts a builtintools.Tool into a tools.Entry.
func entryFromBuiltin(t builtintools.Tool) tools.Entry {
	d := t.Descriptor()
	return tools.Entry{
		Name:        d.Name,
		Description: d.Description,
		InputSchema: append([]byte(nil), d.Parameters...),
		Category:    d.Category,
		Keywords:    append([]string(nil), d.Keywords...),
		AlwaysLoad:  d.AlwaysLoad,
		DefaultLoad: d.DefaultLoad,
		PackageName: "falken-core",
		PackageDesc: "Falken Core built-in tools",
		Safety:      d.Safety,
		Permissions: manifest.DeclaredPermissions{},
	}
}

// validateBuiltinToolEntries ensures built-in tools have valid names and schemas.
func validateBuiltinToolEntries(entries []tools.Entry) error {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if _, dup := seen[entry.Name]; dup {
			return fmt.Errorf("builtintools: duplicate built-in tool name: %s", entry.Name)
		}
		seen[entry.Name] = struct{}{}
		if err := toolvalidation.ValidateDescriptor(entry.Name, entry.Description, entry.InputSchema); err != nil {
			return fmt.Errorf("builtintools: invalid built-in tool %q: %w", entry.Name, err)
		}
	}
	return nil
}

// isBuiltinTool returns true if the tool name corresponds to a registered built-in.
func isBuiltinTool(name string) bool {
	return builtintools.IsBuiltin(name)
}

// executeBuiltinTool dispatches an execution request to the named built-in tool.
func (e sessionToolExecutor) executeBuiltinTool(ctx context.Context, request agent.ToolExecutionRequest) (agent.ToolExecutionResult, error) {
	t := builtintools.ByName(request.Name)
	if t == nil {
		return builtintools.Fail("unknown_tool",
			fmt.Sprintf("unknown built-in tool: %s", request.Name)), nil
	}
	return t.Execute(ctx, e.buildHost(request.Events), request.Arguments)
}

// buildHost assembles a builtintools.Host for one tool invocation.
func (e sessionToolExecutor) buildHost(events agent.EventSink) *builtintools.Host {
	var conversationState *conversation.ConversationState
	if e.plan != nil || e.todos != nil || e.memory != nil || e.commandEvidence != nil {
		conversationState = conversation.NewConversationState(e.plan, e.todos, e.memory, e.commandEvidence)
	}
	var todos *conversation.TodoManager
	if e.todos != nil {
		todos = conversation.NewTodoManager(e.todos)
	}
	var memory *conversation.MemoryManager
	if e.memory != nil {
		memory = conversation.NewMemoryManager(e.memory)
	}
	if e.runtime == nil {
		return builtintools.NewHost(nil, nil, nil, e.mode, todos, memory, conversationState, e.reviewer, events)
	}
	return builtintools.NewHost(
		e.runtime.fileOperations,
		e.runtime.commandExecutor,
		e.runtime.executionState,
		e.mode,
		todos,
		memory,
		conversationState,
		e.reviewer,
		events,
	)
}
