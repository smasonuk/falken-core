package conversation

import (
	"errors"
	"testing"
)

func TestNewConversationStateNilBackendsAreUnavailable(t *testing.T) {
	state := NewConversationState(nil, nil, nil, nil)
	if _, err := state.ReadPlan(); !errors.Is(err, ErrConversationStateUnavailable) {
		t.Fatalf("ReadPlan error = %v, want ErrConversationStateUnavailable", err)
	}
	if _, err := state.ReadTodos(); !errors.Is(err, ErrConversationStateUnavailable) {
		t.Fatalf("ReadTodos error = %v, want ErrConversationStateUnavailable", err)
	}
	if _, err := state.ReadMemory(); !errors.Is(err, ErrConversationStateUnavailable) {
		t.Fatalf("ReadMemory error = %v, want ErrConversationStateUnavailable", err)
	}
	if _, err := state.ReadCommandEvidence(); !errors.Is(err, ErrConversationStateUnavailable) {
		t.Fatalf("ReadCommandEvidence error = %v, want ErrConversationStateUnavailable", err)
	}
}
