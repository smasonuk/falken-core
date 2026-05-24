package falken

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestSessionDefaultToolsIncludeCoreBuiltins(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	session := newTestSessionWithConfig(t, SessionConfig{LLM: llm})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "list tools"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := sessionAgentToolNames(llm.requests[0].Tools)
	for _, want := range []string{"read_file", "read_files", "glob", "grep", "write_file", "edit_file", "multi_edit", "apply_patch", "delete_file", "execute_command", "read_plan", "read_todos", "write_todos", "read_command_evidence", "submit_plan_implementation", "read_memory", "update_memory"} {
		if !sessionAgentHasTool(got, want) {
			t.Fatalf("default tools = %v, missing %q", got, want)
		}
	}
	for _, blocked := range []string{"write_plan"} {
		if sessionAgentHasTool(got, blocked) {
			t.Fatalf("default tools = %v, should hide %q", got, blocked)
		}
	}
}

func TestSessionBuiltInReadFileTool(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	writeFileInTest(t, filepath.Join(workspace, "notes.txt"), "alpha\nbeta\n")
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"notes.txt","start_line":2}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "read done", FinishReason: FinishReasonStop},
	}}
	session := newSessionWithWorkspaceAndConfig(t, workspace, SessionConfig{
		LLM:             llm,
		ApprovalHandler: projectApprovalHandler{fileScope: ApprovalScopeOnce},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if result, err := session.Run(context.Background(), RunRequest{Prompt: "read"}); err != nil || result.FinalOutput != "read done" {
		t.Fatalf("Run = %+v/%v", result, err)
	}
	tool := lastToolResultMessage(t, session)
	if tool.Name != "read_file" || tool.Error != "" || !strings.Contains(tool.Content, "beta") {
		t.Fatalf("tool result = %+v, want read_file content", tool)
	}
	if strings.Contains(string(tool.Payload), `"managed"`) || strings.Contains(string(tool.Payload), `"Managed"`) {
		t.Fatalf("tool payload exposes managed internals: %s", string(tool.Payload))
	}
	for _, forbidden := range []string{`"ID"`, `"ScopeID"`, `"ContentHash"`, `"token"`} {
		if strings.Contains(string(tool.Payload), forbidden) {
			t.Fatalf("tool payload exposes internal token key %s: %s", forbidden, string(tool.Payload))
		}
	}
}

func TestSessionBuiltInReadFilesStatusReflectsFailures(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	writeFileInTest(t, filepath.Join(workspace, "exists.txt"), "alpha\n")
	session := newSessionWithWorkspaceAndConfig(t, workspace, SessionConfig{LLM: noopLLM{}})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	executor := sessionToolExecutor{runtime: session.resources.runtime}
	tests := []struct {
		name        string
		arguments   json.RawMessage
		wantSuccess bool
		wantStatus  string
		wantFailed  int
	}{
		{name: "ok", arguments: json.RawMessage(`{"files":[{"path":"exists.txt"}]}`), wantSuccess: true, wantStatus: "ok"},
		{name: "partial", arguments: json.RawMessage(`{"files":[{"path":"exists.txt"},{"path":"missing.txt"}]}`), wantStatus: "partial", wantFailed: 1},
		{name: "failed", arguments: json.RawMessage(`{"files":[{"path":"missing.txt"}]}`), wantStatus: "failed", wantFailed: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
				Name:      "read_files",
				Arguments: tt.arguments,
			})
			if err != nil {
				t.Fatalf("executeBuiltinTool read_files: %v", err)
			}
			var payload struct {
				Success bool   `json:"success"`
				Status  string `json:"status"`
				Failed  int    `json:"failed"`
			}
			if err := json.Unmarshal(result.Payload, &payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if result.Success != tt.wantSuccess || result.Status != tt.wantStatus || payload.Status != tt.wantStatus || payload.Failed != tt.wantFailed {
				t.Fatalf("result = %+v payload=%+v, want success=%v status=%q failed=%d", result, payload, tt.wantSuccess, tt.wantStatus, tt.wantFailed)
			}
		})
	}
}

func TestSessionBuiltInSearchTools(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	writeFileInTest(t, filepath.Join(workspace, "internal", "tool.go"), "package internal\n\nfunc NewTool() {}\n")
	session := newSessionWithWorkspaceAndConfig(t, workspace, SessionConfig{LLM: noopLLM{}})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	executor := sessionToolExecutor{runtime: session.resources.runtime}

	globResult, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "glob",
		Arguments: json.RawMessage(`{"pattern":"**/*.go","path":"internal"}`),
	})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if !globResult.Success || !strings.Contains(globResult.Content, "internal/tool.go") {
		t.Fatalf("glob result = %+v, want tool.go", globResult)
	}

	grepResult, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "grep",
		Arguments: json.RawMessage(`{"regex":"func New.*Tool","target_paths":["internal"],"glob":"**/*.go","context":1}`),
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !grepResult.Success || !strings.Contains(grepResult.Content, "internal/tool.go:3: func NewTool() {}") {
		t.Fatalf("grep result = %+v, want formatted match", grepResult)
	}

	countResult, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "grep",
		Arguments: json.RawMessage(`{"regex":"func","target_paths":["internal"],"output_mode":"count","limit":1}`),
	})
	if err != nil {
		t.Fatalf("grep count: %v", err)
	}
	if !countResult.Success || !strings.Contains(countResult.Content, "Total matching files: 1") || !strings.Contains(countResult.Content, "internal/tool.go: 1") {
		t.Fatalf("grep count result = %+v, want count summary", countResult)
	}

	invalid, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "grep",
		Arguments: json.RawMessage(`{"regex":"["}`),
	})
	if err != nil {
		t.Fatalf("invalid grep: %v", err)
	}
	if invalid.Success || invalid.Status != "invalid_regex" {
		t.Fatalf("invalid grep result = %+v, want invalid_regex", invalid)
	}
}

