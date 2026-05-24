package conversation

import (
	"errors"
	"fmt"

	"github.com/smasonuk/falken-core/internal/store"
)

var ErrConversationStateUnavailable = errors.New("conversation state is unavailable")

// ConversationState coordinates conversation-scoped plan, todo, and memory state.
type ConversationState struct {
	plan     *PlanManager
	todos    *TodoManager
	memory   *MemoryManager
	evidence *CommandEvidenceManager
}

// NewConversationState creates a state service from the conversation stores.
func NewConversationState(planStore store.PlanBackend, todoStore store.TodoBackend, memoryStore store.MemoryBackend, evidenceStores ...store.CommandEvidenceBackend) *ConversationState {
	state := &ConversationState{}
	if planStore != nil {
		state.plan = NewPlanManager(planStore)
	}
	if todoStore != nil {
		state.todos = NewTodoManager(todoStore)
	}
	if memoryStore != nil {
		state.memory = NewMemoryManager(memoryStore)
	}
	if len(evidenceStores) != 0 && evidenceStores[0] != nil {
		state.evidence = NewCommandEvidenceManager(evidenceStores[0])
	}
	return state
}

func (s *ConversationState) ReadPlan() (string, error) {
	if s == nil || s.plan == nil {
		return "", ErrConversationStateUnavailable
	}
	return s.plan.Read()
}

func (s *ConversationState) WritePlanAndTodos(plan string, todos []Todo) error {
	if s == nil || s.plan == nil || s.todos == nil {
		return ErrConversationStateUnavailable
	}
	if err := ValidateImplementationPlan(plan); err != nil {
		return err
	}
	normalized, err := NormalizeTodos(todos)
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return errors.New("todos must contain at least one item")
	}
	oldPlan, err := s.plan.Read()
	if err != nil {
		return err
	}
	if err := s.plan.Write(plan); err != nil {
		return err
	}
	if err := s.todos.Replace(normalized); err != nil {
		if rollbackErr := s.plan.Write(oldPlan); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback plan: %w", rollbackErr))
		}
		return err
	}
	if s.evidence != nil {
		if err := s.evidence.RecordPlanBaseline(); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationState) ReadTodos() ([]Todo, error) {
	if s == nil || s.todos == nil {
		return nil, ErrConversationStateUnavailable
	}
	return s.todos.Read()
}

func (s *ConversationState) ReplaceTodos(todos []Todo) error {
	if s == nil || s.todos == nil {
		return ErrConversationStateUnavailable
	}
	return s.todos.Replace(todos)
}

// CompletePlanImplementation clears active plan/todo state after an accepted implementation submission.
func (s *ConversationState) CompletePlanImplementation() error {
	if s == nil || s.plan == nil || s.todos == nil {
		return ErrConversationStateUnavailable
	}
	oldPlan, err := s.plan.Read()
	if err != nil {
		return err
	}
	if err := s.plan.Clear(); err != nil {
		return err
	}
	if err := s.todos.Clear(); err != nil {
		if rollbackErr := s.plan.Write(oldPlan); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback plan: %w", rollbackErr))
		}
		return err
	}
	if s.evidence != nil {
		if err := s.evidence.ResetReviewAttempts(); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationState) ReadMemory() (store.MemoryState, error) {
	if s == nil || s.memory == nil {
		return store.MemoryState{}, ErrConversationStateUnavailable
	}
	return s.memory.Read()
}

func (s *ConversationState) UpdateMemory(update MemoryUpdate) (store.MemoryState, error) {
	if s == nil || s.memory == nil {
		return store.MemoryState{}, ErrConversationStateUnavailable
	}
	return s.memory.Update(update)
}

func (s *ConversationState) ReadCommandEvidence() (CommandEvidenceState, error) {
	if s == nil || s.evidence == nil {
		return CommandEvidenceState{}, ErrConversationStateUnavailable
	}
	return s.evidence.Read()
}

func (s *ConversationState) AppendCommandEvidence(record CommandEvidenceRecord) error {
	if s == nil || s.evidence == nil {
		return ErrConversationStateUnavailable
	}
	return s.evidence.Append(record)
}

func (s *ConversationState) RecordCommandEvidenceReview(review CommandEvidenceReview) (CommandEvidenceState, error) {
	if s == nil || s.evidence == nil {
		return CommandEvidenceState{}, ErrConversationStateUnavailable
	}
	return s.evidence.RecordReview(review)
}

func (s *ConversationState) ResetCommandEvidenceReviewAttempts() error {
	if s == nil || s.evidence == nil {
		return ErrConversationStateUnavailable
	}
	return s.evidence.ResetReviewAttempts()
}

func (s *ConversationState) RecordWorkspaceMutation(toolName string, recordedAt string) (CommandEvidenceState, error) {
	if s == nil || s.evidence == nil {
		return CommandEvidenceState{}, ErrConversationStateUnavailable
	}
	return s.evidence.RecordWorkspaceMutation(toolName, recordedAt)
}

func (s *ConversationState) ClearCommandEvidence() error {
	if s == nil || s.evidence == nil {
		return ErrConversationStateUnavailable
	}
	return s.evidence.Clear()
}
