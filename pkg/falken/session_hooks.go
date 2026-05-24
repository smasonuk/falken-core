package falken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// sessionHookRef holds a reference to a registered hook and its provider.
type sessionHookRef struct {
	provider   HookProvider
	descriptor HookDescriptor
}

// sessionHookHub manages the lifecycle and execution of session hooks.
type sessionHookHub struct {
	providers []HookProvider
	started   []HookProvider
	hooks     []sessionHookRef
}

// newSessionHookHub initializes a hub for the given hook providers.
func newSessionHookHub(providers ...HookProvider) *sessionHookHub {
	filtered := make([]HookProvider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			filtered = append(filtered, provider)
		}
	}
	return &sessionHookHub{providers: filtered}
}

// start initializes all hook providers and registers their hooks.
func (h *sessionHookHub) start(ctx context.Context, host ToolHost) error {
	if h == nil {
		return nil
	}
	seen := make(map[string]struct{})
	hooks := make([]sessionHookRef, 0)
	for index, provider := range h.providers {
		startupHost := scopedHookStartupHost(host, index, provider)
		if err := provider.Start(ctx, startupHost); err != nil {
			return h.startupError(ctx, err)
		}
		h.started = append(h.started, provider)

		descriptors, err := provider.Hooks(ctx)
		if err != nil {
			return h.startupError(ctx, err)
		}
		for _, descriptor := range descriptors {
			if err := validateHookDescriptor(descriptor); err != nil {
				return h.startupError(ctx, err)
			}
			key := string(descriptor.Event) + "\x00" + descriptor.Name
			if _, exists := seen[key]; exists {
				return h.startupError(ctx, fmt.Errorf("duplicate hook %q for event %q", descriptor.Name, descriptor.Event))
			}
			seen[key] = struct{}{}
			hooks = append(hooks, sessionHookRef{provider: provider, descriptor: cloneHookDescriptor(descriptor)})
		}
	}
	h.hooks = hooks
	return nil
}

func scopedHookStartupHost(host ToolHost, index int, provider HookProvider) ToolHost {
	base, ok := host.(sessionToolHost)
	if !ok {
		return host
	}
	return newProviderStartupToolHost(base, stableHookProviderNamespace(index, provider), providerStartupSafety(provider))
}

func stableHookProviderNamespace(index int, provider HookProvider) string {
	if provider == nil {
		return fmt.Sprintf("hook-provider:%d:nil", index)
	}
	return fmt.Sprintf("hook-provider:%T:%d", provider, index)
}

// startupError cleans up started providers after a startup failure.
func (h *sessionHookHub) startupError(ctx context.Context, err error) error {
	if closeErr := h.close(ctx); closeErr != nil {
		return errors.Join(err, fmt.Errorf("cleanup started hook providers: %w", closeErr))
	}
	return err
}

// run executes all hooks registered for the specified event.
func (h *sessionHookHub) run(ctx context.Context, event HookEvent, arguments json.RawMessage) error {
	if h == nil {
		return nil
	}
	var runErr error
	for _, hook := range h.hooks {
		if hook.descriptor.Event != event {
			continue
		}
		result, err := hook.provider.RunHook(ctx, HookInvocation{
			Event:     event,
			Name:      hook.descriptor.Name,
			Arguments: append(json.RawMessage(nil), arguments...),
		})
		if err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("run hook %q for event %q: %w", hook.descriptor.Name, event, err))
			continue
		}
		if !result.Success {
			message := result.Error
			if message == "" {
				message = result.Status
			}
			if message == "" {
				message = "hook reported failure"
			}
			runErr = errors.Join(runErr, fmt.Errorf("run hook %q for event %q: %s", hook.descriptor.Name, event, message))
		}
	}
	return runErr
}

// close shuts down all started hook providers.
func (h *sessionHookHub) close(ctx context.Context) error {
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

// validateHookDescriptor checks that a hook descriptor has a valid name and event.
func validateHookDescriptor(descriptor HookDescriptor) error {
	if descriptor.Name == "" {
		return errors.New("hook has empty name")
	}
	if !isSupportedHookEvent(descriptor.Event) {
		return fmt.Errorf("hook %q has unsupported event %q", descriptor.Name, descriptor.Event)
	}
	return nil
}

// isSupportedHookEvent returns true if the event is a valid session lifecycle event.
func isSupportedHookEvent(event HookEvent) bool {
	switch event {
	case HookSessionStart,
		HookSessionClose:
		return true
	default:
		return false
	}
}

// cloneHookDescriptor returns a copy of a HookDescriptor.
func cloneHookDescriptor(descriptor HookDescriptor) HookDescriptor {
	return descriptor
}