func TestSessionBuiltInWriteFileRequiresManagedSafety(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	path := filepath.Join(workspace, "existing.txt")
	writeFileInTest(t, path, "original")
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"existing.txt","content":"changed","operation":"overwrite"}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "write attempted", FinishReason: FinishReasonStop},
	}}
	session := newSessionWithWorkspaceAndConfig(t, workspace, SessionConfig{
		LLM:             llm,
		ApprovalHandler: projectApprovalHandler{fileScope: ApprovalScopeOnce},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "write"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readFileInTest(t, path); got != "original" {
		t.Fatalf("file content = %q, want original after missing read-token rejection", got)
	}
	tool := lastToolResultMessage(t, session)
	if tool.Name != "write_file" || !strings.Contains(tool.Error, "read token") {
		t.Fatalf("tool result = %+v, want read-token rejection", tool)
	}
}

func TestSessionBuiltInEditFileUsesReadTokens(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	path := filepath.Join(workspace, "edit.txt")
	writeFileInTest(t, path, "hello world\n")
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"edit.txt"}`)}}, FinishReason: FinishReasonToolCalls},
		{ToolCalls: []ToolCall{{ID: "call-2", Name: "edit_file", Arguments: json.RawMessage(`{"path":"edit.txt","old":"world","new":"falken"}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "edit done", FinishReason: FinishReasonStop},
	}}
	session := newSessionWithWorkspaceAndConfig(t, workspace, SessionConfig{
		LLM:             llm,
		ApprovalHandler: projectApprovalHandler{fileScope: ApprovalScopeOnce},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "edit"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readFileInTest(t, path); got != "hello falken\n" {
		t.Fatalf("file content = %q, want edited content", got)
	}
}

func TestSessionBuiltInApplyPatchUsesManagedPatchService(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "apply_patch", Arguments: json.RawMessage(`{"patch":"diff --git a/new.txt b/new.txt\nnew file mode 100644\n--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1 @@\n+created\n"}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "patch done", FinishReason: FinishReasonStop},
	}}
	session := newSessionWithWorkspaceAndConfig(t, workspace, SessionConfig{
		LLM:             llm,
		ApprovalHandler: projectApprovalHandler{fileScope: ApprovalScopeOnce},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "patch"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readFileInTest(t, filepath.Join(workspace, "new.txt")); got != "created\n" {
		t.Fatalf("patched file = %q, want created", got)
	}
}

func TestSessionBuiltInExecuteCommandUsesRuntimePolicy(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "execute_command", Arguments: json.RawMessage(`{"command":"printf blocked"}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "command attempted", FinishReason: FinishReasonStop},
	}}
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM:    llm,
		Policy: PolicyConfig{StrictCommandAllowlist: true},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "command"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	tool := lastToolResultMessage(t, session)
	if tool.Name != "execute_command" || tool.Error == "" || !strings.Contains(string(tool.Payload), `"status":"blocked"`) {
		t.Fatalf("tool result = %+v, want policy denial without command output", tool)
	}
	payload := string(tool.Payload)
	if !strings.Contains(payload, `"policy_explanation"`) {
		t.Fatalf("command payload = %s, want policy_explanation", payload)
	}
	for _, forbidden := range []string{`"Policy"`, `"AccessDecision"`, `"ApprovalStatus"`, `"OriginalBytes"`} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("command payload exposes Go-shaped key %s: %s", forbidden, payload)
		}
	}
}

func TestSessionBuiltInExecuteCommandRecordsCommandEvidence(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "execute_command", Arguments: json.RawMessage(`{"command":"true"}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "command complete", FinishReason: FinishReasonStop},
	}}
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM:       llm,
		Execution: ExecutionConfig{Mode: ExecutionModeLocal},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "verify"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	evidence, err := store.NewCommandEvidenceStore(session.layout).Read()
	if err != nil {
		t.Fatalf("Read command evidence: %v", err)
	}
	if len(evidence.Records) != 1 {
		t.Fatalf("records = %+v, want one command evidence record", evidence.Records)
	}
	record := evidence.Records[0]
	if record.Command != "true" || !record.Executed || !record.Succeeded {
		t.Fatalf("record = %+v, want successful command evidence", record)
	}
	if record.RecordedAt == "" {
		t.Fatalf("recorded_at is empty: %+v", record)
	}
}

func TestSessionBuiltInExecuteCommandRecordsFailedCommandEvidence(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "execute_command", Arguments: json.RawMessage(`{"command":"false"}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "command complete", FinishReason: FinishReasonStop},
	}}
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM:       llm,
		Execution: ExecutionConfig{Mode: ExecutionModeLocal},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "verify"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	evidence, err := store.NewCommandEvidenceStore(session.layout).Read()
	if err != nil {
		t.Fatalf("Read command evidence: %v", err)
	}
	if len(evidence.Records) != 1 {
		t.Fatalf("records = %+v, want one command evidence record", evidence.Records)
	}
	record := evidence.Records[0]
	if record.Command != "false" || record.Succeeded || record.ExitCode == 0 {
		t.Fatalf("record = %+v, want failed command evidence", record)
	}
}

