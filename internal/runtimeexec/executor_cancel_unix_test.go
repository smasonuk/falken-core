//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package runtimeexec_test

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/policy"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
)

func TestLocalExecutorCancellationStopsChildProcessGroup(t *testing.T) {
	root := t.TempDir()
	state := newExecutionState(t, root)
	executor := newExecutorWithHandler(policy.Config{}, approveShellHandler{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidCh := make(chan int, 1)
	resultCh := make(chan runtimeexec.CommandResult, 1)
	errCh := make(chan error, 1)

	go func() {
		result, err := executor.Execute(ctx, state, runtimeexec.CommandRequest{
			Command: "sleep 30 & echo child:$!; wait",
			Stream: func(chunk runtimeexec.StreamChunk) {
				text := string(chunk.Data)
				if !strings.Contains(text, "child:") {
					return
				}
				pidText := strings.TrimSpace(strings.TrimPrefix(text, "child:"))
				pid, err := strconv.Atoi(pidText)
				if err == nil {
					select {
					case pidCh <- pid:
					default:
					}
				}
			},
		})
		resultCh <- result
		errCh <- err
	}()

	var childPID int
	select {
	case childPID = <-pidCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for child pid")
	}
	cancel()

	var result runtimeexec.CommandResult
	select {
	case result = <-resultCh:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		t.Fatal("executor did not return after cancellation")
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusExecutionError || !strings.Contains(result.ExecutionError, "canceled") {
		t.Fatalf("result = %+v, want cancellation execution error", result)
	}
	if !strings.Contains(result.Stdout, "child:") {
		t.Fatalf("stdout = %q, want output emitted before cancellation", result.Stdout)
	}
	for i := 0; i < 40; i++ {
		if !processExists(childPID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(childPID, syscall.SIGKILL)
	t.Fatalf("child process %d still exists after cancellation", childPID)
}
