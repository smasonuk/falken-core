package runtimeexec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
)

// ErrBackgroundProcessNotFound indicates a background process ID is not tracked.
var ErrBackgroundProcessNotFound = errors.New("background process not found")

// BackgroundProcessStatus describes the tracked lifecycle state of a background process.
type BackgroundProcessStatus string

const (
	BackgroundProcessRunning BackgroundProcessStatus = "running"
	BackgroundProcessExited  BackgroundProcessStatus = "exited"
	BackgroundProcessStopped BackgroundProcessStatus = "stopped"
)

// BackgroundStartStatus describes the outcome of a background start request.
type BackgroundStartStatus string

const (
	BackgroundStartStarted       BackgroundStartStatus = "started"
	BackgroundStartDenied        BackgroundStartStatus = "denied"
	BackgroundStartFailedToStart BackgroundStartStatus = "failed_to_start"
)

// BackgroundReadStatus describes the outcome of a background log read request.
type BackgroundReadStatus string

const (
	BackgroundReadFound    BackgroundReadStatus = "found"
	BackgroundReadNotFound BackgroundReadStatus = "not_found"
)

// BackgroundStopStatus describes the outcome of a background stop request.
type BackgroundStopStatus string

const (
	BackgroundStopStopped       BackgroundStopStatus = "stopped"
	BackgroundStopNotFound      BackgroundStopStatus = "not_found"
	BackgroundStopAlreadyExited BackgroundStopStatus = "already_exited"
)

// BackgroundStartRequest is the explicit request shape for starting a background process.
type BackgroundStartRequest struct {
	Command          string
	WorkingDir       string
	Env              map[string]string
	ApprovalRequired bool
	Stream           StreamSink
}

// BackgroundStartResult reports whether a background process was started.
type BackgroundStartResult struct {
	Status     BackgroundStartStatus
	ProcessID  string
	Command    string
	WorkingDir string
	Policy     runtimepolicy.ShellResult
	StartError string
}

// BackgroundProcessSnapshot is a read-only view of a tracked background process.
type BackgroundProcessSnapshot struct {
	ID         string
	Command    string
	WorkingDir string
	Status     BackgroundProcessStatus
	ExitCode   int
	ExitError  string
}

// BackgroundLogsResult contains accumulated background process output.
type BackgroundLogsResult struct {
	Status         BackgroundReadStatus
	Process        BackgroundProcessSnapshot
	Stdout         string
	Stderr         string
	CombinedOutput string
}

// BackgroundStopResult reports the outcome of stopping a background process.
type BackgroundStopResult struct {
	Status  BackgroundStopStatus
	Process BackgroundProcessSnapshot
}

// BackgroundManager owns background processes for an execution runtime/session.
type BackgroundManager struct {
	mu           sync.Mutex
	policy       *runtimepolicy.Evaluator
	shellPath    string
	nextID       uint64
	processes    map[string]*backgroundProcess
	maxCompleted int
}

type backgroundProcess struct {
	mu          sync.Mutex
	id          string
	command     string
	workingDir  string
	status      BackgroundProcessStatus
	exitCode    int
	exitError   string
	cmd         *exec.Cmd
	stdout      *outputCollector
	stderr      *outputCollector
	combined    *outputCollector
	completedAt int64
}

// NewBackgroundManager creates a local background-process manager.
func NewBackgroundManager(policy *runtimepolicy.Evaluator) *BackgroundManager {
	return NewBackgroundManagerWithShell(policy, "/bin/sh")
}

// NewBackgroundManagerWithShell creates a local background manager with an explicit shell path.
func NewBackgroundManagerWithShell(policy *runtimepolicy.Evaluator, shellPath string) *BackgroundManager {
	return &BackgroundManager{
		policy:       policy,
		shellPath:    shellPath,
		processes:    make(map[string]*backgroundProcess),
		maxCompleted: 32,
	}
}