func TestSessionBuiltInExecuteCommandEmitsCommandChunkEvents(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "execute_command", Arguments: json.RawMessage(`{"command":"printf hello"}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "command complete", FinishReason: FinishReasonStop},
	}}
	var events []Event
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM: llm,
		Policy: PolicyConfig{
			StrictCommandAllowlist: true,
			AllowedShell: []ShellRule{{
				Command: "printf hello",
				Match:   MatchExact,
			}},
		},
		Events: func(event Event) { events = append(events, event) },
	})
	sandboxRuntime := &countingSandboxRuntime{stdout: "hello"}
	session.resources.runtimeFactory = func(_ context.Context, layout state.Layout, config SessionConfig) (*sessionRuntime, error) {
		return newSandboxTestSessionRuntime(t, layout, config, sandboxRuntime), nil
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "command"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sessionAgentEventTypes(events); !reflect.DeepEqual(got, []EventType{
		EventPlanRouting,
		EventToolCall,
		EventCommandChunk,
		EventToolResult,
		EventAssistantText,
		EventRunCompleted,
	}) {
		t.Fatalf("events = %v, want command chunk between tool call and result", got)
	}
	if events[2].CommandChunk == nil || string(events[2].CommandChunk.Data) != "hello" {
		t.Fatalf("command chunk event = %+v, want stdout hello", events[2])
	}
}

func TestBuiltInCommandContentFromResultSuccessWithStdout(t *testing.T) {
	content := builtinCommandContentFromResult(runtimeexec.CommandResult{
		Status:   runtimeexec.CommandStatusSucceeded,
		Executed: true,
		Command:  "echo hello",
		Stdout:   "hello\n",
		ExitCode: 0,
	})

	assertCommandContentContains(t, content,
		"Command: echo hello",
		"Status: succeeded",
		"Exit code: 0",
		"Stdout:\nhello",
		"Stderr:\n(empty)",
	)
}

func TestBuiltInCommandContentFromResultSuccessWithPolicyExplanationDoesNotShowError(t *testing.T) {
	content := builtinCommandContentFromResult(runtimeexec.CommandResult{
		Status:   runtimeexec.CommandStatusSucceeded,
		Executed: true,
		Command:  "echo hello",
		Stdout:   "hello\n",
		ExitCode: 0,
		Policy: runtimepolicy.ShellResult{
			Explanation: "shell command is allowed and classified as observational",
		},
	})

	if strings.Contains(content, "Error:") {
		t.Fatalf("content = %q, did not expect successful command to include Error", content)
	}
	assertCommandContentContains(t, content,
		"Status: succeeded",
		"Stdout:\nhello",
	)
}

func TestBuiltInCommandContentFromResultSuccessWithNoOutput(t *testing.T) {
	content := builtinCommandContentFromResult(runtimeexec.CommandResult{
		Status:   runtimeexec.CommandStatusSucceeded,
		Executed: true,
		Command:  "true",
		ExitCode: 0,
	})

	assertCommandContentContains(t, content,
		"Command: true",
		"Status: succeeded",
		"Exit code: 0",
		"Stdout:\n(empty)",
		"Stderr:\n(empty)",
	)
}

func TestBuiltInCommandContentFromResultNonZeroWithStderr(t *testing.T) {
	content := builtinCommandContentFromResult(runtimeexec.CommandResult{
		Status:    runtimeexec.CommandStatusExitedNonZero,
		Executed:  true,
		Command:   "sh -c 'echo nope >&2; exit 2'",
		Stderr:    "nope\n",
		ExitCode:  2,
		ExitError: "exit status 2",
	})

	assertCommandContentContains(t, content,
		"Status: exited_non_zero",
		"Exit code: 2",
		"Error: exit status 2",
		"Stdout:\n(empty)",
		"Stderr:\nnope",
	)
}

func TestBuiltInCommandContentFromResultBlockedWithPolicyExplanation(t *testing.T) {
	content := builtinCommandContentFromResult(runtimeexec.CommandResult{
		Status:   runtimeexec.CommandStatusBlocked,
		Command:  "printf blocked",
		ExitCode: -1,
		Policy: runtimepolicy.ShellResult{
			Explanation: "command is denied by strict allowlist",
		},
	})

	assertCommandContentContains(t, content,
		"Status: blocked",
		"Exit code: -1",
		"Error: command is denied by strict allowlist",
		"Stdout:\n(empty)",
		"Stderr:\n(empty)",
	)
}

func TestBuiltInCommandContentFromResultFailedToStart(t *testing.T) {
	content := builtinCommandContentFromResult(runtimeexec.CommandResult{
		Status:     runtimeexec.CommandStatusFailedToStart,
		Command:    "echo hello",
		ExitCode:   -1,
		StartError: "fork/exec /missing-shell: no such file or directory",
	})

	assertCommandContentContains(t, content,
		"Status: failed_to_start",
		"Exit code: -1",
		"Error: fork/exec /missing-shell: no such file or directory",
	)
}

func TestBuiltInCommandContentFromResultTruncatedOutputIncludesArtifact(t *testing.T) {
	content := builtinCommandContentFromResult(runtimeexec.CommandResult{
		Status:         runtimeexec.CommandStatusSucceeded,
		Executed:       true,
		Command:        "yes",
		Stdout:         "hello",
		CombinedOutput: "hello",
		ExitCode:       0,
		Output: runtimeexec.OutputSummary{
			Truncated:     true,
			ArtifactPath:  "/tmp/falken-output.txt",
			InlineLimit:   4,
			OriginalBytes: 1024,
			PreviewBytes:  4,
		},
	})

	assertCommandContentContains(t, content,
		"Status: succeeded",
		"Output: truncated",
		"Artifact: /tmp/falken-output.txt",
		"Output bytes: original=1024 preview=4 limit=4",
	)
}

func TestBuiltInCommandContentFromResultCleanupErrorIsWarning(t *testing.T) {
	result := runtimeexec.CommandResult{
		Status:       runtimeexec.CommandStatusSucceeded,
		Executed:     true,
		Command:      "true",
		ExitCode:     0,
		CleanupError: "cleanup container: remove failed",
	}
	content := builtinCommandContentFromResult(result)
	payload := builtinCommandPayloadFromResult(result)

	assertCommandContentContains(t, content,
		"Status: succeeded",
		"Warning: cleanup container: remove failed",
	)
	if strings.Contains(content, "Error: cleanup container") {
		t.Fatalf("content = %q, cleanup diagnostic should be a warning, not command error", content)
	}
	if payload.CleanupError != "cleanup container: remove failed" {
		t.Fatalf("payload cleanup error = %q, want cleanup diagnostic", payload.CleanupError)
	}
}

func TestSessionPlanModeBlocksBuiltInMutationTools(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"plan.txt","content":"nope"}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "planned", FinishReason: FinishReasonStop},
	}}
	session := newSessionWithWorkspaceAndConfig(t, workspace, SessionConfig{
		LLM:             llm,
		ApprovalHandler: projectApprovalHandler{fileScope: ApprovalScopeOnce},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "plan"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "plan.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan.txt stat = %v, want missing", err)
	}
	if exposed := sessionAgentToolNames(llm.requests[0].Tools); sessionAgentHasTool(exposed, "write_file") {
		t.Fatalf("plan-mode exposed tools = %v, want write_file filtered", exposed)
	} else {
		for _, want := range []string{"glob", "grep", "read_memory", "update_memory"} {
			if !sessionAgentHasTool(exposed, want) {
				t.Fatalf("plan-mode exposed tools = %v, missing %q", exposed, want)
			}
		}
	}
	tool := lastToolResultMessage(t, session)
	if tool.Name != "write_file" || !strings.Contains(tool.Error, "plan mode") {
		t.Fatalf("blocked tool result = %+v", tool)
	}
}

func TestSessionCanUseBuiltinsWithoutExternalToolProviders(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"created.txt","content":"created by builtin","operation":"create"}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "created", FinishReason: FinishReasonStop},
	}}
	session := newSessionWithWorkspaceAndConfig(t, workspace, SessionConfig{
		LLM:             llm,
		ApprovalHandler: projectApprovalHandler{fileScope: ApprovalScopeOnce},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.Run(context.Background(), RunRequest{Prompt: "create"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "created" {
		t.Fatalf("result = %+v", result)
	}
	if got := readFileInTest(t, filepath.Join(workspace, "created.txt")); got != "created by builtin" {
		t.Fatalf("created content = %q", got)
	}
}

func TestBuiltInToolDescriptorsHaveValidObjectSchemas(t *testing.T) {
	for _, tool := range builtinTools() {
		var schema map[string]any
		if err := json.Unmarshal(tool.descriptor.Parameters, &schema); err != nil {
			t.Fatalf("%s schema is invalid JSON: %v", tool.descriptor.Name, err)
		}
		if got := schema["type"]; got != "object" {
			t.Fatalf("%s schema type = %v, want object", tool.descriptor.Name, got)
		}
		if _, ok := schema["properties"].(map[string]any); !ok {
			t.Fatalf("%s schema missing object properties: %#v", tool.descriptor.Name, schema["properties"])
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("%s schema additionalProperties = %#v, want false", tool.descriptor.Name, schema["additionalProperties"])
		}
		assertSchemaObjectsClosed(t, tool.descriptor.Name, schema)
	}
}

func TestBuiltInToolStringMapSchemasRemainOpen(t *testing.T) {
	tool, ok := builtinToolByName("execute_command")
	if !ok {
		t.Fatal("missing execute_command built-in")
	}
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(tool.descriptor.Parameters, &schema); err != nil {
		t.Fatalf("execute_command schema: %v", err)
	}
	env := schema.Properties["env"]
	additional, ok := env["additionalProperties"].(map[string]any)
	if !ok {
		t.Fatalf("env additionalProperties = %#v, want open string map", env["additionalProperties"])
	}
	if additional["type"] != "string" {
		t.Fatalf("env additionalProperties = %#v, want string values", additional)
	}
}

func TestBuiltInToolsRejectUnknownArgumentFields(t *testing.T) {
	executor := planToolExecutorForTest(t)
	if err := executor.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	args := map[string]json.RawMessage{
		"read_file":                  json.RawMessage(`{"path":"notes.txt","unexpected":true}`),
		"read_files":                 json.RawMessage(`{"files":[{"path":"notes.txt"}],"unexpected":true}`),
		"glob":                       json.RawMessage(`{"pattern":"*.go","unexpected":true}`),
		"grep":                       json.RawMessage(`{"regex":"package","unexpected":true}`),
		"write_file":                 json.RawMessage(`{"path":"notes.txt","content":"hello","unexpected":true}`),
		"edit_file":                  json.RawMessage(`{"path":"notes.txt","old":"a","new":"b","unexpected":true}`),
		"multi_edit":                 json.RawMessage(`{"edits":[{"path":"notes.txt","old":"a","new":"b"}],"unexpected":true}`),
		"apply_patch":                json.RawMessage(`{"patch":"diff --git a/a b/a\n","unexpected":true}`),
		"delete_file":                json.RawMessage(`{"path":"notes.txt","unexpected":true}`),
		"execute_command":            json.RawMessage(`{"command":"true","unexpected":true}`),
		"read_plan":                  json.RawMessage(`{"unexpected":true}`),
		"read_todos":                 json.RawMessage(`{"unexpected":true}`),
		"write_todos":                json.RawMessage(`{"todos":[],"unexpected":true}`),
		"read_command_evidence":      json.RawMessage(`{"unexpected":true}`),
		"submit_plan_implementation": json.RawMessage(`{"summary":"done","verification_summary":"tested","unexpected":true}`),
		"read_memory":                json.RawMessage(`{"unexpected":true}`),
		"update_memory":              json.RawMessage(`{"current_goal":"test","unexpected":true}`),
	}
	args["write_plan"] = marshalRawMessageForTest(t, map[string]any{
		"plan":       validBuiltInPlanForTest(),
		"todos":      []agent.Todo{{ID: "task-1", Content: "Task", Status: agent.TodoStatusPending}},
		"unexpected": true,
	})

	for _, tool := range builtinTools() {
		t.Run(tool.descriptor.Name, func(t *testing.T) {
			result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
				Name:      tool.descriptor.Name,
				Arguments: args[tool.descriptor.Name],
			})
			if err != nil {
				t.Fatalf("executeBuiltinTool: %v", err)
			}
			if result.Success || result.Status != "invalid_arguments" || !strings.Contains(result.Error, "unknown field") {
				t.Fatalf("result = %+v, want invalid_arguments unknown-field failure", result)
			}
		})
	}
}

func TestBuiltInToolsRejectTrailingArgumentValues(t *testing.T) {
	executor := planToolExecutorForTest(t)
	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "read_plan",
		Arguments: json.RawMessage(`{} {}`),
	})
	if err != nil {
		t.Fatalf("executeBuiltinTool: %v", err)
	}
	if result.Success || result.Status != "invalid_arguments" || !strings.Contains(result.Error, "exactly one JSON value") {
		t.Fatalf("result = %+v, want trailing JSON rejection", result)
	}
}

func TestBuiltInNoArgToolsTreatEmptyArgumentsAsObject(t *testing.T) {
	executor := planToolExecutorForTest(t)
	for _, name := range []string{"read_plan", "read_todos", "read_command_evidence", "read_memory"} {
		t.Run(name, func(t *testing.T) {
			result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{Name: name})
			if err != nil {
				t.Fatalf("executeBuiltinTool: %v", err)
			}
			if !result.Success {
				t.Fatalf("result = %+v, want empty args to decode as {}", result)
			}
		})
	}
}

func TestBuiltInToolsRejectDeprecatedWorkingDirAlias(t *testing.T) {
	executor := planToolExecutorForTest(t)
	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"notes.txt","current_working_dir":"."}`),
	})
	if err != nil {
		t.Fatalf("executeBuiltinTool: %v", err)
	}
	if result.Success || result.Status != "invalid_arguments" || !strings.Contains(result.Error, "current_working_dir") {
		t.Fatalf("result = %+v, want current_working_dir alias rejected", result)
	}
}

