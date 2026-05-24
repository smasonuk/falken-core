package commandtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
)

// ExecuteCommandTool executes a policy-gated shell command and streams its output.
type ExecuteCommandTool struct{}

func (t *ExecuteCommandTool) Descriptor() api.Descriptor {
	return api.Descriptor{
		Name: "execute_command",
		Description: `Execute a policy-gated shell command and return its output.

CRITICAL USAGE RULES:
1. Verify after every edit: After modifying ANY file you MUST run the project's
   test suite, compiler, linter, typechecker, or a relevant runtime check.
   Falken records command evidence automatically. Do not label commands with
   verification intent.
2. Non-interactive only: Never run commands that require user input or open a
   TUI (e.g. vim, top, less). They will hang until killed.
3. No persistent servers: Do not start long-running processes here. Use a
   background process tool for servers or watchers.
4. Shell file writes are blocked: Commands that write files through shell
   redirection (">", ">>") or "tee <file>" are rejected automatically and
   cannot be approved. Use write_file, edit_file, multi_edit, delete_file, or
   apply_patch for file mutations.
5. Risky deletion commands need approval: Commands such as rm, git clean,
   and find are blocked from automatic execution by policy and require host
   approval when matched by approval-required shell rules.
6. Chain commands with && or | to reduce round-trips:
   e.g. "go build ./... && go test ./..."`,
		Parameters: api.MustSchema(api.ObjectSchema(map[string]any{
			"command": api.StringProp(
				"The full shell command to execute."),
			"working_dir": api.StringProp(
				"Optional working directory. Defaults to the workspace root."),
			"env": api.StringMapProp(
				"Optional environment variable overrides merged with the session environment."),
			"max_inline_output": api.IntegerProp(
				"Maximum bytes of combined output to include inline in the result. " +
					"Output beyond this threshold is written to an artifact file whose " +
					"path is returned in the command payload."),
		}, "command")),
		Category:    "command",
		Keywords:    []string{"shell", "command", "execute", "run", "bash"},
		AlwaysLoad:  true,
		DefaultLoad: true,
		Safety:      api.Safety{ExecutesShell: true},
	}
}

func (t *ExecuteCommandTool) Execute(ctx context.Context, host *api.Host, args json.RawMessage) (agent.ToolExecutionResult, error) {
	var params executeCommandParams
	if err := api.DecodeArgs(args, &params); err != nil {
		return api.Fail("invalid_arguments", err.Error()), nil
	}

	executor, state, err := host.RequireExecutor()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}

	var stream runtimeexec.StreamSink
	if sink := host.Events(); sink != nil {
		stream = func(chunk runtimeexec.StreamChunk) {
			sink(agent.CommandChunkEvent(chunk))
		}
	}

	result, err := executor.Execute(ctx, state, runtimeexec.CommandRequest{
		Command:         params.Command,
		WorkingDir:      params.WorkingDir,
		Env:             params.Env,
		MaxInlineOutput: params.MaxInlineOutput,
		Stream:          stream,
	})
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}

	if agent.ShouldRecordCommandEvidence(result) {
		if err := appendCommandEvidenceRecord(host, result); err != nil {
			return agent.ToolExecutionResult{}, err
		}
	}

	success := result.Executed && result.Status == runtimeexec.CommandStatusSucceeded
	return api.ResultFromPayload(
		success,
		string(result.Status),
		buildCommandContent(result),
		buildCommandPayload(result),
	)
}

func appendCommandEvidenceRecord(host *api.Host, result runtimeexec.CommandResult) error {
	state, err := host.RequireConversationState()
	if err != nil {
		if errors.Is(err, api.ErrHostUnavailable) {
			return nil
		}
		return err
	}
	record := agent.CommandEvidenceRecordFromCommandResult(result, time.Now().UTC().Format(time.RFC3339Nano))
	return state.AppendCommandEvidence(record)
}

type executeCommandParams struct {
	Command         string            `json:"command"`
	WorkingDir      string            `json:"working_dir"`
	Env             map[string]string `json:"env"`
	MaxInlineOutput int               `json:"max_inline_output"`
}