// Start evaluates policy and starts a command asynchronously when allowed.
func (m *BackgroundManager) Start(ctx context.Context, state *ExecutionState, request BackgroundStartRequest) (BackgroundStartResult, error) {
	if state == nil {
		return BackgroundStartResult{}, ErrExecutionStateRequired
	}
	if m.policy == nil {
		return BackgroundStartResult{}, ErrPolicyEvaluatorRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return BackgroundStartResult{
			Status:     BackgroundStartFailedToStart,
			Command:    request.Command,
			StartError: err.Error(),
		}, nil
	}

	policyResult, err := m.policy.EvaluateShell(ctx, runtimepolicy.ShellRequest{
		Command:          request.Command,
		ApprovalRequired: request.ApprovalRequired,
	})
	if err != nil {
		return BackgroundStartResult{}, fmt.Errorf("evaluate shell policy: %w", err)
	}

	result := BackgroundStartResult{
		Status:  BackgroundStartDenied,
		Command: request.Command,
		Policy:  policyResult,
	}
	if !policyResult.Allowed {
		return result, nil
	}

	workingDir := state.CurrentWorkingDir()
	if request.WorkingDir != "" {
		workingDir, err = state.ResolveWorkingDir(request.WorkingDir)
		if err != nil {
			return BackgroundStartResult{}, fmt.Errorf("resolve background working directory: %w", err)
		}
	}
	result.WorkingDir = workingDir

	if err := ctx.Err(); err != nil {
		result.Status = BackgroundStartFailedToStart
		result.StartError = err.Error()
		return result, nil
	}

	stdout := newOutputCollector(DefaultMaxInlineOutput, "", false)
	stderr := newOutputCollector(DefaultMaxInlineOutput, "", false)
	combined := newOutputCollector(DefaultMaxInlineOutput, "", false)

	// #nosec G204 -- background process startup is policy-gated before this boundary.
	cmd := exec.CommandContext(context.Background(), m.shellPath, "-c", request.Command)
	cmd.Dir = workingDir
	cmd.Env = state.MergedEnvironment(request.Env)
	cmd.Stdout = streamWriter{
		stream:   StreamStdout,
		primary:  stdout,
		combined: combined,
		sink:     request.Stream,
	}
	cmd.Stderr = streamWriter{
		stream:   StreamStderr,
		primary:  stderr,
		combined: combined,
		sink:     request.Stream,
	}

	if err := startProcessGroup(cmd); err != nil {
		result.Status = BackgroundStartFailedToStart
		result.StartError = err.Error()
		return result, nil
	}

	process := &backgroundProcess{
		id:         m.nextProcessID(),
		command:    request.Command,
		workingDir: workingDir,
		status:     BackgroundProcessRunning,
		exitCode:   -1,
		cmd:        cmd,
		stdout:     stdout,
		stderr:     stderr,
		combined:   combined,
	}

	m.mu.Lock()
	m.processes[process.id] = process
	m.gcCompletedLocked()
	m.mu.Unlock()

	go process.wait()

	result.Status = BackgroundStartStarted
	result.ProcessID = process.id
	return result, nil
}

// Get returns a snapshot for a tracked background process.
func (m *BackgroundManager) Get(id string) (BackgroundProcessSnapshot, bool) {
	process, ok := m.lookup(id)
	if !ok {
		return BackgroundProcessSnapshot{}, false
	}
	return process.snapshot(), true
}

// ReadLogs returns accumulated output for a tracked background process.
func (m *BackgroundManager) ReadLogs(id string) BackgroundLogsResult {
	process, ok := m.lookup(id)
	if !ok {
		return BackgroundLogsResult{Status: BackgroundReadNotFound}
	}
	stdout, _, _ := process.stdout.snapshot()
	stderr, _, _ := process.stderr.snapshot()
	combined, _, _ := process.combined.snapshot()

	return BackgroundLogsResult{
		Status:         BackgroundReadFound,
		Process:        process.snapshot(),
		Stdout:         stdout,
		Stderr:         stderr,
		CombinedOutput: combined,
	}
}

// Stop stops a tracked background process or reports its deterministic terminal state.
func (m *BackgroundManager) Stop(id string) BackgroundStopResult {
	process, ok := m.lookup(id)
	if !ok {
		return BackgroundStopResult{Status: BackgroundStopNotFound}
	}

	return process.stop()
}