func TestBuiltInToolSchemasPreserveRequiredFields(t *testing.T) {
	wants := map[string][]string{
		"read_file":                  {"path"},
		"read_files":                 {"files"},
		"glob":                       {"pattern"},
		"grep":                       {"regex"},
		"write_file":                 {"path", "content"},
		"edit_file":                  {"path", "old", "new"},
		"multi_edit":                 {"edits"},
		"apply_patch":                {"patch"},
		"delete_file":                {"path"},
		"execute_command":            {"command"},
		"read_plan":                  nil,
		"write_plan":                 {"plan", "todos"},
		"read_todos":                 nil,
		"write_todos":                {"todos"},
		"read_command_evidence":      nil,
		"submit_plan_implementation": {"summary", "verification_summary"},
		"read_memory":                nil,
		"update_memory":              nil,
	}

	for _, tool := range builtinTools() {
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(tool.descriptor.Parameters, &schema); err != nil {
			t.Fatalf("%s schema: %v", tool.descriptor.Name, err)
		}
		want, ok := wants[tool.descriptor.Name]
		if !ok {
			t.Fatalf("unexpected built-in tool %q", tool.descriptor.Name)
		}
		if !sameStringSet(schema.Required, want) {
			t.Fatalf("%s required fields = %v, want %v", tool.descriptor.Name, schema.Required, want)
		}
		delete(wants, tool.descriptor.Name)
	}
	if len(wants) != 0 {
		t.Fatalf("missing built-in tool schemas for %v", wants)
	}
}

