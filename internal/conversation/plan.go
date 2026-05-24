package conversation

import (
	"errors"

	"github.com/smasonuk/falken-core/internal/store"
)

const DefaultPlanStarterText = "# Plan\n\n"

var ErrInvalidPlan = errors.New("plan content is empty or not meaningful")

// PlanManager reads and writes runtime plan text through the typed plan store.
type PlanManager struct {
	store store.PlanBackend
}

// NewPlanManager creates a runtime plan helper over the conversation-scoped plan store.
func NewPlanManager(planStore store.PlanBackend) *PlanManager {
	return &PlanManager{store: planStore}
}

// Read returns the persisted runtime plan. Missing plan state returns an empty string.
func (m *PlanManager) Read() (string, error) {
	return m.store.Read()
}

// Write replaces the persisted runtime plan text.
func (m *PlanManager) Write(plan string) error {
	return m.store.Write(plan)
}

// Clear removes the active runtime plan.
func (m *PlanManager) Clear() error {
	return m.store.Clear()
}
