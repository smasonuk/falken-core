package runtimectx

import (
	"context"
	"testing"

	"github.com/smasonuk/falken-core/internal/extensions/manifest"
)

func TestRuntimeContextRoundTrip(t *testing.T) {
	eventChan := make(chan any, 1)
	perms := manifest.GranularPermissions{
		Shell: []string{"echo"},
		Network: []manifest.NetworkRule{
			{Domain: "example.com"},
		},
	}

	ctx := context.Background()
	ctx = WithToolName(ctx, "tool")
	ctx = WithPluginName(ctx, "plugin")
	ctx = WithPermissions(ctx, perms)
	ctx = WithSandboxOnly(ctx, true)
	ctx = WithEventChan(ctx, eventChan)

	if got, ok := ToolName(ctx); !ok || got != "tool" {
		t.Fatalf("ToolName() = %q, %v", got, ok)
	}
	if got, ok := PluginName(ctx); !ok || got != "plugin" {
		t.Fatalf("PluginName() = %q, %v", got, ok)
	}
	if got, ok := Permissions(ctx); !ok || len(got.Shell) != 1 || got.Shell[0] != "echo" {
		t.Fatalf("Permissions() = %#v, %v", got, ok)
	}
	if got, ok := SandboxOnly(ctx); !ok || !got {
		t.Fatalf("SandboxOnly() = %v, %v", got, ok)
	}
	if got, ok := EventChan(ctx); !ok || got == nil {
		t.Fatalf("EventChan() = %v, %v", got, ok)
	}
}
