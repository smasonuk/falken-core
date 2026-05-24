package tools

import (
	"errors"
	"sort"
)

var (
	// ErrToolNotRegistered indicates activation was requested for a tool that is not registered.
	ErrToolNotRegistered = errors.New("tool is not registered")
	// ErrToolAlwaysLoad indicates an always-load tool cannot be deactivated through the public runtime registry.
	ErrToolAlwaysLoad = errors.New("always-load tool cannot be deactivated")
)

// RuntimeRegistry tracks the subset of registered tools that are active for runtime callers.
//
// Activation only makes validated metadata available. It does not instantiate Wasm,
// register host functions, or execute tool code.
type RuntimeRegistry struct {
	registered *Registry
	active     map[string]Entry
}

// NewRuntimeRegistry creates an active-tool registry backed by registered tool metadata.
func NewRuntimeRegistry(registered *Registry) *RuntimeRegistry {
	if registered == nil {
		registered = NewRegistry()
	}
	return &RuntimeRegistry{
		registered: registered,
		active:     make(map[string]Entry),
	}
}

// Activate makes a registered tool available to runtime callers.
func (r *RuntimeRegistry) Activate(name string) (Entry, error) {
	if r == nil {
		return Entry{}, ToolNotRegisteredError{Name: name}
	}
	if entry, ok := r.active[name]; ok {
		return CloneEntry(entry), nil
	}

	entry, ok := r.registered.Lookup(name)
	if !ok {
		return Entry{}, ToolNotRegisteredError{Name: name}
	}

	r.active[name] = CloneEntry(entry)
	return CloneEntry(entry), nil
}

// ActivateDefaults activates tools marked default-load or always-load in registered metadata.
func (r *RuntimeRegistry) ActivateDefaults() error {
	if r == nil {
		return nil
	}

	for _, entry := range r.registered.List() {
		if !entry.DefaultLoad && !entry.AlwaysLoad {
			continue
		}
		if _, err := r.Activate(entry.Name); err != nil {
			return err
		}
	}
	return nil
}

// Deactivate removes a normal active tool from the runtime registry.
func (r *RuntimeRegistry) Deactivate(name string) error {
	if r == nil {
		return ToolNotRegisteredError{Name: name}
	}

	entry, ok := r.registered.Lookup(name)
	if !ok {
		return ToolNotRegisteredError{Name: name}
	}
	if entry.AlwaysLoad {
		return AlwaysLoadDeactivateError{Name: name}
	}

	delete(r.active, name)
	return nil
}

// Lookup returns active tool metadata by runtime name.
func (r *RuntimeRegistry) Lookup(name string) (Entry, bool) {
	if r == nil {
		return Entry{}, false
	}
	entry, ok := r.active[name]
	if !ok {
		return Entry{}, false
	}
	return CloneEntry(entry), true
}

// IsActive reports whether a registered tool is currently active.
func (r *RuntimeRegistry) IsActive(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.active[name]
	return ok
}

// List returns active tools in deterministic name order.
func (r *RuntimeRegistry) List() []Entry {
	if r == nil {
		return nil
	}

	names := make([]string, 0, len(r.active))
	for name := range r.active {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		entries = append(entries, CloneEntry(r.active[name]))
	}
	return entries
}

// Count returns the number of active tools.
func (r *RuntimeRegistry) Count() int {
	if r == nil {
		return 0
	}
	return len(r.active)
}

// ToolNotRegisteredError identifies an activation request for an unknown registered tool.
type ToolNotRegisteredError struct {
	Name string
}

func (e ToolNotRegisteredError) Error() string {
	return ErrToolNotRegistered.Error() + ": " + e.Name
}

// Is lets callers use errors.Is(err, ErrToolNotRegistered).
func (e ToolNotRegisteredError) Is(target error) bool {
	return target == ErrToolNotRegistered
}

// AlwaysLoadDeactivateError identifies a disallowed deactivation of an always-load tool.
type AlwaysLoadDeactivateError struct {
	Name string
}

func (e AlwaysLoadDeactivateError) Error() string {
	return ErrToolAlwaysLoad.Error() + ": " + e.Name
}

// Is lets callers use errors.Is(err, ErrToolAlwaysLoad).
func (e AlwaysLoadDeactivateError) Is(target error) bool {
	return target == ErrToolAlwaysLoad
}
