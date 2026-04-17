package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smasonuk/falken-core/internal/host"
	"github.com/smasonuk/falken-core/internal/permissions"
	"github.com/smasonuk/falken-core/internal/runtimectx"
)

func TestExecuteCommandTool_EventChannelStreamsOutput(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}

	shell := host.NewStatefulShell(cwd, t.TempDir(), permissions.NewManager(&permissions.Config{
		GlobalAllowedCommands: []string{"make**"},
	}, nil), nil)
	shell.TestingMode = true
	shell.RealCWD = filepath.Clean(cwd)

	tool := &ExecuteCommandTool{
		runner: &Runner{Shell: shell},
	}

	eventChan := make(chan any, 10)
	ctx := runtimectx.WithEventChan(context.Background(), eventChan)

	result, err := tool.Run(ctx, map[string]any{"Command": "make -v"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result["result"] == "" {
		t.Fatalf("expected command output")
	}

	foundChunk := false
	for len(eventChan) > 0 {
		msg := <-eventChan
		if _, ok := msg.(CommandStreamMsg); ok {
			foundChunk = true
			break
		}
	}
	if !foundChunk {
		t.Fatalf("expected CommandStreamMsg to be emitted")
	}
}
