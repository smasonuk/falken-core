package planningtools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
)

// ReadCommandEvidenceTool reads runtime command evidence.
type ReadCommandEvidenceTool struct{}

func (t *ReadCommandEvidenceTool) Descriptor() api.Descriptor {
	return api.Descriptor{
		Name: "read_command_evidence",
		Description: `Read conversation-scoped command evidence recorded from execute_command calls.

Command evidence records commands, statuses, exit codes, policy metadata, and
bounded output metadata. It does not prove semantic verification and does not
store full command output.`,
		Parameters:  api.MustSchema(api.ObjectSchema(map[string]any{})),
		Category:    "planning",
		Keywords:    []string{"command", "evidence", "read"},
		AlwaysLoad:  true,
		DefaultLoad: true,
		Safety:      api.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true},
	}
}

func (t *ReadCommandEvidenceTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	_ = ctx
	var params struct{}
	if err := api.DecodeArgs(args, &params); err != nil {
		return api.Fail("invalid_arguments", err.Error()), nil
	}
	state, err := host.RequireConversationState()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	evidence, err := state.ReadCommandEvidence()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	return api.Success("Command evidence read successfully.", map[string]any{
		"success":         true,
		"records":         evidence.Records,
		"review_attempts": evidence.ReviewAttempts,
		"last_review":     evidence.LastReview,
	})
}
