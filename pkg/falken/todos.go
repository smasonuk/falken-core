package falken

import (
	"fmt"

	"github.com/smasonuk/falken-core/internal/agent"
)

// Todo is the public representation of one current Falken todo item.
type Todo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// ReadTodos returns the current conversation todo list.
func (s *Session) ReadTodos() ([]Todo, error) {
	if s == nil {
		return nil, ErrSessionClosed
	}

	s.mu.Lock()
	switch s.state {
	case lifecycleClosed:
		s.mu.Unlock()
		return nil, ErrSessionClosed
	case lifecycleStarting:
		s.mu.Unlock()
		return nil, ErrSessionStarting
	case lifecycleClosing:
		s.mu.Unlock()
		return nil, ErrSessionClosing
	}
	todoStore := s.stores.todos
	s.mu.Unlock()

	todos, err := agent.NewTodoManager(todoStore).Read()
	if err != nil {
		return nil, fmt.Errorf("read todos: %w", err)
	}

	out := make([]Todo, 0, len(todos))
	for _, todo := range todos {
		out = append(out, Todo{
			ID:      todo.ID,
			Content: todo.Content,
			Status:  string(todo.Status),
		})
	}
	return out, nil
}

func toAgentTodos(todos []Todo) []agent.Todo {
	out := make([]agent.Todo, 0, len(todos))
	for _, todo := range todos {
		out = append(out, agent.Todo{
			ID:      todo.ID,
			Content: todo.Content,
			Status:  agent.TodoStatus(todo.Status),
		})
	}
	return out
}
