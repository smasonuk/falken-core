package tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type TaskStore struct {
	filePath string
	mu       sync.Mutex
}

func NewTaskStore(filePath string) *TaskStore {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("Warning: Failed to create directory for tasks.json: %v\n", err)
	}
	return &TaskStore{
		filePath: filePath,
	}
}

func (s *TaskStore) updateLocked(fn func([]Task) ([]Task, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var tasks []Task
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read tasks file: %w", err)
		}
	} else {
		if err := json.Unmarshal(data, &tasks); err != nil {
			return fmt.Errorf("failed to unmarshal tasks: %w", err)
		}
	}

	newTasks, err := fn(tasks)
	if err != nil {
		return err
	}

	newData, err := json.MarshalIndent(newTasks, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tasks: %w", err)
	}

	tmpPath := s.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, newData, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func (s *TaskStore) Load() ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var tasks []Task
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Task{}, nil
		}
		return nil, fmt.Errorf("failed to read tasks file: %w", err)
	}

	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tasks: %w", err)
	}

	return tasks, nil
}

func (s *TaskStore) GetTask(id string) (*Task, error) {
	tasks, err := s.Load()
	if err != nil {
		return nil, err
	}

	for _, t := range tasks {
		if t.ID == id {
			return &t, nil
		}
	}

	return nil, fmt.Errorf("task with ID %s not found", id)
}

func (s *TaskStore) CreateTask(kind, subject, description string, dependsOn []string, parentTaskID string) (string, error) {
	var newID string
	err := s.updateLocked(func(tasks []Task) ([]Task, error) {
		maxID := 0
		for _, t := range tasks {
			var idInt int
			if _, err := fmt.Sscanf(t.ID, "%d", &idInt); err == nil && idInt > maxID {
				maxID = idInt
			}
		}
		newID = fmt.Sprintf("%d", maxID+1)

		now := time.Now().Unix()

		newTask := Task{
			ID:           newID,
			Kind:         kind,
			Subject:      subject,
			Description:  description,
			Status:       StatusPending,
			DependsOn:    dependsOn,
			ParentTaskID: parentTaskID,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if newTask.DependsOn == nil {
			newTask.DependsOn = []string{}
		}

		// Ensure dependencies exist
		if len(newTask.DependsOn) > 0 {
			taskMap := make(map[string]bool)
			for _, t := range tasks {
				taskMap[t.ID] = true
			}
			for _, depID := range newTask.DependsOn {
				if !taskMap[depID] {
					return nil, fmt.Errorf("dependency task ID %s does not exist", depID)
				}
			}
		}

		return append(tasks, newTask), nil
	})

	return newID, err
}

func validateTransition(current, next TaskStatus) error {
	if current == next {
		return nil
	}
	switch current {
	case StatusPending:
		if next != StatusInProgress && next != StatusCancelled {
			return fmt.Errorf("invalid transition from pending to %s", next)
		}
	case StatusInProgress:
		if next != StatusVerifying && next != StatusFailed && next != StatusCancelled && next != StatusCompleted {
			return fmt.Errorf("invalid transition from in_progress to %s", next)
		}
	case StatusVerifying:
		if next != StatusCompleted && next != StatusFailed && next != StatusCancelled {
			return fmt.Errorf("invalid transition from verifying to %s", next)
		}
	case StatusFailed:
		if next != StatusInProgress && next != StatusCancelled {
			return fmt.Errorf("invalid transition from failed to %s", next)
		}
	case StatusCompleted:
		if next != StatusInProgress {
			return fmt.Errorf("invalid transition from completed to %s", next)
		}
	case StatusCancelled:
		if next != StatusInProgress {
			return fmt.Errorf("invalid transition from cancelled to %s", next)
		}
	}
	return nil
}

func (s *TaskStore) UpdateTask(id string, patch TaskPatch) error {
	return s.updateLocked(func(tasks []Task) ([]Task, error) {
		taskMap := make(map[string]*Task)
		for i := range tasks {
			taskMap[tasks[i].ID] = &tasks[i]
		}

		task, exists := taskMap[id]
		if !exists {
			return nil, fmt.Errorf("task with ID %s not found", id)
		}

		now := time.Now().Unix()

		if patch.Status != nil {
			newStatus := *patch.Status
			if err := validateTransition(task.Status, newStatus); err != nil {
				return nil, err
			}

			if newStatus == StatusInProgress {
				// check dependencies
				for _, depID := range task.DependsOn {
					dep, exists := taskMap[depID]
					if !exists {
						return nil, fmt.Errorf("dependency task #%s not found", depID)
					}
					if dep.Status != StatusCompleted {
						return nil, fmt.Errorf("cannot start task: Blocked by pending task #%s (%s)", depID, dep.Subject)
					}
				}
				if task.StartedAt == 0 {
					task.StartedAt = now
				}
				if task.Status == StatusFailed {
					task.RetryCount++
				}
			}

			if newStatus == StatusCompleted {
				task.CompletedAt = now
			}

			task.Status = newStatus
		}

		if patch.Subject != nil {
			task.Subject = *patch.Subject
		}
		if patch.Description != nil {
			task.Description = *patch.Description
		}
		if patch.Summary != nil {
			task.Summary = *patch.Summary
		}
		if patch.LastError != nil {
			task.LastError = *patch.LastError
		}
		if patch.ResultPath != nil {
			task.ResultPath = *patch.ResultPath
		}
		if patch.DependsOn != nil {
			dependsOn := *patch.DependsOn
			// Check for circular dependencies
			for _, depID := range dependsOn {
				if depID == id {
					return nil, fmt.Errorf("task cannot depend on itself")
				}
				if _, exists := taskMap[depID]; !exists {
					return nil, fmt.Errorf("cannot depend on non-existent task #%s", depID)
				}
				if wouldCreateCycle(taskMap, depID, id) {
					return nil, fmt.Errorf("cannot add dependency: creates a circular loop")
				}
			}
			task.DependsOn = dependsOn
		}

		task.UpdatedAt = now

		return tasks, nil
	})
}

func wouldCreateCycle(taskMap map[string]*Task, startID, targetID string) bool {
	visited := make(map[string]bool)
	var visit func(string) bool
	visit = func(currentID string) bool {
		if currentID == targetID {
			return true
		}
		if visited[currentID] {
			return false
		}
		visited[currentID] = true
		task, exists := taskMap[currentID]
		if !exists {
			return false
		}
		for _, depID := range task.DependsOn {
			if visit(depID) {
				return true
			}
		}
		return false
	}
	return visit(startID)
}

func (s *TaskStore) DeleteTask(id string) error {
	return s.updateLocked(func(tasks []Task) ([]Task, error) {
		newTasks := make([]Task, 0, len(tasks)-1)
		found := false
		for _, t := range tasks {
			if t.ID == id {
				found = true
				continue
			}
			t.DependsOn = filterString(t.DependsOn, id)
			newTasks = append(newTasks, t)
		}

		if !found {
			return nil, fmt.Errorf("task with ID %s not found", id)
		}
		return newTasks, nil
	})
}

func filterString(slice []string, val string) []string {
	result := []string{}
	for _, s := range slice {
		if s != val {
			result = append(result, s)
		}
	}
	return result
}