// StopAll stops every tracked background process. It is safe to call repeatedly.
func (m *BackgroundManager) StopAll() []BackgroundStopResult {
	m.mu.Lock()
	processes := make([]*backgroundProcess, 0, len(m.processes))
	for _, process := range m.processes {
		processes = append(processes, process)
	}
	m.mu.Unlock()

	results := make([]BackgroundStopResult, 0, len(processes))
	for _, process := range processes {
		results = append(results, process.stop())
	}
	return results
}

// CleanupExited removes tracked terminal processes from the manager.
func (m *BackgroundManager) CleanupExited() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	removed := 0
	for id, process := range m.processes {
		snapshot := process.snapshot()
		if snapshot.Status == BackgroundProcessExited || snapshot.Status == BackgroundProcessStopped {
			delete(m.processes, id)
			removed++
		}
	}
	return removed
}

// TrackedCount reports the number of processes currently retained by the manager.
func (m *BackgroundManager) TrackedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.processes)
}

func (m *BackgroundManager) lookup(id string) (*backgroundProcess, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	process, ok := m.processes[id]
	return process, ok
}

func (m *BackgroundManager) gcCompletedLocked() {
	if m.maxCompleted <= 0 {
		return
	}
	completed := 0
	for _, process := range m.processes {
		snapshot := process.snapshot()
		if snapshot.Status == BackgroundProcessExited || snapshot.Status == BackgroundProcessStopped {
			completed++
		}
	}
	for completed > m.maxCompleted {
		var oldestID string
		var oldestAt int64
		for id, process := range m.processes {
			snapshot := process.snapshot()
			if snapshot.Status != BackgroundProcessExited && snapshot.Status != BackgroundProcessStopped {
				continue
			}
			process.mu.Lock()
			completedAt := process.completedAt
			process.mu.Unlock()
			if oldestID == "" || completedAt < oldestAt {
				oldestID = id
				oldestAt = completedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(m.processes, oldestID)
		completed--
	}
}

func (m *BackgroundManager) nextProcessID() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextID++
	return fmt.Sprintf("bg-%06d", m.nextID)
}

func (p *backgroundProcess) wait() {
	err := p.cmd.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.status == BackgroundProcessStopped {
		if err != nil {
			p.exitError = err.Error()
		}
		p.completedAt = time.Now().UnixNano()
		return
	}

	p.status = BackgroundProcessExited
	p.completedAt = time.Now().UnixNano()
	p.exitCode = 0
	if err != nil {
		p.exitError = err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			p.exitCode = exitErr.ExitCode()
		}
	}
}

func (p *backgroundProcess) stop() BackgroundStopResult {
	p.mu.Lock()
	if p.status == BackgroundProcessExited {
		snapshot := p.snapshotLocked()
		p.mu.Unlock()
		return BackgroundStopResult{Status: BackgroundStopAlreadyExited, Process: snapshot}
	}
	if p.status == BackgroundProcessStopped {
		snapshot := p.snapshotLocked()
		p.mu.Unlock()
		return BackgroundStopResult{Status: BackgroundStopAlreadyExited, Process: snapshot}
	}

	p.status = BackgroundProcessStopped
	p.exitCode = -1
	p.completedAt = time.Now().UnixNano()
	process := p.cmd.Process
	p.mu.Unlock()

	if process != nil {
		_ = stopProcess(process)
	}

	return BackgroundStopResult{
		Status:  BackgroundStopStopped,
		Process: p.snapshot(),
	}
}

func (p *backgroundProcess) snapshot() BackgroundProcessSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.snapshotLocked()
}

func (p *backgroundProcess) snapshotLocked() BackgroundProcessSnapshot {
	return BackgroundProcessSnapshot{
		ID:         p.id,
		Command:    p.command,
		WorkingDir: p.workingDir,
		Status:     p.status,
		ExitCode:   p.exitCode,
		ExitError:  p.exitError,
	}
}
