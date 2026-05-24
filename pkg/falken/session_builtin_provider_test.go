package falken

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
)

type builtinTool struct {
	descriptor builtintools.Descriptor
}

func builtinTools() []builtinTool {
	all := builtintools.All()
	result := make([]builtinTool, len(all))
	for i, tool := range all {
		result[i] = builtinTool{descriptor: tool.Descriptor()}
	}
	return result
}

func builtinToolByName(name string) (builtinTool, bool) {
	tool := builtintools.ByName(name)
	if tool == nil {
		return builtinTool{}, false
	}
	return builtinTool{descriptor: tool.Descriptor()}, true
}

func builtinToolEntry(d builtintools.Descriptor) tools.Entry {
	return entryFromBuiltin(testBuiltinDescriptorTool{descriptor: d})
}

type testBuiltinDescriptorTool struct {
	descriptor builtintools.Descriptor
}

func (t testBuiltinDescriptorTool) Descriptor() builtintools.Descriptor {
	return t.descriptor
}

func (t testBuiltinDescriptorTool) Execute(context.Context, *builtintools.Host, json.RawMessage) (agent.ToolExecutionResult, error) {
	return agent.ToolExecutionResult{}, nil
}

func validateBuiltinTools(entries []builtinTool) error {
	converted := make([]tools.Entry, 0, len(entries))
	for _, entry := range entries {
		converted = append(converted, builtinToolEntry(entry.descriptor))
	}
	return validateBuiltinToolEntries(converted)
}

type builtinCommandPayload struct {
	Success           bool                 `json:"success"`
	Status            string               `json:"status"`
	Executed          bool                 `json:"executed"`
	Command           string               `json:"command"`
	WorkingDir        string               `json:"working_dir,omitempty"`
	Stdout            string               `json:"stdout,omitempty"`
	Stderr            string               `json:"stderr,omitempty"`
	CombinedOutput    string               `json:"combined_output,omitempty"`
	ExitCode          int                  `json:"exit_code"`
	PolicyOutcome     string               `json:"policy_outcome,omitempty"`
	PolicyExplanation string               `json:"policy_explanation,omitempty"`
	Output            builtinOutputSummary `json:"output"`
	StartError        string               `json:"start_error,omitempty"`
	ExecutionError    string               `json:"execution_error,omitempty"`
	ExitError         string               `json:"exit_error,omitempty"`
	CleanupError      string               `json:"cleanup_error,omitempty"`
}

type builtinOutputSummary struct {
	Truncated     bool   `json:"truncated"`
	ArtifactPath  string `json:"artifact_path,omitempty"`
	InlineLimit   int    `json:"inline_limit,omitempty"`
	OriginalBytes int    `json:"original_bytes,omitempty"`
	PreviewBytes  int    `json:"preview_bytes,omitempty"`
}

func builtinCommandPayloadFromResult(result runtimeexec.CommandResult) builtinCommandPayload {
	return builtinCommandPayload{
		Success:           result.Executed && result.Status == runtimeexec.CommandStatusSucceeded,
		Status:            string(result.Status),
		Executed:          result.Executed,
		Command:           result.Command,
		WorkingDir:        result.WorkingDir,
		Stdout:            result.Stdout,
		Stderr:            result.Stderr,
		CombinedOutput:    result.CombinedOutput,
		ExitCode:          result.ExitCode,
		PolicyOutcome:     string(result.Policy.Outcome),
		PolicyExplanation: result.Policy.Explanation,
		Output: builtinOutputSummary{
			Truncated:     result.Output.Truncated,
			ArtifactPath:  result.Output.ArtifactPath,
			InlineLimit:   result.Output.InlineLimit,
			OriginalBytes: result.Output.OriginalBytes,
			PreviewBytes:  result.Output.PreviewBytes,
		},
		StartError:     result.StartError,
		ExecutionError: result.ExecutionError,
		ExitError:      result.ExitError,
		CleanupError:   result.CleanupError,
	}
}

func builtinCommandContentFromResult(result runtimeexec.CommandResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Command: %s\n", result.Command)
	fmt.Fprintf(&builder, "Status: %s\n", result.Status)
	fmt.Fprintf(&builder, "Exit code: %d\n", result.ExitCode)
	if errorText := commandResultError(result); errorText != "" {
		fmt.Fprintf(&builder, "Error: %s\n", errorText)
	}
	if result.CleanupError != "" {
		fmt.Fprintf(&builder, "Warning: %s\n", result.CleanupError)
	}
	if result.Output.Truncated {
		fmt.Fprintln(&builder, "Output: truncated")
		if result.Output.ArtifactPath != "" {
			fmt.Fprintf(&builder, "Artifact: %s\n", result.Output.ArtifactPath)
		}
		if result.Output.OriginalBytes != 0 || result.Output.PreviewBytes != 0 {
			fmt.Fprintf(&builder, "Output bytes: original=%d preview=%d limit=%d\n",
				result.Output.OriginalBytes, result.Output.PreviewBytes, result.Output.InlineLimit)
		}
	}
	builder.WriteString("\nStdout:\n")
	builder.WriteString(emptyMarker(result.Stdout))
	builder.WriteString("\n\nStderr:\n")
	builder.WriteString(emptyMarker(result.Stderr))
	return builder.String()
}

func commandResultError(result runtimeexec.CommandResult) string {
	switch {
	case result.StartError != "":
		return result.StartError
	case result.ExecutionError != "":
		return result.ExecutionError
	case result.ExitError != "":
		return result.ExitError
	case result.Status == runtimeexec.CommandStatusBlocked && result.Policy.Explanation != "":
		return result.Policy.Explanation
	default:
		return ""
	}
}

func emptyMarker(value string) string {
	if value == "" {
		return "(empty)"
	}
	return value
}
