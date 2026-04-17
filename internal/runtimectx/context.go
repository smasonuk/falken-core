package runtimectx

import (
	"context"

	"github.com/smasonuk/falken-core/internal/extensions/manifest"
)

type contextKey string

const (
	toolNameKey    contextKey = "tool_name"
	pluginNameKey  contextKey = "plugin_name"
	permissionsKey contextKey = "permissions"
	sandboxOnlyKey contextKey = "sandbox_only"
	eventChanKey   contextKey = "event_channel"
)

func WithToolName(ctx context.Context, toolName string) context.Context {
	return context.WithValue(ctx, toolNameKey, toolName)
}

func ToolName(ctx context.Context) (string, bool) {
	value, ok := ctx.Value(toolNameKey).(string)
	return value, ok
}

func WithPluginName(ctx context.Context, pluginName string) context.Context {
	return context.WithValue(ctx, pluginNameKey, pluginName)
}

func PluginName(ctx context.Context) (string, bool) {
	value, ok := ctx.Value(pluginNameKey).(string)
	return value, ok
}

func WithPermissions(ctx context.Context, permissions manifest.GranularPermissions) context.Context {
	return context.WithValue(ctx, permissionsKey, permissions)
}

func Permissions(ctx context.Context) (manifest.GranularPermissions, bool) {
	value, ok := ctx.Value(permissionsKey).(manifest.GranularPermissions)
	return value, ok
}

func WithSandboxOnly(ctx context.Context, sandboxOnly bool) context.Context {
	return context.WithValue(ctx, sandboxOnlyKey, sandboxOnly)
}

func SandboxOnly(ctx context.Context) (bool, bool) {
	value, ok := ctx.Value(sandboxOnlyKey).(bool)
	return value, ok
}

func WithEventChan(ctx context.Context, eventChan chan<- any) context.Context {
	return context.WithValue(ctx, eventChanKey, eventChan)
}

func EventChan(ctx context.Context) (chan<- any, bool) {
	value, ok := ctx.Value(eventChanKey).(chan<- any)
	return value, ok
}
