package agent

import "github.com/smasonuk/falken-core/internal/conversation"

type TodoStatus = conversation.TodoStatus
type Todo = conversation.Todo
type TodoManager = conversation.TodoManager

const (
	TodoStatusPending    = conversation.TodoStatusPending
	TodoStatusInProgress = conversation.TodoStatusInProgress
	TodoStatusCompleted  = conversation.TodoStatusCompleted
)

var NewTodoManager = conversation.NewTodoManager
var ValidateTodos = conversation.ValidateTodos
var NormalizeTodos = conversation.NormalizeTodos
var RenderTodos = conversation.RenderTodos
