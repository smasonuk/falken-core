package memorytools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/conversation"
)

// UpdateMemoryTool merges concise durable task context into persistent agent memory.
type UpdateMemoryTool struct{}

type updateMemoryRequest struct {
	CurrentGoal          string   `json:"current_goal"`
	ClearCurrentGoal     bool     `json:"clear_current_goal"`
	AddEntries           []string `json:"add_entries"`
	RemoveEntries        []string `json:"remove_entries"`
	AddImportantFiles    []string `json:"add_important_files"`
	RemoveImportantFiles []string `json:"remove_important_files"`
	AddDecisions         []string `json:"add_decisions"`
	RemoveDecisions      []string `json:"remove_decisions"`
	AddOpenQuestions     []string `json:"add_open_questions"`
	RemoveOpenQuestions  []string `json:"remove_open_questions"`
}

func (t *UpdateMemoryTool) Descriptor() api.Descriptor {
	return api.Descriptor{
		Name: "update_memory",
		Description: `Update persistent structured agent memory.

CRITICAL USAGE RULES:
1. Merge semantics: omitted fields are preserved. You do not need to repeat old information to keep it.
2. Keep memory extremely concise. Store only stable task context, important file paths, major decisions, and unresolved questions.
3. Do not store raw code snippets, secrets, credentials, command output dumps, or large logs.
4. Remove resolved open questions promptly.
5. Memory is Falken internal state, not a workspace file.`,
		Parameters:  api.MustSchema(api.ObjectSchema(updateMemoryProps())),
		Category:    "memory",
		Keywords:    []string{"memory", "update", "context"},
		AlwaysLoad:  true,
		DefaultLoad: true,
		Safety:      api.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true, MutatesHostState: true},
	}
}

func (t *UpdateMemoryTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	_ = ctx

	var req updateMemoryRequest
	if err := api.DecodeArgs(args, &req); err != nil {
		return api.Fail("invalid_arguments", err.Error()), nil
	}
	state, err := host.RequireConversationState()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}
	before, err := state.ReadMemory()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}

	updated, err := state.UpdateMemory(conversation.MemoryUpdate{
		CurrentGoal:          req.CurrentGoal,
		ClearCurrentGoal:     req.ClearCurrentGoal,
		AddEntries:           req.AddEntries,
		RemoveEntries:        req.RemoveEntries,
		AddImportantFiles:    req.AddImportantFiles,
		RemoveImportantFiles: req.RemoveImportantFiles,
		AddDecisions:         req.AddDecisions,
		RemoveDecisions:      req.RemoveDecisions,
		AddOpenQuestions:     req.AddOpenQuestions,
		RemoveOpenQuestions:  req.RemoveOpenQuestions,
	})
	if err != nil {
		return api.Fail("invalid_memory", err.Error()), nil
	}

	changed := !conversation.MemoryEqual(before, updated)
	return api.Success(conversation.RenderMemory(updated), map[string]any{
		"success":        true,
		"memory_updated": changed,
		"changed":        changed,
		"memory":         payloadMemory(updated),
	})
}

func updateMemoryProps() map[string]any {
	stringItems := map[string]any{"type": "string"}
	return map[string]any{
		"current_goal":           api.StringProp("Concise current goal. Replaces the existing current goal when provided."),
		"clear_current_goal":     api.BoolProp("Set true to clear the current goal."),
		"add_entries":            api.ArrayProp(stringItems, "Freeform concise notes to add."),
		"remove_entries":         api.ArrayProp(stringItems, "Freeform notes to remove exactly after normalization."),
		"add_important_files":    api.ArrayProp(stringItems, "Important file paths to remember."),
		"remove_important_files": api.ArrayProp(stringItems, "Important file paths to stop remembering."),
		"add_decisions":          api.ArrayProp(stringItems, "Key architectural or implementation decisions to remember."),
		"remove_decisions":       api.ArrayProp(stringItems, "Decisions to remove exactly after normalization."),
		"add_open_questions":     api.ArrayProp(stringItems, "Unresolved questions or unknowns to remember."),
		"remove_open_questions":  api.ArrayProp(stringItems, "Resolved questions to remove exactly after normalization."),
	}
}
