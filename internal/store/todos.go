package store

import (
	"fmt"

	"github.com/smasonuk/falken-core/internal/persist"
	"github.com/smasonuk/falken-core/internal/state"
)

// TodoItem is a persisted todo item.
type TodoItem struct {
	ID      string `json:"id,omitempty"`
	Content string `json:"content,omitempty"`
	Status  string `json:"status,omitempty"`
	Text    string `json:"text,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

// TodoState persists conversation todos as a structured JSON document.
type TodoState struct {
	Items []TodoItem `json:"items"`
}

// TodoStore persists conversation todo state.
type TodoStore struct {
	path string
}

// NewTodoStore builds a todo store from the canonical state layout.
func NewTodoStore(layout state.Layout) TodoStore {
	return TodoStore{path: layout.TodosPath}
}

// Path returns the canonical backing file path for the todo store.
func (s TodoStore) Path() string {
	return s.path
}

// Read loads todo state. A missing file returns an empty todo state.
func (s TodoStore) Read() (TodoState, error) {
	todos := TodoState{Items: []TodoItem{}}
	found, err := persist.ReadJSON(s.path, &todos)
	if err != nil {
		return TodoState{}, fmt.Errorf("read todo store: %w", err)
	}
	if !found {
		return TodoState{Items: []TodoItem{}}, nil
	}
	if todos.Items == nil {
		todos.Items = []TodoItem{}
	}

	return todos, nil
}

// Write persists the complete todo state atomically.
func (s TodoStore) Write(todos TodoState) error {
	if todos.Items == nil {
		todos.Items = []TodoItem{}
	}

	if err := persist.WriteJSONAtomic(s.path, todos, 0o600); err != nil {
		return fmt.Errorf("write todo store: %w", err)
	}

	return nil
}