func buildCommandContent(r runtimeexec.CommandResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Command:   %s\n", r.Command)
	fmt.Fprintf(&b, "Status:    %s\n", r.Status)
	fmt.Fprintf(&b, "Exit code: %d\n", r.ExitCode)
	if r.ToolWorkingDir != "" || r.HostWorkingDir != "" || r.SandboxWorkingDir != "" {
		b.WriteString("\n--- path context ---\n")
		if r.ToolWorkingDir != "" {
			fmt.Fprintf(&b, "Tool working directory: %s\n", r.ToolWorkingDir)
		}
		if r.HostWorkingDir != "" {
			fmt.Fprintf(&b, "Host working directory: %s\n", r.HostWorkingDir)
		}
		if r.SandboxWorkingDir != "" {
			fmt.Fprintf(&b, "Sandbox working directory: %s\n", r.SandboxWorkingDir)
		}
		b.WriteString("Use workspace-relative paths in future tool calls.\n")
	}
	if errText := firstNonEmpty(r.StartError, r.ExecutionError, r.ExitError); errText != "" {
		fmt.Fprintf(&b, "Error:     %s\n", errText)
	}
	if !r.Executed && r.Policy.Explanation != "" {
		fmt.Fprintf(&b, "Policy:    %s\n", r.Policy.Explanation)
	}
	if r.CleanupError != "" {
		fmt.Fprintf(&b, "Warning:   %s\n", r.CleanupError)
	}
	if r.Output.Truncated {
		if r.Output.ArtifactWorkspaceAccessible {
			fmt.Fprintf(&b, "\nNote: output truncated (%d bytes). Full output saved to:\n  %s\n",
				r.Output.OriginalBytes, r.Output.ArtifactPath)
		} else {
			fmt.Fprintf(&b, "\nNote: output truncated (%d bytes). Full output saved in agent command artifact state, not the workspace:\n  %s\n",
				r.Output.OriginalBytes, r.Output.ArtifactPath)
		}
	}
	b.WriteString("\n--- stdout ---\n")
	b.WriteString(blankIfEmpty(r.Stdout))
	b.WriteString("\n--- stderr ---\n")
	b.WriteString(blankIfEmpty(r.Stderr))
	return b.String()
}

func buildCommandPayload(r runtimeexec.CommandResult) map[string]any {
	success := r.Executed && r.Status == runtimeexec.CommandStatusSucceeded
	p := map[string]any{
		"success":            success,
		"status":             string(r.Status),
		"executed":           r.Executed,
		"command":            r.Command,
		"exit_code":          r.ExitCode,
		"stdout":             r.Stdout,
		"stderr":             r.Stderr,
		"combined_output":    r.CombinedOutput,
		"policy_outcome":     string(r.Policy.Outcome),
		"policy_explanation": r.Policy.Explanation,
		"policy": map[string]any{
			"outcome":                       string(r.Policy.Outcome),
			"explanation":                   r.Policy.Explanation,
			"blocked_by_shell_write_bypass": r.Policy.BlockedByShellWriteBypass,
		},
		"output": map[string]any{
			"truncated":                     r.Output.Truncated,
			"artifact_path":                 r.Output.ArtifactPath,
			"artifact_workspace_accessible": r.Output.ArtifactWorkspaceAccessible,
			"original_bytes":                r.Output.OriginalBytes,
			"preview_bytes":                 r.Output.PreviewBytes,
			"inline_limit":                  r.Output.InlineLimit,
		},
	}
	if r.WorkingDir != "" {
		p["working_dir"] = r.WorkingDir
	}
	if r.ToolWorkingDir != "" {
		p["tool_working_dir"] = r.ToolWorkingDir
	}
	if r.HostWorkingDir != "" {
		p["host_working_dir"] = r.HostWorkingDir
	}
	if r.SandboxWorkingDir != "" {
		p["sandbox_working_dir"] = r.SandboxWorkingDir
	}
	if r.StartError != "" {
		p["start_error"] = r.StartError
	}
	if r.ExecutionError != "" {
		p["execution_error"] = r.ExecutionError
	}
	if r.ExitError != "" {
		p["exit_error"] = r.ExitError
	}
	if r.CleanupError != "" {
		p["cleanup_error"] = r.CleanupError
	}
	if !success {
		p["error"] = commandErrorString(r)
	}
	return p
}

func commandErrorString(r runtimeexec.CommandResult) string {
	return firstNonEmpty(
		r.StartError,
		r.ExecutionError,
		r.ExitError,
		r.Policy.Explanation,
		string(r.Status),
	)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func blankIfEmpty(s string) string {
	if s == "" {
		return "(empty)"
	}
	return s
}
