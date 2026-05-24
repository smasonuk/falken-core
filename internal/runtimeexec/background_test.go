package runtimeexec_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
)

func TestBackgroundStartBlockedCommandDenied(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManager(policy.Config{
		BlockedShell: []policy.ShellRule{{
			Command: "touch marker",
			Match:   policy.MatchExact,
		}},
	})

	result, err := manager.Start(context.Background(), state, runtimeexec.BackgroundStartRequest{
		Command: "touch marker",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Status != runtimeexec.BackgroundStartDenied {
		t.Fatalf("expected denied start result, got %+v", result)
	}
	if result.ProcessID != "" {
		t.Fatalf("denied start should not allocate process ID, got %+v", result)
	}
	if result.Policy.Outcome != runtimepolicy.ShellOutcomeDenied {
		t.Fatalf("expected denied shell policy outcome, got %+v", result.Policy)
	}
	if _, err := os.Stat(filepath.Join(root, "marker")); !os.IsNotExist(err) {
		t.Fatalf("blocked background command should not create marker, stat err=%v", err)
	}
}

func TestBackgroundStartShellWriteDenied(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManagerWithHandler(policy.Config{}, approveShellHandler{})

	result, err := manager.Start(context.Background(), state, runtimeexec.BackgroundStartRequest{
		Command: "echo hi > marker",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Status != runtimeexec.BackgroundStartDenied {
		t.Fatalf("expected shell-write start denial, got %+v", result)
	}
	if !result.Policy.BlockedByShellWriteBypass {
		t.Fatalf("expected shell-write policy flag, got %+v", result.Policy)
	}
	if _, err := os.Stat(filepath.Join(root, "marker")); !os.IsNotExist(err) {
		t.Fatalf("shell-write background command should not create marker, stat err=%v", err)
	}
}

func TestBackgroundStartCancelledContextDoesNotLaunchProcess(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManager(policy.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := manager.Start(ctx, state, runtimeexec.BackgroundStartRequest{
		Command: "sleep 5",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Status != runtimeexec.BackgroundStartFailedToStart {
		t.Fatalf("start status = %q, want failed_to_start; result=%+v", result.Status, result)
	}
	if result.ProcessID != "" {
		t.Fatalf("cancelled start allocated process ID: %+v", result)
	}
	if !strings.Contains(result.StartError, "context canceled") {
		t.Fatalf("start error = %q, want context canceled", result.StartError)
	}
	if manager.TrackedCount() != 0 {
		t.Fatalf("tracked count = %d, want zero", manager.TrackedCount())
	}
}

func TestBackgroundAllowedCommandStartsAndCanBeLookedUp(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManager(policy.Config{})

	first := startBackground(t, manager, state, "sleep 5")
	second := startBackground(t, manager, state, "sleep 5")
	t.Cleanup(func() {
		manager.StopAll()
	})

	if first.ProcessID == "" || second.ProcessID == "" {
		t.Fatalf("expected non-empty process IDs: first=%+v second=%+v", first, second)
	}
	if first.ProcessID == second.ProcessID {
		t.Fatalf("expected unique process IDs, both were %q", first.ProcessID)
	}
	if first.ProcessID != "bg-000001" || second.ProcessID != "bg-000002" {
		t.Fatalf("unexpected process IDs: first=%q second=%q", first.ProcessID, second.ProcessID)
	}

	snapshot, ok := manager.Get(first.ProcessID)
	if !ok {
		t.Fatalf("expected process %q to be found", first.ProcessID)
	}
	if snapshot.ID != first.ProcessID || snapshot.Command != "sleep 5" {
		t.Fatalf("unexpected process snapshot: %+v", snapshot)
	}
	if snapshot.Status != runtimeexec.BackgroundProcessRunning {
		t.Fatalf("expected running process, got %+v", snapshot)
	}

	if _, ok := manager.Get("bg-missing"); ok {
		t.Fatal("expected missing process lookup to return false")
	}
}

func TestBackgroundReadUnknownProcessReturnsNotFound(t *testing.T) {
	manager := newBackgroundManager(policy.Config{})

	result := manager.ReadLogs("bg-missing")
	if result.Status != runtimeexec.BackgroundReadNotFound {
		t.Fatalf("expected not-found read result, got %+v", result)
	}
}

func TestBackgroundLogsCanBeReadRepeatedly(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManager(policy.Config{})
	start := startBackground(t, manager, state, "printf 'hello'; sleep 5")
	t.Cleanup(func() {
		manager.StopAll()
	})

	waitFor(t, func() bool {
		return strings.Contains(manager.ReadLogs(start.ProcessID).CombinedOutput, "hello")
	})

	first := manager.ReadLogs(start.ProcessID)
	second := manager.ReadLogs(start.ProcessID)
	if first.Status != runtimeexec.BackgroundReadFound || second.Status != runtimeexec.BackgroundReadFound {
		t.Fatalf("expected found log reads: first=%+v second=%+v", first, second)
	}
	if first.CombinedOutput != second.CombinedOutput {
		t.Fatalf("repeated log reads should be deterministic: first=%q second=%q", first.CombinedOutput, second.CombinedOutput)
	}
	if first.Stdout != "hello" || first.CombinedOutput != "hello" {
		t.Fatalf("unexpected logs: %+v", first)
	}
}

func TestBackgroundLogsAccumulateOverTime(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManager(policy.Config{})
	start := startBackground(t, manager, state, "printf 'first'; sleep 0.1; printf 'second'; sleep 5")
	t.Cleanup(func() {
		manager.StopAll()
	})

	waitFor(t, func() bool {
		return strings.Contains(manager.ReadLogs(start.ProcessID).CombinedOutput, "first")
	})
	early := manager.ReadLogs(start.ProcessID)
	if !strings.Contains(early.CombinedOutput, "first") {
		t.Fatalf("expected early logs to include first output, got %+v", early)
	}

	waitFor(t, func() bool {
		return strings.Contains(manager.ReadLogs(start.ProcessID).CombinedOutput, "firstsecond")
	})
	later := manager.ReadLogs(start.ProcessID)
	if later.CombinedOutput != "firstsecond" {
		t.Fatalf("expected accumulated logs, got %+v", later)
	}
}

func TestBackgroundStopRunningProcess(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManager(policy.Config{})
	start := startBackground(t, manager, state, "sleep 5")

	result := manager.Stop(start.ProcessID)
	if result.Status != runtimeexec.BackgroundStopStopped {
		t.Fatalf("expected stopped result, got %+v", result)
	}

	waitFor(t, func() bool {
		snapshot, ok := manager.Get(start.ProcessID)
		return ok && snapshot.Status == runtimeexec.BackgroundProcessStopped
	})
}

func TestBackgroundStopUnknownProcessReturnsNotFound(t *testing.T) {
	manager := newBackgroundManager(policy.Config{})

	result := manager.Stop("bg-missing")
	if result.Status != runtimeexec.BackgroundStopNotFound {
		t.Fatalf("expected not-found stop result, got %+v", result)
	}
}

func TestBackgroundStopAlreadyExitedProcess(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManager(policy.Config{})
	start := startBackground(t, manager, state, "printf 'done'")

	waitFor(t, func() bool {
		snapshot, ok := manager.Get(start.ProcessID)
		return ok && snapshot.Status == runtimeexec.BackgroundProcessExited
	})

	result := manager.Stop(start.ProcessID)
	if result.Status != runtimeexec.BackgroundStopAlreadyExited {
		t.Fatalf("expected already-exited stop result, got %+v", result)
	}
	if result.Process.Status != runtimeexec.BackgroundProcessExited {
		t.Fatalf("expected exited process snapshot, got %+v", result)
	}
}

func TestBackgroundStopAllStopsTrackedProcesses(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManager(policy.Config{})
	first := startBackground(t, manager, state, "sleep 5")
	second := startBackground(t, manager, state, "sleep 5")

	results := manager.StopAll()
	if len(results) != 2 {
		t.Fatalf("StopAll returned %d results, want 2: %+v", len(results), results)
	}
	for _, result := range results {
		if result.Status != runtimeexec.BackgroundStopStopped {
			t.Fatalf("expected stopped result from StopAll, got %+v", result)
		}
	}

	waitFor(t, func() bool {
		firstSnapshot, firstOK := manager.Get(first.ProcessID)
		secondSnapshot, secondOK := manager.Get(second.ProcessID)
		return firstOK && secondOK &&
			firstSnapshot.Status == runtimeexec.BackgroundProcessStopped &&
			secondSnapshot.Status == runtimeexec.BackgroundProcessStopped
	})

	again := manager.StopAll()
	if len(again) != 2 {
		t.Fatalf("second StopAll returned %d results, want 2: %+v", len(again), again)
	}
	for _, result := range again {
		if result.Status != runtimeexec.BackgroundStopAlreadyExited {
			t.Fatalf("expected idempotent already-exited result, got %+v", result)
		}
	}
}

func TestBackgroundStopKillsChildProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group termination is Unix-specific")
	}
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManagerWithHandler(policy.Config{}, approveShellHandler{})
	start := startBackground(t, manager, state, "sleep 100 & printf '%s' \"$!\"; wait")

	var pid int
	waitFor(t, func() bool {
		logs := manager.ReadLogs(start.ProcessID)
		parsed, err := strconv.Atoi(strings.TrimSpace(logs.Stdout))
		if err != nil {
			return false
		}
		pid = parsed
		return processExists(pid)
	})

	result := manager.Stop(start.ProcessID)
	if result.Status != runtimeexec.BackgroundStopStopped {
		t.Fatalf("Stop = %+v, want stopped", result)
	}
	waitFor(t, func() bool {
		return !processExists(pid)
	})
}

func TestBackgroundCleanupExitedRemovesProcesses(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	manager := newBackgroundManager(policy.Config{})
	first := startBackground(t, manager, state, "printf first")
	second := startBackground(t, manager, state, "printf second")

	waitFor(t, func() bool {
		firstSnapshot, firstOK := manager.Get(first.ProcessID)
		secondSnapshot, secondOK := manager.Get(second.ProcessID)
		return firstOK && secondOK &&
			firstSnapshot.Status == runtimeexec.BackgroundProcessExited &&
			secondSnapshot.Status == runtimeexec.BackgroundProcessExited
	})
	if manager.TrackedCount() != 2 {
		t.Fatalf("tracked count before cleanup = %d, want 2", manager.TrackedCount())
	}
	if removed := manager.CleanupExited(); removed != 2 {
		t.Fatalf("CleanupExited removed %d, want 2", removed)
	}
	if manager.TrackedCount() != 0 {
		t.Fatalf("tracked count after cleanup = %d, want 0", manager.TrackedCount())
	}
}

func TestBackgroundLaunchFailureIsStructured(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{}, nil))
	manager := runtimeexec.NewBackgroundManagerWithShell(evaluator, filepath.Join(root, "missing-shell"))

	result, err := manager.Start(context.Background(), state, runtimeexec.BackgroundStartRequest{
		Command: "printf 'hello'",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Status != runtimeexec.BackgroundStartFailedToStart {
		t.Fatalf("expected failed-to-start result, got %+v", result)
	}
	if result.ProcessID != "" {
		t.Fatalf("failed start should not allocate process ID, got %+v", result)
	}
	if result.StartError == "" {
		t.Fatalf("expected start error to be recorded, got %+v", result)
	}
}

func startBackground(t *testing.T, manager *runtimeexec.BackgroundManager, state *runtimeexec.ExecutionState, command string) runtimeexec.BackgroundStartResult {
	t.Helper()

	result, err := manager.Start(context.Background(), state, runtimeexec.BackgroundStartRequest{
		Command: command,
	})
	if err != nil {
		t.Fatalf("Start %q: %v", command, err)
	}
	if result.Status != runtimeexec.BackgroundStartStarted {
		t.Fatalf("expected %q to start, got %+v", command, result)
	}
	if result.ProcessID == "" {
		t.Fatalf("expected %q to receive process ID, got %+v", command, result)
	}
	return result
}

func newBackgroundManager(config policy.Config) *runtimeexec.BackgroundManager {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(config, nil))
	return runtimeexec.NewBackgroundManager(evaluator)
}

func newBackgroundManagerWithHandler(config policy.Config, handler policy.ApprovalHandler) *runtimeexec.BackgroundManager {
	evaluator := runtimepolicy.NewEvaluator(policy.NewEngine(config, handler))
	return runtimeexec.NewBackgroundManager(evaluator)
}

func waitFor(t *testing.T, ready func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
