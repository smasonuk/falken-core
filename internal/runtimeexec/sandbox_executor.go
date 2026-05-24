package runtimeexec

import (
	"context"
	"fmt"

	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	falkenruntime "github.com/smasonuk/falken-core/pkg/falken/runtime"
)

// SandboxExecutor runs policy-approved commands through a sandbox runtime.
type SandboxExecutor struct {
	policy  *runtimepolicy.Evaluator
	runtime falkenruntime.SandboxRuntime
}

// NewSandboxExecutor creates a command executor backed by a sandbox runtime.
func NewSandboxExecutor(policy *runtimepolicy.Evaluator, runtime falkenruntime.SandboxRuntime) *SandboxExecutor {
	return &SandboxExecutor{
		policy:  policy,
		runtime: runtime,
	}
}

// Execute evaluates shell policy and delegates allowed commands to the sandbox runtime.
func (e *SandboxExecutor) Execute(ctx context.Context, state *ExecutionState, request CommandRequest) (CommandResult, error) {
	if state == nil {
		return CommandResult{}, ErrExecutionStateRequired
	}
	if e.policy == nil {
		return CommandResult{}, ErrPolicyEvaluatorRequired
	}
	if e.runtime == nil {
		return CommandResult{}, ErrSandboxRuntimeRequired
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
			return CommandResult{}, fmt.Errorf("resolve sandbox command working directory: %w", err)
		}
	}
	result.WorkingDir = workingDir
	result.HostWorkingDir = workingDir
	result.ToolWorkingDir = state.ToolPathForHostPath(workingDir)
	result.SandboxWorkingDir = state.SandboxPathForHostPath(workingDir)

	outputCollectors := newCommandOutputCollectors(state, request.MaxInlineOutput)

	sandboxResult, execErr := executeInSandbox(ctx, e.runtime, falkenruntime.SandboxCommandRequest{
		Command:        request.Command,
		HostWorkingDir: workingDir,
		Env:            state.SandboxEnvironment(request.Env),
		Stdout: streamWriter{
			stream:   StreamStdout,
			primary:  outputCollectors.stdout,
			combined: outputCollectors.combined,
			sink:     request.Stream,
		},
		Stderr: streamWriter{
			stream:   StreamStderr,
			primary:  outputCollectors.stderr,
			combined: outputCollectors.combined,
			sink:     request.Stream,
		},
	})
	if execErr != "" {
		result.Status = CommandStatusExecutionError
		result.ExecutionError = execErr
		return result, nil
	}

	result.CleanupError = sandboxResult.CleanupError
	if sandboxResult.StartError != "" {
		result.Status = CommandStatusFailedToStart
		result.StartError = sandboxResult.StartError
		return result, nil
	}

	result.Executed = sandboxResult.Started
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

	if sandboxResult.RuntimeError != "" {
		result.Status = CommandStatusExecutionError
		result.ExecutionError = sandboxResult.RuntimeError
		return result, nil
	}
	if sandboxResult.ExitError != "" {
		result.Status = CommandStatusExitedNonZero
		result.ExitCode = sandboxResult.ExitCode
		result.ExitError = sandboxResult.ExitError
		return result, nil
	}

	result.Status = CommandStatusSucceeded
	result.ExitCode = sandboxResult.ExitCode
	return result, nil
}

func executeInSandbox(ctx context.Context, runtime falkenruntime.SandboxRuntime, request falkenruntime.SandboxCommandRequest) (falkenruntime.SandboxCommandResult, string) {
	result, err := runtime.Execute(ctx, request)
	if err != nil {
		return falkenruntime.SandboxCommandResult{}, err.Error()
	}
	return result, ""
}