func TestBuiltInHostStateSafetyIsExplicit(t *testing.T) {
	wants := map[string]struct {
		reads   bool
		mutates bool
	}{
		"read_plan":                  {reads: true},
		"write_plan":                 {reads: true, mutates: true},
		"read_todos":                 {reads: true},
		"write_todos":                {reads: true, mutates: true},
		"read_command_evidence":      {reads: true},
		"submit_plan_implementation": {reads: true, mutates: true},
		"read_memory":                {reads: true},
		"update_memory":              {reads: true, mutates: true},
	}
	for _, tool := range builtinTools() {
		want, ok := wants[tool.descriptor.Name]
		if !ok {
			if tool.descriptor.Safety.ReadsHostState || tool.descriptor.Safety.MutatesHostState {
				t.Fatalf("%s declares unexpected host-state safety: %+v", tool.descriptor.Name, tool.descriptor.Safety)
			}
			continue
		}
		if tool.descriptor.Safety.ReadsHostState != want.reads || tool.descriptor.Safety.MutatesHostState != want.mutates {
			t.Fatalf("%s host-state safety = %+v, want reads=%v mutates=%v", tool.descriptor.Name, tool.descriptor.Safety, want.reads, want.mutates)
		}
		delete(wants, tool.descriptor.Name)
	}
	if len(wants) != 0 {
		t.Fatalf("missing host-state safety checks for %v", wants)
	}
}

func TestBuiltInWriteFileSchemaIncludesMode(t *testing.T) {
	tool, ok := builtinToolByName("write_file")
	if !ok {
		t.Fatal("missing write_file built-in")
	}
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.descriptor.Parameters, &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if schema.Properties["mode"].Type != "string" {
		t.Fatalf("mode schema = %+v, want string property", schema.Properties["mode"])
	}
}

func TestBuiltInToolSchemasDoNotExposeApprovalRequired(t *testing.T) {
	for _, tool := range builtinTools() {
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(tool.descriptor.Parameters, &schema); err != nil {
			t.Fatalf("%s schema: %v", tool.descriptor.Name, err)
		}
		if _, ok := schema.Properties["approval_required"]; ok {
			t.Fatalf("%s exposes model-controlled approval_required", tool.descriptor.Name)
		}
	}
}

func TestBuiltInToolRegistrationRejectsDuplicates(t *testing.T) {
	tools := builtinTools()
	duplicate := tools[0]
	duplicate.descriptor.Name = tools[1].descriptor.Name
	err := validateBuiltinTools([]builtinTool{tools[1], duplicate})
	if err == nil || !strings.Contains(err.Error(), "duplicate built-in tool name") {
		t.Fatalf("validateBuiltinTools error = %v, want duplicate built-in tool name", err)
	}
}

func TestBuiltInToolRegistrationRejectsInvalidDescriptors(t *testing.T) {
	tool := builtinTools()[0]
	tests := []struct {
		name string
		edit func(*builtintools.Descriptor)
		want string
	}{
		{
			name: "invalid name",
			edit: func(d *builtintools.Descriptor) {
				d.Name = "1bad"
			},
			want: "invalid built-in tool",
		},
		{
			name: "missing description",
			edit: func(d *builtintools.Descriptor) {
				d.Description = ""
			},
			want: "description is required",
		},
		{
			name: "non object schema",
			edit: func(d *builtintools.Descriptor) {
				d.Parameters = json.RawMessage(`{"type":"string"}`)
			},
			want: "object schema",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invalid := tool
			tt.edit(&invalid.descriptor)
			err := validateBuiltinTools([]builtinTool{invalid})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateBuiltinTools error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestBuiltInToolDescriptorConvertsToEntry(t *testing.T) {
	tool, ok := builtinToolByName("read_file")
	if !ok {
		t.Fatal("missing read_file built-in")
	}
	entry := builtinToolEntry(tool.descriptor)

	if entry.Name != "read_file" {
		t.Fatalf("entry = %+v, want read_file", entry)
	}
	if entry.PackageName != "falken-core" || !entry.AlwaysLoad || !entry.DefaultLoad {
		t.Fatalf("entry metadata = %+v, want built-in package and load defaults", entry)
	}
	if len(entry.InputSchema) == 0 || !json.Valid(entry.InputSchema) {
		t.Fatalf("entry schema = %q, want valid JSON", string(entry.InputSchema))
	}
	if !entry.Safety.ReadsWorkspace {
		t.Fatalf("entry safety = %+v, want read workspace safety metadata", entry.Safety)
	}
}

func TestBuiltInToolExecutionUsesDescriptorDispatch(t *testing.T) {
	executor := planToolExecutorForTest(t)
	if err := executor.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "write_plan",
		Arguments: json.RawMessage(`{"plan":"# Goal\nTest descriptor dispatch for write_plan.\n\n# Files\nOnly the runtime plan state is updated.\n\n# Changes\nStep 1 records this test plan through the built-in tool.\n\n# Verification\nThe unit test verifies the dispatch result.","todos":[{"id":"verify-dispatch","content":"Verify descriptor-dispatched write_plan stores both plan and todos","status":"pending"}]}`),
	})
	if err != nil {
		t.Fatalf("executeBuiltinTool: %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %+v, want success", result)
	}
	plan, err := executor.mode.Plan().Read()
	if err != nil {
		t.Fatalf("Read plan: %v", err)
	}
	if !strings.Contains(plan, "Test descriptor dispatch for write_plan") {
		t.Fatalf("plan = %q, want descriptor-dispatched write", plan)
	}
}

func TestBuiltInWritePlanRejectsContentAlias(t *testing.T) {
	executor := planToolExecutorForTest(t)
	if err := executor.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "write_plan",
		Arguments: json.RawMessage(`{"content":"# Goal\nTest deprecated content alias rejection for write_plan.\n\n# Files\nOnly the runtime plan state is updated.\n\n# Changes\n1. Attempt to write this plan using the content alias instead of the plan field.\n\n# Verification\nThe unit test verifies the alias is rejected.","todos":[{"id":"verify-alias","content":"Verify the content alias is rejected","status":"pending"}]}`),
	})
	if err != nil {
		t.Fatalf("executeBuiltinTool: %v", err)
	}
	if result.Success || result.Status != "invalid_arguments" || !strings.Contains(result.Error, "unknown field") {
		t.Fatalf("result = %+v, want strict unknown-field rejection", result)
	}
}

