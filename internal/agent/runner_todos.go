package agent

import "strings"

const todoProgressNudgePrefix = "TODO progress checkpoint:"

func (r *Runner) todoProgressNudge() (Message, error) {
	if r == nil || r.history == nil {
		return Message{}, nil
	}

	todos, err := r.history.ReadTodos()
	if err != nil {
		return Message{}, err
	}
	if len(todos) == 0 || allTodosCompleted(todos) {
		return Message{}, nil
	}

	return SystemMessage(
		todoProgressNudgePrefix + "\n" +
			RenderTodos(todos) +
			"\n\nIf the last successful tool result completed or materially advanced the current todo, your next tool call should be write_todos. " +
			"Mark completed todos now; do not defer TODO completion until submit_plan_implementation. " +
			"If no todo is complete yet, continue normally.",
	), nil
}

func allTodosCompleted(todos []Todo) bool {
	for _, todo := range todos {
		if todo.Status != TodoStatusCompleted {
			return false
		}
	}
	return true
}

func removeTrailingTodoProgressNudge(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	last := messages[len(messages)-1]
	if last.Role == RoleSystem && strings.HasPrefix(last.Content, todoProgressNudgePrefix) {
		return messages[:len(messages)-1]
	}
	return messages
}

// shouldNudgeTodoProgress is a UX checkpoint heuristic. It intentionally treats
// successful verification-like commands as progress signals even when they do
// not mutate workspace files.
func shouldNudgeTodoProgress(result ToolResult, workspaceMutated bool, workspaceMutatedThisRun bool) bool {
	if !toolResultSucceeded(result) {
		return false
	}

	switch result.Name {
	case "write_todos",
		"read_todos",
		"write_plan",
		"read_plan",
		"submit_plan_implementation",
		"read_command_evidence",
		"read_memory",
		"update_memory",
		"read_file",
		"read_files",
		"glob",
		"grep":
		return false
	}

	if workspaceMutated {
		return true
	}

	if result.Name != "execute_command" {
		return false
	}

	payload := mutationPayload(result)
	if !payload.Executed {
		return false
	}
	if !toolResultSucceeded(result) {
		return false
	}
	if !isObservationalCommand(payloadCommand(result)) {
		return true
	}
	if workspaceMutatedThisRun {
		return true
	}

	return false
}
