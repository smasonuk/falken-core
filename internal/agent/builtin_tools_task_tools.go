package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/smasonuk/falken-core/internal/tasks"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

func truncateTaskArtifact(content string) string {
	const maxChars = 12000
	if len(content) <= maxChars {
		return content
	}
	return content[:maxChars] + "\n\n[TRUNCATED: task artifact exceeded 12000 characters.]"
}

func readTaskArtifact(path string) (string, bool, string) {
	if path == "" {
		return "", false, ""
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", true, fmt.Sprintf("Failed to read task artifact: %v", err)
	}

	return truncateTaskArtifact(string(data)), true, ""
}

func taskToModelView(task tasks.Task) map[string]any {
	return map[string]any{
		"id":             task.ID,
		"kind":           task.Kind,
		"subject":        task.Subject,
		"description":    task.Description,
		"status":         task.Status,
		"depends_on":     task.DependsOn,
		"parent_task_id": task.ParentTaskID,
		"session_id":     task.SessionID,
		"summary":        task.Summary,
		"last_error":     task.LastError,
		"retry_count":    task.RetryCount,
		"owner":          task.Owner,
		"created_at":     task.CreatedAt,
		"updated_at":     task.UpdatedAt,
		"started_at":     task.StartedAt,
		"completed_at":   task.CompletedAt,
		"has_result":     task.ResultPath != "",
		"has_plan":       task.PlanPath != "",
	}
}

func taskToListView(task tasks.Task) map[string]any {
	return map[string]any{
		"id":         task.ID,
		"kind":       task.Kind,
		"subject":    task.Subject,
		"status":     task.Status,
		"depends_on": task.DependsOn,
		"has_result": task.ResultPath != "",
		"has_plan":   task.PlanPath != "",
		"summary":    task.Summary,
		"last_error": task.LastError,
	}
}

// TaskCreateTool
type TaskCreateTool struct {
	store *tasks.TaskStore
}

func (t *TaskCreateTool) Name() string        { return "TaskCreate" }
func (t *TaskCreateTool) Description() string { return "Create a new task in the management system." }
func (t *TaskCreateTool) IsLongRunning() bool { return false }

func (t *TaskCreateTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"Kind": {
					Type:        jsonschema.String,
					Description: "The kind of task (e.g., subagent, verification, research).",
				},
				"Subject": {
					Type:        jsonschema.String,
					Description: "A short summary of the task.",
				},
				"Description": {
					Type:        jsonschema.String,
					Description: "Detailed explanation of what needs to be done.",
				},
				"DependsOn": {
					Type: jsonschema.Array,
					Items: &jsonschema.Definition{
						Type: jsonschema.String,
					},
					Description: "Optional: List of task IDs this task depends on.",
				},
				"ParentTaskID": {
					Type:        jsonschema.String,
					Description: "Optional: The ID of the parent task.",
				},
			},
			Required: []string{"Subject", "Description"},
		},
	}
}

func (t *TaskCreateTool) Run(ctx context.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid arguments")
	}
	kind, _ := m["Kind"].(string)
	subject, _ := m["Subject"].(string)
	description, _ := m["Description"].(string)

	var dependsOn []string
	if deps, ok := m["DependsOn"].([]any); ok {
		for _, d := range deps {
			if str, ok := d.(string); ok {
				dependsOn = append(dependsOn, str)
			}
		}
	}

	parentTaskID, _ := m["ParentTaskID"].(string)

	id, err := t.store.CreateTask(kind, subject, description, dependsOn, parentTaskID)
	if err != nil {
		return nil, err
	}

	return map[string]any{"id": id, "status": "created"}, nil
}

// TaskListTool
type TaskListTool struct {
	store *tasks.TaskStore
}

func (t *TaskListTool) Name() string { return "TaskList" }
func (t *TaskListTool) Description() string {
	return "List all tasks with their current status and dependencies."
}
func (t *TaskListTool) IsLongRunning() bool { return false }

func (t *TaskListTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"Status": {
					Type:        jsonschema.String,
					Description: "Optional: filter tasks by status (pending, in_progress, verifying, completed, failed, cancelled).",
				},
			},
		},
	}
}

