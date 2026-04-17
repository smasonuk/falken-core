package todo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type TodoStore struct {
	path string
	mu   sync.Mutex
}

func NewTodoStore(path string) *TodoStore {
	os.MkdirAll(filepath.Dir(path), 0755)
	return &TodoStore{path: path}
}

func (s *TodoStore) Read() ([]TodoItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []TodoItem{}, nil
		}
		return nil, err
	}

	var todos []TodoItem
	if err := json.Unmarshal(data, &todos); err != nil {
		return nil, err
	}

	return todos, nil
}

func (s *TodoStore) Write(todos []TodoItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(todos, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0644)
}
