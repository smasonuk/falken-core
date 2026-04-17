package todo

type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

type TodoItem struct {
	ID       string     `json:"id"`
	Content  string     `json:"content"`
	Status   TodoStatus `json:"status"`
	Priority string     `json:"priority,omitempty"`
}
