package commandtools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestExecuteCommandRecordsSuccessfulEvidence(t *testing.T) {
	host, conv := commandToolHostForTest(t, fakeCommandExecutor{
		result: runtimeexec.CommandResult{
			Status:   runtimeexec.CommandStatusSucceeded,
			Executed: true,
			Command:  "go test ./...",
			ExitCode: 0,
			Policy: runtimepolicy.ShellResult{
				Outcome:     runtimepolicy.ShellOutcomeAllowed,
				Allowed:     true,
				Explanation: "shell command is allowed",
			},
		},
	})

	result, err := new(ExecuteCommandTool).Execute(context.Background(), host, json.RawMessage(`{"command":"go test ./..."}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %+v, want success", result)
	}

	evidence, err := conv.ReadCommandEvidence()
	if err != nil {
		t.Fatalf("ReadCommandEvidence: %v", err)
	}
	if len(evidence.Records) != 1 {
		t.Fatalf("records = %+v, want one command evidence record", evidence.Records)
	}
	record := evidence.Records[0]
	if record.Command != "go test ./..." || record.Status != "succeeded" || !record.Executed || !record.Succeeded {
		t.Fatalf("record = %+v, want successful command facts", record)
	}
	if record.PolicyOutcome != string(runtimepolicy.ShellOutcomeAllowed) {
		t.Fatalf("record policy = %+v, want policy metadata", record)
	}
}

func TestExecuteCommandRejectsVerificationIntent(t *testing.T) {
	host, conv := commandToolHostForTest(t, fakeCommandExecutor{
		result: runtimeexec.CommandResult{
			Status:   runtimeexec.CommandStatusSucceeded,
			Executed: true,
			Command:  "ls",
			ExitCode: 0,
			Policy: runtimepolicy.ShellResult{
				Outcome: runtimepolicy.ShellOutcomeAllowed,
				Allowed: true,
			},
		},
	})

	args := json.RawMessage(`{"command":"ls","` + "verification_" + `intent":"test"}`)
	result, err := new(ExecuteCommandTool).Execute(context.Background(), host, args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || result.Status != "invalid_arguments" || !strings.Contains(result.Error, "unknown field") {
		t.Fatalf("result = %+v, want strict unknown-field rejection", result)
	}
	evidence, err := conv.ReadCommandEvidence()
	if err != nil {
		t.Fatalf("ReadCommandEvidence: %v", err)
	}
	if len(evidence.Records) != 0 {
		t.Fatalf("records = %+v, want no record for malformed tool call", evidence.Records)
	}
}

func TestExecuteCommandRecordsFailedBlockedAndFailedToStartResults(t *testing.T) {
	tests := []struct {
		name       string
		result     runtimeexec.CommandResult
		wantStatus string
	}{
		{
			name: "failed",
			result: runtimeexec.CommandResult{
				Status:   runtimeexec.CommandStatusExitedNonZero,
				Executed: true,
				Command:  "go test ./...",
				ExitCode: 1,
				Policy:   runtimepolicy.ShellResult{Outcome: runtimepolicy.ShellOutcomeAllowed, Allowed: true},
			},
			wantStatus: "exited_non_zero",
		},
		{
			name: "blocked",
			result: runtimeexec.CommandResult{
				Status:   runtimeexec.CommandStatusBlocked,
				Executed: false,
				Command:  "echo hello > out.txt",
				ExitCode: -1,
				Policy: runtimepolicy.ShellResult{
					Outcome:                   runtimepolicy.ShellOutcomeBlockedByShellWriteBypass,
					BlockedByShellWriteBypass: true,
				},
			},
			wantStatus: "blocked",
		},
		{
			name: "failed to start",
			result: runtimeexec.CommandResult{
				Status:     runtimeexec.CommandStatusFailedToStart,
				Executed:   false,
				Command:    "missing-command",
				ExitCode:   -1,
				StartError: "exec: not found",
				Policy:     runtimepolicy.ShellResult{Outcome: runtimepolicy.ShellOutcomeAllowed, Allowed: true},
			},
			wantStatus: "failed_to_start",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, conv := commandToolHostForTest(t, fakeCommandExecutor{result: tt.result})
			_, err := new(ExecuteCommandTool).Execute(context.Background(), host, json.RawMessage(`{"command":"`+tt.result.Command+`"}`))
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			evidence, err := conv.ReadCommandEvidence()
			if err != nil {
				t.Fatalf("ReadCommandEvidence: %v", err)
			}
			if len(evidence.Records) != 1 || evidence.Records[0].Status != tt.wantStatus {
				t.Fatalf("records = %+v, want status %s", evidence.Records, tt.wantStatus)
			}
		})
	}
}

func TestExecuteCommandEvidenceDoesNotPersistOutput(t *testing.T) {
	large := strings.Repeat("output\n", 20000)
	host, conv := commandToolHostForTest(t, fakeCommandExecutor{
		result: runtimeexec.CommandResult{
			Status:         runtimeexec.CommandStatusSucceeded,
			Executed:       true,
			Command:        "go test ./...",
			ExitCode:       0,
			Stdout:         large,
			Stderr:         large,
			CombinedOutput: large + large,
			Output: runtimeexec.OutputSummary{
				Truncated:     true,
				OriginalBytes: len(large) * 2,
				PreviewBytes:  65536,
			},
			Policy: runtimepolicy.ShellResult{Outcome: runtimepolicy.ShellOutcomeAllowed, Allowed: true},
		},
	})

	_, err := new(ExecuteCommandTool).Execute(context.Background(), host, json.RawMessage(`{"command":"go test ./..."}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	evidence, err := conv.ReadCommandEvidence()
	if err != nil {
		t.Fatalf("ReadCommandEvidence: %v", err)
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	if strings.Contains(string(data), "output\noutput") {
		t.Fatalf("evidence persisted command output; size=%d", len(data))
	}
	record := evidence.Records[0]
	if !record.OutputTruncated || record.OutputOriginalBytes == 0 || record.OutputPreviewBytes == 0 {
		t.Fatalf("record output metadata = %+v, want bounded output metadata", record)
	}
}

type fakeCommandExecutor struct {
	result runtimeexec.CommandResult
}

func (f fakeCommandExecutor) Execute(_ context.Context, state *runtimeexec.ExecutionState, request runtimeexec.CommandRequest) (runtimeexec.CommandResult, error) {
	result := f.result
	if result.Command == "" {
		result.Command = request.Command
	}
	if result.WorkingDir == "" {
		result.WorkingDir = state.WorkspaceRoot()
	}
	if result.ToolWorkingDir == "" {
		result.ToolWorkingDir = state.ToolPathForHostPath(result.WorkingDir)
	}
	return result, nil
}

func commandToolHostForTest(t *testing.T, executor runtimeexec.CommandExecutor) (*api.Host, *agent.ConversationState) {
	t.Helper()
	workspaceRoot := t.TempDir()
	layout, err := state.ResolveLayout(workspaceRoot, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	execState, err := runtimeexec.NewExecutionStateForLayout(layout)
	if err != nil {
		t.Fatalf("NewExecutionStateForLayout: %v", err)
	}
	conv := agent.NewConversationState(
		store.NewPlanStore(layout),
		store.NewTodoStore(layout),
		store.NewMemoryStore(layout),
		store.NewCommandEvidenceStore(layout),
	)
	return api.NewHost(nil, executor, execState, nil, nil, nil, conv, nil, nil), conv
}
