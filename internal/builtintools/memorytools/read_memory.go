package memorytools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/conversation"
)

// ReadMemoryTool reads the current persistent agent memory from internal state.
type ReadMemoryTool struct{}

func (t *ReadMemoryTool) Descriptor() api.Descriptor {
	return api.Descriptor{
		Name: "read_memory",
		Description: `Read the current persistent agent memory.

Memory is stored in Falken internal state, not in the workspace filesystem.
Use this tool to recover durable task context after history compaction or when
continuing earlier work.`,
		Parameters:  api.MustSchema(api.ObjectSchema(map[string]any{})),
		Category:    "memory",
		Keywords:    []string{"memory", "read", "context"},
		AlwaysLoad:  true,
		DefaultLoad: true,
		Safety:      api.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true},
	}
}

func (t *ReadMemoryTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	_ = ctx

	var params struct{}
	if err := api.DecodeArgs(args, &params); err != nil {
		return api.Fail("invalid_arguments", err.Error()), nil
	}

	state, err := host.RequireConversationState()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	current, err := state.ReadMemory()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}

	content := conversation.RenderMemory(current)
	return api.Success(content, map[string]any{
		"success": true,
		"memory":  payloadMemory(current),
		"empty":   conversation.IsMemoryEmpty(current),
	})
}
