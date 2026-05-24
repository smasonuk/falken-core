package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/smasonuk/falken-core/internal/persist"
	"github.com/smasonuk/falken-core/internal/state"
)

// ErrPlanStorePathRequired indicates a plan store was used before it had a backing path.
var ErrPlanStorePathRequired = errors.New("plan store path is required")

// PlanStore persists the conversation plan as plain text.
type PlanStore struct {
	path string
}

// NewPlanStore builds a plan store from the canonical state layout.
func NewPlanStore(layout state.Layout) PlanStore {
	return PlanStore{path: layout.PlanPath}
}

// Path returns the canonical backing file path for the plan store.
func (s PlanStore) Path() string {
	return s.path
}

// Read loads the current plan text. A missing file returns an empty plan.
func (s PlanStore) Read() (string, error) {
	if err := s.validatePath(); err != nil {
		return "", err
	}
	plan, found, err := persist.ReadText(s.path)
	if err != nil {
		return "", fmt.Errorf("read plan store: %w", err)
	}
	if !found {
		return "", nil
	}

	return plan, nil
}

// Write persists the plan text atomically.
func (s PlanStore) Write(plan string) error {
	if err := s.validatePath(); err != nil {
		return err
	}
	if err := persist.WriteTextAtomic(s.path, plan, 0o600); err != nil {
		return fmt.Errorf("write plan store: %w", err)
	}

	return nil
}

// Clear removes the active plan text.
func (s PlanStore) Clear() error {
	return s.Write("")
}

func (s PlanStore) validatePath() error {
	if strings.TrimSpace(s.path) == "" {
		return ErrPlanStorePathRequired
	}
	return nil
}
