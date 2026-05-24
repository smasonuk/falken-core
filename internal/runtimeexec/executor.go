package runtimeexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
)

var (
	// ErrExecutionStateRequired indicates a nil execution state was supplied.
	ErrExecutionStateRequired = errors.New("execution state is required")
	// ErrPolicyEvaluatorRequired indicates a command executor has no runtime policy evaluator.
	ErrPolicyEvaluatorRequired = errors.New("runtime policy evaluator is required")
	// ErrSandboxRuntimeRequired indicates a sandbox executor has no sandbox runtime.
	ErrSandboxRuntimeRequired = errors.New("sandbox runtime is required")
)

// CommandStatus identifies the lifecycle outcome of a command request.
type CommandStatus string

const (
	CommandStatusBlocked        CommandStatus = "blocked"
	CommandStatusFailedToStart  CommandStatus = "failed_to_start"
	CommandStatusExecutionError CommandStatus = "execution_error"
	CommandStatusSucceeded      CommandStatus = "succeeded"
	CommandStatusExitedNonZero  CommandStatus = "exited_non_zero"
)

// StreamName identifies a command output stream.
type StreamName string

const (
	StreamStdout StreamName = "stdout"
	StreamStderr StreamName = "stderr"
)

// StreamChunk is emitted to an optional request stream sink as output arrives.
type StreamChunk struct {
	Stream StreamName
	Data   []byte
}

// StreamSink receives command output chunks during execution.
type StreamSink func(StreamChunk)

