package agent

import (
	"os"
	"path/filepath"
	"sync"
)

// PlanStore persists the current runner's implementation plan.
// It intentionally stores plans under .falken/state/current rather than the
// workspace so plans do not appear in generated diffs.
type PlanStore struct {
	path string
	mu   sync.Mutex
}

func NewPlanStore(path string) *PlanStore {
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	return &PlanStore{path: path}
}

func (s *PlanStore) Read() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	return string(data), nil
}

func (s *PlanStore) Write(content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}

	return os.WriteFile(s.path, []byte(content), 0644)
}

func (s *PlanStore) Path() string {
	return s.path
}