func (t *TaskListTool) Run(ctx context.Context, args any) (map[string]any, error) {
	allTasks, err := t.store.Load()
	if err != nil {
		return nil, err
	}

	filterStatus := ""
	if m, ok := args.(map[string]any); ok {
		if s, ok := m["Status"].(string); ok {
			filterStatus = s
		}
	}

	var readyTasks []map[string]any
	var blockedTasks []map[string]any
	var inProgressTasks []map[string]any
	var completedTasks []map[string]any
	var otherTasks []map[string]any

	// Pre-calculate completed tasks for dependency checking
	completedMap := make(map[string]bool)
	for _, task := range allTasks {
		if task.Status == tasks.StatusCompleted {
			completedMap[task.ID] = true
		}
	}

	for _, task := range allTasks {
		if filterStatus != "" && string(task.Status) != filterStatus {
			continue
		}

		taskData := taskToListView(task)

		if task.Status == tasks.StatusInProgress || task.Status == tasks.StatusVerifying {			inProgressTasks = append(inProgressTasks, taskData)
		} else if task.Status == tasks.StatusCompleted {
			completedTasks = append(completedTasks, taskData)
		} else if task.Status == tasks.StatusPending || task.Status == tasks.StatusFailed {
			isBlocked := false
			for _, depID := range task.DependsOn {
				if !completedMap[depID] {
					isBlocked = true
					break
				}
			}
			if isBlocked {
				blockedTasks = append(blockedTasks, taskData)
			} else {
				readyTasks = append(readyTasks, taskData)
			}
		} else {
			otherTasks = append(otherTasks, taskData)
		}
	}

	return map[string]any{
		"ready_tasks":       readyTasks,
		"blocked_tasks":     blockedTasks,
		"in_progress_tasks": inProgressTasks,
		"completed_tasks":   completedTasks,
		"other_tasks":       otherTasks,
		"total_tasks":       len(allTasks),
	}, nil
}

// TaskGetTool
type TaskGetTool struct {
	store *tasks.TaskStore
}

func (t *TaskGetTool) Name() string        { return "TaskGet" }
func (t *TaskGetTool) Description() string { return "Retrieve full details of a specific task." }
func (t *TaskGetTool) IsLongRunning() bool { return false }

func (t *TaskGetTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"ID": {
					Type:        jsonschema.String,
					Description: "The ID of the task to retrieve.",
				},
			},
			Required: []string{"ID"},
		},
	}
}

func (t *TaskGetTool) Run(ctx context.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid arguments")
	}
	id, _ := m["ID"].(string)

	task, err := t.store.GetTask(id)
	if err != nil {
		return nil, err
	}

	res := map[string]any{
		"task": taskToModelView(*task),
	}

	if content, exists, readErr := readTaskArtifact(task.ResultPath); exists {
		if readErr != "" {
			res["result_error"] = readErr
		} else {
			res["result"] = content
		}
	}

	if content, exists, readErr := readTaskArtifact(task.PlanPath); exists {
		if readErr != "" {
			res["plan_error"] = readErr
		} else {
			res["plan"] = content
		}
	}

	return res, nil
}

// TaskUpdateTool
type TaskUpdateTool struct {
	store *tasks.TaskStore
}

func (t *TaskUpdateTool) Name() string { return "TaskUpdate" }
func (t *TaskUpdateTool) Description() string {
	return "Update a task's status, dependencies, or results."
}
func (t *TaskUpdateTool) IsLongRunning() bool { return false }

func (t *TaskUpdateTool) Definition() openai.FunctionDefinition {
	return openai.FunctionDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"ID": {
					Type:        jsonschema.String,
					Description: "The ID of the task to update.",
				},
				"Status": {
					Type:        jsonschema.String,
					Description: "New status (pending, in_progress, verifying, completed, failed, cancelled, deleted). Note: 'deleted' will permanently remove the task.",
				},
				"Subject": {
					Type:        jsonschema.String,
					Description: "New task subject.",
				},
				"Description": {
					Type:        jsonschema.String,
					Description: "New task description.",
				},
				"Summary": {
					Type:        jsonschema.String,
					Description: "Summary of the task result or current state.",
				},
				"LastError": {
					Type:        jsonschema.String,
					Description: "Error message if the task failed.",
				},
				"DependsOn": {
					Type: jsonschema.Array,
					Items: &jsonschema.Definition{
						Type: jsonschema.String,
					},
					Description: "Replace the list of task IDs that this task depends on.",
				},
			},
			Required: []string{"ID"},
		},
	}
}

func (t *TaskUpdateTool) Run(ctx context.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid arguments")
	}
	id, _ := m["ID"].(string)

	patch := tasks.TaskPatch{}
	if status, ok := m["Status"].(string); ok {
		if status == "deleted" {
			if err := t.store.DeleteTask(id); err != nil {
				return nil, err
			}
			return map[string]any{"result": "Task deleted successfully."}, nil
		}
		taskStatus := tasks.TaskStatus(status)
		patch.Status = &taskStatus
	}
	if subject, ok := m["Subject"].(string); ok {
		patch.Subject = &subject
	}
	if description, ok := m["Description"].(string); ok {
		patch.Description = &description
	}
	if summary, ok := m["Summary"].(string); ok {
		patch.Summary = &summary
	}
	if lastError, ok := m["LastError"].(string); ok {
		patch.LastError = &lastError
	}
	if dependsOn, ok := m["DependsOn"].([]any); ok {
		var deps []string
		for _, d := range dependsOn {
			if s, ok := d.(string); ok {
				deps = append(deps, s)
			}
		}
		patch.DependsOn = &deps
	}

	err := t.store.UpdateTask(id, patch)
	if err != nil {
		return nil, err
	}

	msg := "Task updated successfully."
	if patch.Status != nil && *patch.Status == tasks.StatusCompleted {
		msg = "Task marked completed. Call TaskList to find your next unblocked task."
	}

	return map[string]any{"result": msg}, nil
}