// CommandRequest is the explicit command execution request shape.
type CommandRequest struct {
	Command          string            `json:"command"`
	WorkingDir       string            `json:"working_dir,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	ApprovalRequired bool              `json:"approval_required,omitempty"`
	MaxInlineOutput  int               `json:"max_inline_output,omitempty"`
	Stream           StreamSink        `json:"-"`
}

// CommandResult is the structured result of policy-gated command execution.
type CommandResult struct {
	Status            CommandStatus             `json:"status"`
	Executed          bool                      `json:"executed"`
	Command           string                    `json:"command"`
	WorkingDir        string                    `json:"working_dir,omitempty"`
	ToolWorkingDir    string                    `json:"tool_working_dir,omitempty"`
	HostWorkingDir    string                    `json:"host_working_dir,omitempty"`
	SandboxWorkingDir string                    `json:"sandbox_working_dir,omitempty"`
	Stdout            string                    `json:"stdout,omitempty"`
	Stderr            string                    `json:"stderr,omitempty"`
	CombinedOutput    string                    `json:"combined_output,omitempty"`
	ExitCode          int                       `json:"exit_code"`
	Policy            runtimepolicy.ShellResult `json:"policy"`
	Output            OutputSummary             `json:"output"`
	StartError        string                    `json:"start_error,omitempty"`
	ExecutionError    string                    `json:"execution_error,omitempty"`
	ExitError         string                    `json:"exit_error,omitempty"`
	CleanupError      string                    `json:"cleanup_error,omitempty"`
}

// CommandExecutor is the command execution abstraction future backends can satisfy.
type CommandExecutor interface {
	Execute(context.Context, *ExecutionState, CommandRequest) (CommandResult, error)
}

// LocalExecutor runs policy-approved commands as local shell processes.
type LocalExecutor struct {
	policy    *runtimepolicy.Evaluator
	shellPath string
}

// NewLocalExecutor creates the initial local process command executor.
func NewLocalExecutor(policy *runtimepolicy.Evaluator) *LocalExecutor {
	return NewLocalExecutorWithShell(policy, "/bin/sh")
}

// NewLocalExecutorWithShell creates a local executor using an explicit shell path.
func NewLocalExecutorWithShell(policy *runtimepolicy.Evaluator, shellPath string) *LocalExecutor {
	return &LocalExecutor{
		policy:    policy,
		shellPath: shellPath,
	}
}

// Execute evaluates shell policy and runs an allowed command locally.
func (e *LocalExecutor) Execute(ctx context.Context, state *ExecutionState, request CommandRequest) (CommandResult, error) {
	if state == nil {
		return CommandResult{}, ErrExecutionStateRequired
	}
	if e.policy == nil {
		return CommandResult{}, ErrPolicyEvaluatorRequired
	}

	policyResult, err := e.policy.EvaluateShell(ctx, runtimepolicy.ShellRequest{
		Command:          request.Command,
		ApprovalRequired: request.ApprovalRequired,
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("evaluate shell policy: %w", err)
	}

	result := CommandResult{
		Command:  request.Command,
		Policy:   policyResult,
		ExitCode: -1,
	}
	if !policyResult.Allowed {
		result.Status = CommandStatusBlocked
		return result, nil
	}

	workingDir := state.CurrentWorkingDir()
	if request.WorkingDir != "" {
		workingDir, err = state.ResolveWorkingDir(request.WorkingDir)
		if err != nil {
			return CommandResult{}, fmt.Errorf("resolve command working directory: %w", err)
		}
	}
	result.WorkingDir = workingDir
	result.HostWorkingDir = workingDir
	result.ToolWorkingDir = state.ToolPathForHostPath(workingDir)
	result.SandboxWorkingDir = state.SandboxPathForHostPath(workingDir)

	outputCollectors := newCommandOutputCollectors(state, request.MaxInlineOutput)

	// #nosec G204 -- this executor is the policy-gated process boundary for shell commands.
	cmd := exec.Command(e.shellPath, "-c", request.Command)
	cmd.Dir = workingDir
	cmd.Env = state.MergedEnvironment(request.Env)
	cmd.Stdout = streamWriter{
		stream:   StreamStdout,
		primary:  outputCollectors.stdout,
		combined: outputCollectors.combined,
		sink:     request.Stream,
	}
	cmd.Stderr = streamWriter{
		stream:   StreamStderr,
		primary:  outputCollectors.stderr,
		combined: outputCollectors.combined,
		sink:     request.Stream,
	}

	if err := startProcessGroup(cmd); err != nil {
		result.Status = CommandStatusFailedToStart
		result.StartError = err.Error()
		return result, nil
	}

	result.Executed = true
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	cancelled := false
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		cancelled = true
		if err := stopProcess(cmd.Process); err != nil {
			result.CleanupError = err.Error()
		}
		waitErr = <-waitCh
	}
	output, outputErr := outputCollectors.result()
	if outputErr != nil {
		result.Status = CommandStatusExecutionError
		result.ExecutionError = outputErr.Error()
		return result, nil
	}
	result.Stdout = output.stdout
	result.Stderr = output.stderr
	result.CombinedOutput = output.combined
	result.Output = output.summary

	if cancelled {
		result.Status = CommandStatusExecutionError
		result.ExecutionError = ctx.Err().Error()
		return result, nil
	}

	if waitErr == nil {
		result.Status = CommandStatusSucceeded
		result.ExitCode = 0
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		result.Status = CommandStatusExitedNonZero
		result.ExitCode = exitErr.ExitCode()
		result.ExitError = waitErr.Error()
		return result, nil
	}

	result.Status = CommandStatusExecutionError
	result.ExecutionError = waitErr.Error()
	return result, nil
}

type streamWriter struct {
	stream   StreamName
	primary  io.Writer
	combined io.Writer
	sink     StreamSink
}

func (w streamWriter) Write(data []byte) (int, error) {
	if _, err := w.primary.Write(data); err != nil {
		return 0, err
	}
	if _, err := w.combined.Write(data); err != nil {
		return 0, err
	}
	if w.sink != nil {
		chunk := make([]byte, len(data))
		copy(chunk, data)
		w.sink(StreamChunk{
			Stream: w.stream,
			Data:   chunk,
		})
	}
	return len(data), nil
}
