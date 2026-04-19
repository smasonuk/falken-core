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

func TestIsValidTaskStatus(t *testing.T) {
	valid := []TaskStatus{StatusPending, StatusInProgress, StatusVerifying, StatusCompleted, StatusFailed, StatusCancelled}
	for _, s := range valid {
		if !IsValidTaskStatus(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
	invalid := []TaskStatus{"banana", "", "deleted", "COMPLETED"}
	for _, s := range invalid {
		if IsValidTaskStatus(s) {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}

func TestUpdateTask_InvalidStatusString(t *testing.T) {
	store := newTestTaskStore(t)
	id := mustCreateTask(t, store, "task", nil)

	for _, bad := range []TaskStatus{"banana", "", "deleted"} {
		if err := store.UpdateTask(id, TaskPatch{Status: statusPtr(bad)}); err == nil {
			t.Errorf("expected error for invalid status %q, got nil", bad)
		}
	}
}

func TestCreateTask_NonexistentDependency(t *testing.T) {
	store := newTestTaskStore(t)
	_, err := store.CreateTask("subagent", "task", "desc", []string{"999"}, "")
	if err == nil {
		t.Fatal("expected error for nonexistent dependency, got nil")
	}
}

func TestCreateTask_SelfDependency(t *testing.T) {
	// Self-dependency via ID assigned at creation is impossible via the normal path
	// (the task doesn't know its own ID before creation), but the guard should still
	// catch any future cases where the generated ID matches a supplied dep ID.
	// Here we seed a task so ID "2" will be next, then try to create a task with dep "2".
	store := newTestTaskStore(t)
	_ = mustCreateTask(t, store, "seed", nil) // gets ID "1"

	// Try to depend on "2" which will be this task's own ID
	_, err := store.CreateTask("subagent", "self-dep", "desc", []string{"2"}, "")
	// "2" doesn't exist yet, so it should fail with nonexistent-dep error
	if err == nil {
		t.Fatal("expected error when depending on own (not yet existing) ID")
	}
}
