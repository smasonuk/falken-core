package tasks

import (
	"path/filepath"
	"testing"
)

func newTestTaskStore(t *testing.T) *TaskStore {
	t.Helper()
	return NewTaskStore(filepath.Join(t.TempDir(), "tasks.json"))
}

func strPtr(s string) *string { return &s }

func statusPtr(s TaskStatus) *TaskStatus { return &s }

func slicePtr(v []string) *[]string { return &v }

func mustCreateTask(t *testing.T, store *TaskStore, subject string, dependsOn []string) string {
	t.Helper()
	id, err := store.CreateTask("subagent", subject, subject+" description", dependsOn, "")
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	return id
}

func TestTaskStoreUpdateTask_ValidStatusTransition(t *testing.T) {
	store := newTestTaskStore(t)
	id := mustCreateTask(t, store, "task", nil)

	if err := store.UpdateTask(id, TaskPatch{Status: statusPtr(StatusInProgress)}); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	task, err := store.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task.Status != StatusInProgress {
		t.Fatalf("expected status %s, got %s", StatusInProgress, task.Status)
	}
	if task.StartedAt == 0 {
		t.Fatalf("expected StartedAt to be set")
	}
}

func TestTaskStoreUpdateTask_InvalidStatusTransition(t *testing.T) {
	store := newTestTaskStore(t)
	id := mustCreateTask(t, store, "task", nil)

	if err := store.UpdateTask(id, TaskPatch{Status: statusPtr(StatusCompleted)}); err == nil {
		t.Fatalf("expected invalid transition to fail")
	}
}

func TestTaskStoreUpdateTask_DependencyEnforcement(t *testing.T) {
	store := newTestTaskStore(t)
	depID := mustCreateTask(t, store, "dep", nil)
	taskID := mustCreateTask(t, store, "task", []string{depID})

	if err := store.UpdateTask(taskID, TaskPatch{Status: statusPtr(StatusInProgress)}); err == nil {
		t.Fatalf("expected blocked dependency transition to fail")
	}

	if err := store.UpdateTask(depID, TaskPatch{Status: statusPtr(StatusInProgress)}); err != nil {
		t.Fatalf("starting dep failed: %v", err)
	}
	if err := store.UpdateTask(depID, TaskPatch{Status: statusPtr(StatusCompleted)}); err != nil {
		t.Fatalf("completing dep failed: %v", err)
	}
	if err := store.UpdateTask(taskID, TaskPatch{Status: statusPtr(StatusInProgress)}); err != nil {
		t.Fatalf("expected dependent task to start after dependency completion: %v", err)
	}
}

func TestTaskStoreUpdateTask_CycleDetection(t *testing.T) {
	store := newTestTaskStore(t)
	aID := mustCreateTask(t, store, "a", nil)
	bID := mustCreateTask(t, store, "b", []string{aID})

	if err := store.UpdateTask(aID, TaskPatch{DependsOn: slicePtr([]string{bID})}); err == nil {
		t.Fatalf("expected circular dependency to fail")
	}
}

func TestTaskStoreUpdateTask_CompletedTaskTimestampSet(t *testing.T) {
	store := newTestTaskStore(t)
	id := mustCreateTask(t, store, "task", nil)

	if err := store.UpdateTask(id, TaskPatch{Status: statusPtr(StatusInProgress)}); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := store.UpdateTask(id, TaskPatch{Status: statusPtr(StatusCompleted)}); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	task, err := store.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task.CompletedAt == 0 {
		t.Fatalf("expected CompletedAt to be set")
	}
}

func TestTaskStoreUpdateTask_RetryCountIncrementsFailedToInProgress(t *testing.T) {
	store := newTestTaskStore(t)
	id := mustCreateTask(t, store, "task", nil)

	if err := store.UpdateTask(id, TaskPatch{Status: statusPtr(StatusInProgress)}); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := store.UpdateTask(id, TaskPatch{Status: statusPtr(StatusFailed), LastError: strPtr("boom")}); err != nil {
		t.Fatalf("fail failed: %v", err)
	}
	if err := store.UpdateTask(id, TaskPatch{Status: statusPtr(StatusInProgress)}); err != nil {
		t.Fatalf("restart failed: %v", err)
	}

	task, err := store.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task.RetryCount != 1 {
		t.Fatalf("expected RetryCount 1, got %d", task.RetryCount)
	}
}
