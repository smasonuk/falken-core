package conversation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/smasonuk/falken-core/internal/store"
)

const todosSectionHeader = "--- CURRENT TODOS ---"

const (
	maxTodos             = 20
	maxTodoIDLength      = 64
	maxTodoContentLength = 300
)

// TodoStatus is the explicit agent-runtime todo state.
type TodoStatus string

const (
	TodoStatusPending    TodoStatus = "pending"
	TodoStatusInProgress TodoStatus = "in_progress"
	TodoStatusCompleted  TodoStatus = "completed"
)

// Todo is the agent-runtime representation of one checklist item.
type Todo struct {
	ID      string     `json:"id"`
	Content string     `json:"content"`
	Status  TodoStatus `json:"status"`
}

// TodoManager coordinates runtime todo validation, persistence, and rendering.
type TodoManager struct {
	store store.TodoBackend
}

// NewTodoManager creates a todo runtime helper over the conversation-scoped todo store.
func NewTodoManager(todoStore store.TodoBackend) *TodoManager {
	return &TodoManager{store: todoStore}
}

// Read returns the current todo list. Missing todo state returns an empty list.
func (m *TodoManager) Read() ([]Todo, error) {
	state, err := m.store.Read()
	if err != nil {
		return nil, err
	}

	todos := make([]Todo, 0, len(state.Items))
	for i, item := range state.Items {
		todo := todoFromStoreItem(item)
		if err := validateTodo(todo); err != nil {
			return nil, fmt.Errorf("read todo item %d: %w", i, err)
		}
		todos = append(todos, todo)
	}
	todos, err = NormalizeTodos(todos)
	if err != nil {
		return nil, err
	}

	return cloneTodos(todos), nil
}

// Replace validates and persists the complete current todo list.
func (m *TodoManager) Replace(todos []Todo) error {
	normalized, err := NormalizeTodos(todos)
	if err != nil {
		return err
	}

	items := make([]store.TodoItem, 0, len(normalized))
	for _, todo := range normalized {
		items = append(items, todoToStoreItem(todo))
	}

	return m.store.Write(store.TodoState{Items: items})
}

// Clear removes all current todos.
func (m *TodoManager) Clear() error {
	return m.store.Write(store.TodoState{Items: []store.TodoItem{}})
}

// Render returns the current todo list in deterministic prompt/context form.
func (m *TodoManager) Render() (string, error) {
	todos, err := m.Read()
	if err != nil {
		return "", err
	}

	return RenderTodos(todos), nil
}

// ValidateTodos rejects missing IDs/content, unknown statuses, duplicate IDs,
// impractical lengths, and ambiguous progress state.
func ValidateTodos(todos []Todo) error {
	_, err := NormalizeTodos(todos)
	return err
}

// NormalizeTodos trims todo IDs/content and validates the normalized list.
func NormalizeTodos(todos []Todo) ([]Todo, error) {
	if len(todos) > maxTodos {
		return nil, fmt.Errorf("too many todos: %d exceeds maximum %d", len(todos), maxTodos)
	}
	normalized := make([]Todo, 0, len(todos))
	seen := make(map[string]struct{}, len(todos))
	inProgress := 0
	for i, todo := range todos {
		todo = normalizeTodo(todo)
		if err := validateTodo(todo); err != nil {
			return nil, fmt.Errorf("todo %d: %w", i, err)
		}
		if _, exists := seen[todo.ID]; exists {
			return nil, fmt.Errorf("todo %d: duplicate id %q", i, todo.ID)
		}
		seen[todo.ID] = struct{}{}
		if todo.Status == TodoStatusInProgress {
			inProgress++
		}
		normalized = append(normalized, todo)
	}
	if inProgress > 1 {
		return nil, fmt.Errorf("multiple in_progress todos: %d", inProgress)
	}

	return normalized, nil
}

// RenderTodos renders todos in stable input order for runtime context.
func RenderTodos(todos []Todo) string {
	if len(todos) == 0 {
		return todosSectionHeader + "\n(no todos)"
	}

	var builder strings.Builder
	builder.WriteString(todosSectionHeader)
	for _, todo := range todos {
		builder.WriteString("\n")
		builder.WriteString(todoMarker(todo.Status))
		builder.WriteString(" ")
		builder.WriteString(renderTodoField(todo.ID))
		builder.WriteString(": ")
		builder.WriteString(renderTodoField(todo.Content))
	}

	return builder.String()
}

func renderTodoField(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.TrimSpace(value)
}

func validateTodo(todo Todo) error {
	if strings.TrimSpace(todo.ID) == "" {
		return errors.New("missing id")
	}
	if len([]rune(todo.ID)) > maxTodoIDLength {
		return fmt.Errorf("id exceeds maximum length %d", maxTodoIDLength)
	}
	if strings.TrimSpace(todo.Content) == "" {
		return errors.New("missing content")
	}
	if len([]rune(todo.Content)) > maxTodoContentLength {
		return fmt.Errorf("content exceeds maximum length %d", maxTodoContentLength)
	}
	switch todo.Status {
	case TodoStatusPending, TodoStatusInProgress, TodoStatusCompleted:
		return nil
	default:
		return fmt.Errorf("unknown status %q", todo.Status)
	}
}

func normalizeTodo(todo Todo) Todo {
	todo.ID = strings.TrimSpace(todo.ID)
	todo.Content = strings.TrimSpace(todo.Content)
	return todo
}

func todoFromStoreItem(item store.TodoItem) Todo {
	todo := Todo{
		ID:      item.ID,
		Content: item.Content,
		Status:  TodoStatus(item.Status),
	}
	if todo.Content == "" {
		todo.Content = item.Text
	}
	if todo.Status == "" {
		if item.Done {
			todo.Status = TodoStatusCompleted
		} else {
			todo.Status = TodoStatusPending
		}
	}

	return todo
}

func todoToStoreItem(todo Todo) store.TodoItem {
	return store.TodoItem{
		ID:      todo.ID,
		Content: todo.Content,
		Status:  string(todo.Status),
		Text:    todo.Content,
		Done:    todo.Status == TodoStatusCompleted,
	}
}

func todoMarker(status TodoStatus) string {
	switch status {
	case TodoStatusPending:
		return "[ ]"
	case TodoStatusInProgress:
		return "[>]"
	case TodoStatusCompleted:
		return "[x]"
	default:
		return "[?]"
	}
}

func cloneTodos(todos []Todo) []Todo {
	return append([]Todo(nil), todos...)
}
