package falken

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/persist"
	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/internal/workspace"
)

var (
	errToolHostCapabilityDenied  = errors.New("tool host capability is not declared")
	errToolStateNamespaceMissing = errors.New("tool state namespace is unavailable")
	errToolStateKeyRequired      = errors.New("tool state key is required")
)

// CheckFileAccess evaluates a file request against the tool's declared capabilities and session policy.
func (h sessionToolHost) CheckFileAccess(ctx context.Context, request ToolFileAccessRequest) (ToolFileAccessResult, error) {
	if h.capabilityScoped {
		switch request.Mode {
		case FileAccessRead:
			if !h.safety.ReadsWorkspace && !h.safety.MutatesWorkspace {
				return ToolFileAccessResult{Allowed: false, Explanation: "tool does not declare workspace file capability"}, nil
			}
		case FileAccessWrite, FileAccessCreate:
			if !h.safety.MutatesWorkspace {
				return ToolFileAccessResult{Allowed: false, Explanation: "tool does not declare workspace mutation capability"}, nil
			}
		default:
			if !h.safety.ReadsWorkspace && !h.safety.MutatesWorkspace {
				return ToolFileAccessResult{Allowed: false, Explanation: "tool does not declare workspace file capability"}, nil
			}
		}
	}
	if h.runtime == nil || h.runtime.executionPolicy == nil {
		return ToolFileAccessResult{}, errSessionRuntimeUnavailable
	}
	resolved, err := h.resolveToolFilePath(request)
	if err != nil {
		return ToolFileAccessResult{Allowed: false, Explanation: err.Error()}, nil
	}
	result, err := h.runtime.executionPolicy.EvaluateFile(ctx, runtimepolicy.FileRequest{
		Path:             resolved,
		Mode:             policy.FileAccessMode(request.Mode),
		ApprovalRequired: request.ApprovalRequired,
	})
	if err != nil {
		return ToolFileAccessResult{}, err
	}
	return ToolFileAccessResult{
		Allowed:     result.Allowed,
		Explanation: result.Explanation,
	}, nil
}

// ExecuteCommand evaluates a shell command request and executes it if permitted.
func (h sessionToolHost) ExecuteCommand(ctx context.Context, request ToolCommandRequest) (ToolCommandResult, error) {
	if h.capabilityScoped && !h.safety.ExecutesShell {
		return ToolCommandResult{
			Success:           false,
			Status:            "blocked",
			Executed:          false,
			Command:           request.Command,
			WorkingDir:        request.WorkingDir,
			ExitCode:          -1,
			PolicyExplanation: "tool does not declare shell capability",
		}, nil
	}
	if h.runtime == nil || h.runtime.commandExecutor == nil || h.runtime.executionState == nil {
		return ToolCommandResult{}, errSessionRuntimeUnavailable
	}
	result, err := h.runtime.commandExecutor.Execute(ctx, h.runtime.executionState, runtimeexec.CommandRequest{
		Command:          request.Command,
		WorkingDir:       request.WorkingDir,
		Env:              cloneStringMap(request.Env),
		ApprovalRequired: request.ApprovalRequired,
		MaxInlineOutput:  request.MaxInlineOutput,
	})
	if err != nil {
		return ToolCommandResult{}, err
	}
	if h.commandEvidence != nil && agent.ShouldRecordCommandEvidence(result) {
		manager := agent.NewCommandEvidenceManager(h.commandEvidence)
		record := agent.CommandEvidenceRecordFromCommandResult(result, time.Now().UTC().Format(time.RFC3339Nano))
		if err := manager.Append(record); err != nil {
			return ToolCommandResult{}, err
		}
	}
	return toolCommandResultFromInternal(result), nil
}

// GetState retrieves a tool-scoped, persistent state value.
func (h sessionToolHost) GetState(_ context.Context, request ToolStateGetRequest) (ToolStateGetResult, error) {
	if h.capabilityScoped && !h.safety.ReadsHostState && !h.safety.UsesHostState {
		return ToolStateGetResult{}, errToolHostCapabilityDenied
	}
	path, err := h.toolStatePath(request.Key)
	if err != nil {
		return ToolStateGetResult{}, err
	}
	value, found, err := persist.ReadText(path)
	if err != nil {
		return ToolStateGetResult{}, err
	}
	return ToolStateGetResult{Value: value, Found: found}, nil
}

// SetState stores a tool-scoped, persistent state value.
func (h sessionToolHost) SetState(_ context.Context, request ToolStateSetRequest) (ToolStateSetResult, error) {
	if h.capabilityScoped && !h.safety.MutatesHostState && !h.safety.UsesHostState {
		return ToolStateSetResult{}, errToolHostCapabilityDenied
	}
	path, err := h.toolStatePath(request.Key)
	if err != nil {
		return ToolStateSetResult{}, err
	}
	if err := persist.WriteTextAtomic(path, request.Value, 0o600); err != nil {
		return ToolStateSetResult{}, err
	}
	return ToolStateSetResult{Path: path}, nil
}

// resolveToolFilePath converts a requested file path to an absolute path within the workspace.
func (h sessionToolHost) resolveToolFilePath(request ToolFileAccessRequest) (string, error) {
	if h.runtime != nil && h.runtime.executionState != nil {
		return h.runtime.executionState.ResolveWorkspacePath(request.Path, request.Mode == FileAccessCreate)
	}
	cwd := h.CurrentWorkingDir()
	sandboxMountPath := ""
	if h.runtime != nil && h.runtime.executionState != nil {
		sandboxMountPath = h.runtime.executionState.SandboxMountPath()
	}
	switch request.Mode {
	case FileAccessCreate:
		return workspace.ResolveForCreateWithSandboxMount(h.layout.WorkspaceRoot, cwd, request.Path, sandboxMountPath)
	default:
		return workspace.ResolveExistingWithSandboxMount(h.layout.WorkspaceRoot, cwd, request.Path, sandboxMountPath)
	}
}

// toolStatePath computes the persistent file path for a given tool state key.
func (h sessionToolHost) toolStatePath(key string) (string, error) {
	if h.namespace == "" {
		return "", errToolStateNamespaceMissing
	}
	if key == "" {
		return "", errToolStateKeyRequired
	}
	if h.layout.PluginStateRoot == "" {
		return "", fmt.Errorf("%w: plugin state root is unavailable", errSessionRuntimeUnavailable)
	}
	path := filepath.Join(h.layout.PluginStateRoot, safeToolStateName(h.namespace), safeToolStateName(key)+".state")
	cleaned := filepath.Clean(path)
	root := filepath.Clean(h.layout.PluginStateRoot)
	if rel, err := filepath.Rel(root, cleaned); err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tool state path escapes plugin state root")
	}
	return cleaned, nil
}

// safeToolStateName hashes a string to produce a filesystem-safe directory or file name.
func safeToolStateName(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// toolCommandResultFromInternal maps an internal command result to the public ToolCommandResult.
func toolCommandResultFromInternal(result runtimeexec.CommandResult) ToolCommandResult {
	return ToolCommandResult{
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
		Output: ToolCommandOutputSummary{
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

// cloneStringMap returns a shallow copy of a string map.
func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