func TestBuiltInWritePlanRequiresValidTodosAndDoesNotPartiallyWrite(t *testing.T) {
	tests := []struct {
		name      string
		args      json.RawMessage
		wantError string
	}{
		{
			name:      "missing todos",
			args:      writePlanArgsForTest(t, validBuiltInPlanForTest(), nil),
			wantError: "todos field is required",
		},
		{
			name:      "empty todos",
			args:      writePlanArgsForTest(t, validBuiltInPlanForTest(), []agent.Todo{}),
			wantError: "todos must contain at least one item",
		},
		{
			name:      "invalid todos",
			args:      writePlanArgsForTest(t, validBuiltInPlanForTest(), []agent.Todo{{ID: "task-1", Content: "Task", Status: "blocked"}}),
			wantError: "unknown status",
		},
		{
			name:      "invalid plan",
			args:      writePlanArgsForTest(t, "# Goal\nToo short", []agent.Todo{{ID: "task-1", Content: "Task", Status: agent.TodoStatusPending}}),
			wantError: "too short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := planToolExecutorForTest(t)
			if err := executor.mode.EnterPlan(); err != nil {
				t.Fatalf("EnterPlan: %v", err)
			}
			result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
				Name:      "write_plan",
				Arguments: tt.args,
			})
			if err != nil {
				t.Fatalf("executeBuiltinTool: %v", err)
			}
			if result.Success || result.Status != "invalid_todos" && result.Status != "invalid_plan" || !strings.Contains(result.Error, tt.wantError) {
				t.Fatalf("result = %+v, want failure containing %q", result, tt.wantError)
			}
			plan, err := executor.mode.Plan().Read()
			if err != nil {
				t.Fatalf("Read plan: %v", err)
			}
			if strings.Contains(plan, "Valid built-in plan") {
				t.Fatalf("plan was written on validation failure: %q", plan)
			}
			todos, err := agent.NewTodoManager(executor.todos).Read()
			if err != nil {
				t.Fatalf("Read todos: %v", err)
			}
			if len(todos) != 0 {
				t.Fatalf("todos were written on validation failure: %+v", todos)
			}
		})
	}
}

func TestBuiltInWritePlanWritesPlanAndTodos(t *testing.T) {
	executor := planToolExecutorForTest(t)
	if err := executor.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	wantTodos := []agent.Todo{
		{ID: "inspect-flow", Content: "Inspect current planning, todo, and compaction flow", Status: agent.TodoStatusCompleted},
		{ID: "add-todo-tools", Content: "Add built-in todo update tools", Status: agent.TodoStatusInProgress},
		{ID: "verify", Content: "Run targeted tests for plan and todo behavior", Status: agent.TodoStatusPending},
	}
	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "write_plan",
		Arguments: writePlanArgsForTest(t, validBuiltInPlanForTest(), wantTodos),
	})
	if err != nil {
		t.Fatalf("executeBuiltinTool: %v", err)
	}
	if !result.Success || !strings.Contains(string(result.Payload), `"todos_written":true`) {
		t.Fatalf("result = %+v, want successful atomic write", result)
	}
	plan, err := executor.mode.Plan().Read()
	if err != nil {
		t.Fatalf("Read plan: %v", err)
	}
	if !strings.Contains(plan, "Valid built-in plan") {
		t.Fatalf("plan = %q, want written valid plan", plan)
	}
	gotTodos, err := agent.NewTodoManager(executor.todos).Read()
	if err != nil {
		t.Fatalf("Read todos: %v", err)
	}
	if !reflect.DeepEqual(gotTodos, wantTodos) {
		t.Fatalf("todos = %+v, want %+v", gotTodos, wantTodos)
	}
}

func TestBuiltInWritePlanRejectsInvalidPlan(t *testing.T) {
	executor := planToolExecutorForTest(t)
	if err := executor.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "write_plan",
		Arguments: json.RawMessage(`{"plan":"   "}`),
	})
	if err != nil {
		t.Fatalf("executeBuiltinTool: %v", err)
	}
	if result.Success || result.Status != "invalid_plan" {
		t.Fatalf("result = %+v, want invalid_plan failure", result)
	}
}

func TestBuiltInWritePlanRejectsOutsidePlanMode(t *testing.T) {
	executor := planToolExecutorForTest(t)
	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "write_plan",
		Arguments: json.RawMessage(`{"plan":"# Goal\nTest outside mode block.\n\n# Files\nRuntime plan only.\n\n# Changes\nWrite a plan.\n\n# Verification\nCheck the tool result."}`),
	})
	if err != nil {
		t.Fatalf("executeBuiltinTool: %v", err)
	}
	if result.Success || result.Status != "blocked_by_mode" {
		t.Fatalf("result = %+v, want blocked_by_mode failure", result)
	}
}

func TestBuiltInWritePlanValidationReportsDetectedHeadings(t *testing.T) {
	executor := planToolExecutorForTest(t)
	if err := executor.mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "write_plan",
		Arguments: json.RawMessage(`{"plan":"# Goal\nBuild a production-ready, terminal-based Expense Tracker in Go as a modular multi-package application with SQLite persistence, YAML config management, concurrent CSV import, and table-driven storage tests.\n\n# Files\n- go.mod\n- cmd/expense-tracker/main.go\n\n# Verification\nRun go test ./... and go build ./cmd/expense-tracker."}`),
	})
	if err != nil {
		t.Fatalf("executeBuiltinTool: %v", err)
	}
	if result.Success || result.Status != "invalid_plan" {
		t.Fatalf("result = %+v, want invalid_plan failure", result)
	}
	for _, want := range []string{
		"Plan is missing required heading: Changes",
		"Detected headings:",
		"- Goal",
		"- Files",
		"- Verification",
		"Expected one of:",
		"# Changes",
		"# Implementation Steps",
	} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("error = %q, missing %q", result.Error, want)
		}
	}
}

func TestBuiltInReadWriteTodosWorkInDefaultMode(t *testing.T) {
	executor := planToolExecutorForTest(t)
	wantTodos := []agent.Todo{{ID: "task-1", Content: "Update progress", Status: agent.TodoStatusInProgress}}
	writeResult, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "write_todos",
		Arguments: writeTodosArgsForTest(t, wantTodos),
	})
	if err != nil {
		t.Fatalf("write_todos: %v", err)
	}
	if !writeResult.Success || !strings.Contains(string(writeResult.Payload), `"todos_written":true`) {
		t.Fatalf("write_todos result = %+v, want success", writeResult)
	}

	readResult, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{Name: "read_todos"})
	if err != nil {
		t.Fatalf("read_todos: %v", err)
	}
	if !readResult.Success || !strings.Contains(readResult.Content, "Update progress") {
		t.Fatalf("read_todos result = %+v, want current todos", readResult)
	}

	invalid, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "write_todos",
		Arguments: writeTodosArgsForTest(t, []agent.Todo{{ID: "bad", Content: "Bad", Status: "blocked"}}),
	})
	if err != nil {
		t.Fatalf("invalid write_todos: %v", err)
	}
	if invalid.Success || invalid.Status != "invalid_todos" {
		t.Fatalf("invalid write_todos result = %+v, want invalid_todos", invalid)
	}
}

