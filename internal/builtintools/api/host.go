package api

import (
	"errors"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/conversation"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

// ErrHostUnavailable is returned when a tool requests a service that has not
// been wired up for this session.
var ErrHostUnavailable = errors.New("session runtime is unavailable")

// Host gives tools access to every session-scoped service they may need.
type Host struct {
	fileOps  workspacefiles.Operations
	executor runtimeexec.CommandExecutor
	state    *runtimeexec.ExecutionState
	mode     *agent.ModeState
	todos    *conversation.TodoManager
	memory   *conversation.MemoryManager
	conv     *conversation.ConversationState
	reviewer agent.CommandEvidenceReviewer
	events   agent.EventSink
}

// NewHost assembles a Host from session-level services. Any field may be nil;
// tools must surface a clean error rather than panic if a service is absent.
func NewHost(
	fileOps workspacefiles.Operations,
	executor runtimeexec.CommandExecutor,
	state *runtimeexec.ExecutionState,
	mode *agent.ModeState,
	todos *conversation.TodoManager,
	memory *conversation.MemoryManager,
	conv *conversation.ConversationState,
	reviewer agent.CommandEvidenceReviewer,
	events agent.EventSink,
) *Host {
	return &Host{
		fileOps:  fileOps,
		executor: executor,
		state:    state,
		mode:     mode,
		todos:    todos,
		memory:   memory,
		conv:     conv,
		reviewer: reviewer,
		events:   events,
	}
}

// Events returns the per-call event sink. May be nil.
func (h *Host) Events() agent.EventSink {
	if h == nil {
		return nil
	}
	return h.events
}

func (h *Host) RequireFileOps() (workspacefiles.Operations, error) {
	if h == nil || h.fileOps == nil {
		return nil, ErrHostUnavailable
	}
	return h.fileOps, nil
}

func (h *Host) RequireExecutor() (runtimeexec.CommandExecutor, *runtimeexec.ExecutionState, error) {
	if h == nil || h.executor == nil || h.state == nil {
		return nil, nil, ErrHostUnavailable
	}
	return h.executor, h.state, nil
}

func (h *Host) RequireMode() (*agent.ModeState, error) {
	if h == nil || h.mode == nil {
		return nil, ErrHostUnavailable
	}
	return h.mode, nil
}

func (h *Host) RequireTodos() (*conversation.TodoManager, error) {
	if h == nil || h.todos == nil {
		return nil, ErrHostUnavailable
	}
	return h.todos, nil
}

func (h *Host) RequireMemory() (*conversation.MemoryManager, error) {
	if h == nil || h.memory == nil {
		return nil, ErrHostUnavailable
	}
	return h.memory, nil
}

func (h *Host) RequireConversationState() (*conversation.ConversationState, error) {
	if h == nil || h.conv == nil {
		return nil, ErrHostUnavailable
	}
	return h.conv, nil
}

func (h *Host) CommandEvidenceReviewer() agent.CommandEvidenceReviewer {
	if h == nil {
		return nil
	}
	return h.reviewer
}