func TestBuiltInReadUpdateMemoryWorkInDefaultMode(t *testing.T) {
	executor := planToolExecutorForTest(t)
	updateResult, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name: "update_memory",
		Arguments: json.RawMessage(`{
			"current_goal":"Add search and memory built-ins",
			"add_entries":["legacy note"],
			"add_important_files":["internal/builtintools/registry.go"],
			"add_decisions":["Search tools do not issue read tokens."],
			"add_open_questions":["Any docs left?"]
		}`),
	})
	if err != nil {
		t.Fatalf("update_memory: %v", err)
	}
	if !updateResult.Success || !strings.Contains(updateResult.Content, "Current goal:") || !strings.Contains(string(updateResult.Payload), `"changed":true`) {
		t.Fatalf("update_memory result = %+v, want changed memory", updateResult)
	}

	readResult, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{Name: "read_memory"})
	if err != nil {
		t.Fatalf("read_memory: %v", err)
	}
	if !readResult.Success || !strings.Contains(readResult.Content, "Add search and memory built-ins") {
		t.Fatalf("read_memory result = %+v, want current goal", readResult)
	}

	removeResult, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "update_memory",
		Arguments: json.RawMessage(`{"clear_current_goal":true,"remove_entries":["legacy note"],"remove_important_files":["internal/builtintools/registry.go"],"remove_decisions":["Search tools do not issue read tokens."],"remove_open_questions":["Any docs left?"]}`),
	})
	if err != nil {
		t.Fatalf("remove update_memory: %v", err)
	}
	if !removeResult.Success || !strings.Contains(removeResult.Content, "(no memory entries)") {
		t.Fatalf("remove result = %+v, want empty memory", removeResult)
	}

	invalid, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "update_memory",
		Arguments: json.RawMessage(`{"current_goal":"` + strings.Repeat("x", 501) + `"}`),
	})
	if err != nil {
		t.Fatalf("invalid update_memory: %v", err)
	}
	if invalid.Success || invalid.Status != "invalid_memory" {
		t.Fatalf("invalid update_memory result = %+v, want invalid_memory", invalid)
	}
}

func TestBuiltInHostMemoryConstruction(t *testing.T) {
	withMemory := planToolExecutorForTest(t).buildHost(nil)
	if _, err := withMemory.RequireMemory(); err != nil {
		t.Fatalf("RequireMemory with store: %v", err)
	}

	withoutMemory := sessionToolExecutor{}.buildHost(nil)
	if _, err := withoutMemory.RequireMemory(); !errors.Is(err, builtintools.ErrHostUnavailable) {
		t.Fatalf("RequireMemory without store error = %v, want ErrHostUnavailable", err)
	}
}

func TestBuiltInReadPlanWorksInDefaultMode(t *testing.T) {
	executor := planToolExecutorForTest(t)
	if err := executor.mode.Plan().Write(validBuiltInPlanForTest()); err != nil {
		t.Fatalf("Write plan: %v", err)
	}
	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{Name: "read_plan"})
	if err != nil {
		t.Fatalf("read_plan: %v", err)
	}
	if !result.Success || !strings.Contains(result.Content, "Valid built-in plan") {
		t.Fatalf("read_plan result = %+v, want default-mode read", result)
	}
}

func TestBuiltInSubmitPlanImplementationBlocksIncompleteTodos(t *testing.T) {
	executor := planToolExecutorForTest(t)
	if err := executor.mode.Plan().Write(validBuiltInPlanForTest()); err != nil {
		t.Fatalf("Write plan: %v", err)
	}
	if err := agent.NewTodoManager(executor.todos).Replace([]agent.Todo{{
		ID: "t1", Content: "Add regression tests", Status: agent.TodoStatusPending,
	}}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}

	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "submit_plan_implementation",
		Arguments: json.RawMessage(`{"summary":"implemented","verification_summary":"not run"}`),
	})
	if err != nil {
		t.Fatalf("submit_plan_implementation: %v", err)
	}
	if result.Success || result.Status != "blocked_incomplete_todos" {
		t.Fatalf("result = %+v, want blocked incomplete todos", result)
	}
	if !strings.Contains(string(result.Payload), `"accepted":false`) || !strings.Contains(string(result.Payload), "Add regression tests") {
		t.Fatalf("payload = %s, want incomplete todo blocker", result.Payload)
	}
}

func TestBuiltInSubmitPlanImplementationAcceptsCompletedTodos(t *testing.T) {
	executor := planToolExecutorForTest(t)
	if err := executor.mode.Plan().Write(validBuiltInPlanForTest()); err != nil {
		t.Fatalf("Write plan: %v", err)
	}
	if err := agent.NewTodoManager(executor.todos).Replace([]agent.Todo{{
		ID: "t1", Content: "Add regression tests", Status: agent.TodoStatusCompleted,
	}}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}

	result, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "submit_plan_implementation",
		Arguments: json.RawMessage(`{"summary":"implemented","verification_summary":"go test ./... passed","known_issues":[]}`),
	})
	if err != nil {
		t.Fatalf("submit_plan_implementation: %v", err)
	}
	if !result.Success || result.Status != "accepted" || !strings.Contains(string(result.Payload), `"accepted":true`) {
		t.Fatalf("result = %+v, want accepted", result)
	}
	if !strings.Contains(string(result.Payload), `"todos_cleared":true`) || !strings.Contains(string(result.Payload), `"active_plan_cleared":true`) {
		t.Fatalf("payload = %s, want cleared plan/todo fields", result.Payload)
	}
	plan, err := executor.mode.Plan().Read()
	if err != nil {
		t.Fatalf("Read plan after accepted submit: %v", err)
	}
	if strings.TrimSpace(plan) != "" {
		t.Fatalf("plan after accepted submit = %q, want empty", plan)
	}
	todos, err := agent.NewTodoManager(executor.todos).Read()
	if err != nil {
		t.Fatalf("Read todos after accepted submit: %v", err)
	}
	if len(todos) != 0 {
		t.Fatalf("todos after accepted submit = %+v, want empty", todos)
	}
}

func TestBuiltInSubmitPlanImplementationReviewerRetryThenWarning(t *testing.T) {
	executor := planToolExecutorForTest(t)
	executor.reviewer = &scriptedCommandEvidenceReviewer{reviews: []agent.CommandEvidenceReview{
		{Verdict: "insufficient", Confidence: "medium", Reason: "only inspection commands were present"},
		{Verdict: "insufficient", Confidence: "medium", Reason: "still no clear validation command"},
	}}
	if err := executor.mode.Plan().Write(validBuiltInPlanForTest()); err != nil {
		t.Fatalf("Write plan: %v", err)
	}
	if err := agent.NewTodoManager(executor.todos).Replace([]agent.Todo{{
		ID: "t1", Content: "Implement change", Status: agent.TodoStatusCompleted,
	}}); err != nil {
		t.Fatalf("Replace todos: %v", err)
	}

	blocked, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "submit_plan_implementation",
		Arguments: json.RawMessage(`{"summary":"implemented","verification_summary":"not run"}`),
	})
	if err != nil {
		t.Fatalf("submit_plan_implementation blocked: %v", err)
	}
	if blocked.Success || blocked.Status != "blocked_verification_review" {
		t.Fatalf("blocked result = %+v, want blocked_verification_review", blocked)
	}
	if !strings.Contains(string(blocked.Payload), `"review_attempts":1`) {
		t.Fatalf("blocked payload = %s, want first review attempt", blocked.Payload)
	}

	accepted, err := executor.executeBuiltinTool(context.Background(), agent.ToolExecutionRequest{
		Name:      "submit_plan_implementation",
		Arguments: json.RawMessage(`{"summary":"implemented","verification_summary":"still not confirmed"}`),
	})
	if err != nil {
		t.Fatalf("submit_plan_implementation accepted: %v", err)
	}
	if !accepted.Success || accepted.Status != "accepted_with_verification_warning" {
		t.Fatalf("accepted result = %+v, want accepted_with_verification_warning", accepted)
	}
	if !strings.Contains(accepted.Content, "accepted with warning") || !strings.Contains(string(accepted.Payload), "verification_not_confirmed") {
		t.Fatalf("accepted result = %+v payload=%s, want verification warning", accepted, accepted.Payload)
	}
}

func planToolExecutorForTest(t *testing.T) sessionToolExecutor {
	t.Helper()
	dir := t.TempDir()
	layout := state.Layout{
		PlanPath:            filepath.Join(dir, "plan.json"),
		TodosPath:           filepath.Join(dir, "todos.json"),
		MemoryPath:          filepath.Join(dir, "memory.json"),
		CommandEvidencePath: filepath.Join(dir, "command_evidence.json"),
	}
	return sessionToolExecutor{
		mode:            agent.NewModeState(store.NewPlanStore(layout)),
		plan:            store.NewPlanStore(layout),
		todos:           store.NewTodoStore(layout),
		memory:          store.NewMemoryStore(layout),
		commandEvidence: store.NewCommandEvidenceStore(layout),
		reviewer: &scriptedCommandEvidenceReviewer{reviews: []agent.CommandEvidenceReview{{
			Verdict:               "sufficient",
			VerificationPerformed: true,
			Confidence:            "high",
			Reason:                "verification summary describes a passing check",
		}}},
	}
}

type scriptedCommandEvidenceReviewer struct {
	reviews []agent.CommandEvidenceReview
	err     error
	calls   int
}

func (r *scriptedCommandEvidenceReviewer) ReviewCommandEvidence(context.Context, agent.CommandEvidenceReviewRequest) (agent.CommandEvidenceReview, error) {
	r.calls++
	if r.err != nil {
		return agent.CommandEvidenceReview{}, r.err
	}
	if len(r.reviews) == 0 {
		return agent.CommandEvidenceReview{Verdict: "sufficient", Confidence: "high", Reason: "ok"}, nil
	}
	review := r.reviews[0]
	if len(r.reviews) > 1 {
		r.reviews = r.reviews[1:]
	}
	return review, nil
}

func validBuiltInPlanForTest() string {
	return "# Goal\nValid built-in plan for testing atomic plan and todo writes.\n\n# Files\n- internal/builtintools/write_plan.go\n- internal/builtintools/write_todos.go\n\n# Changes\n1. Validate the implementation plan first.\n2. Validate the todo list second.\n3. Persist both runtime artifacts only after validation succeeds.\n\n# Verification\nRun targeted tests for built-in plan and todo behavior."
}

func writePlanArgsForTest(t *testing.T, plan string, todos any) json.RawMessage {
	t.Helper()
	args := map[string]any{"plan": plan}
	if todos != nil {
		args["todos"] = todos
	}
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal write_plan args: %v", err)
	}
	return data
}

func writeTodosArgsForTest(t *testing.T, todos []agent.Todo) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(map[string]any{"todos": todos})
	if err != nil {
		t.Fatalf("marshal write_todos args: %v", err)
	}
	return data
}

func marshalRawMessageForTest(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal raw message: %v", err)
	}
	return data
}

func assertSchemaObjectsClosed(t *testing.T, path string, schema map[string]any) {
	t.Helper()
	if schema["type"] == "object" {
		switch additional := schema["additionalProperties"].(type) {
		case bool:
			if !additional {
				// Closed object schema.
			} else {
				t.Fatalf("%s additionalProperties = true, want false or typed map", path)
			}
		case map[string]any:
			if additional["type"] != "string" {
				t.Fatalf("%s additionalProperties = %#v, want string map schema", path, additional)
			}
		default:
			t.Fatalf("%s additionalProperties = %#v, want false or string map schema", path, schema["additionalProperties"])
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for name, prop := range properties {
			if nested, ok := prop.(map[string]any); ok {
				assertSchemaObjectsClosed(t, path+".properties."+name, nested)
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		assertSchemaObjectsClosed(t, path+".items", items)
	}
}

func assertCommandContentContains(t *testing.T, content string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, missing %q", content, want)
		}
	}
}

func newSessionWithWorkspaceAndConfig(t *testing.T, workspace string, config SessionConfig) *Session {
	t.Helper()
	if config.Runtime == nil && config.Execution.Mode == "" {
		config.Execution.Mode = ExecutionModeLocal
	}
	session, err := newSessionWithConfig(workspace, "", config)
	if err != nil {
		t.Fatalf("NewSessionWithConfig: %v", err)
	}
	return session
}

func newTestSessionWithConfig(t *testing.T, config SessionConfig) *Session {
	t.Helper()
	return newSessionWithWorkspaceAndConfig(t, tempWorkspace(t), config)
}

func lastToolResultMessage(t *testing.T, session *Session) ToolResult {
	t.Helper()
	history := loadSessionAgentHistory(t, session)
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].ToolResult != nil {
			return *history[i].ToolResult
		}
	}
	t.Fatalf("history = %+v, want tool result", history)
	return ToolResult{}
}

func readFileInTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}

func TestBuiltInToolEntriesAreDeterministic(t *testing.T) {
	first := sessionAgentToolNames(toolDefinitionsFromBuiltinsForTest())
	second := sessionAgentToolNames(toolDefinitionsFromBuiltinsForTest())
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("built-in entries not deterministic: %v vs %v", first, second)
	}
}

func toolDefinitionsFromBuiltinsForTest() []ToolDefinition {
	entries := builtinToolEntries()
	definitions := make([]ToolDefinition, 0, len(entries))
	for _, entry := range entries {
		definitions = append(definitions, ToolDefinition{Name: entry.Name})
	}
	return definitions
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, value := range got {
		seen[value]++
	}
	for _, value := range want {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}
