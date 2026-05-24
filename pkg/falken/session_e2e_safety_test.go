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

	"github.com/smasonuk/falken-core/internal/files"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestSessionE2E_PermissionDenied(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	secretPath := writeFileInWorkspaceE2E(t, workspace, "secret.txt", "secret")
	deniedWritePath := filepath.Join(workspace, "blocked.txt")
	secretPolicyPath := realPathE2E(t, secretPath)
	deniedWritePolicyPath := realPathForCreateE2E(t, deniedWritePath)
	session := newPublicSafetySession(t, Config{
		WorkspaceDir: workspace,
		LLM:          noopLLM{},
		Policy: PolicyConfig{
			BlockedFiles: []FileRule{
				{Path: secretPolicyPath, Match: MatchExact, Modes: []FileAccessMode{FileAccessRead}},
				{Path: deniedWritePolicyPath, Match: MatchExact, Modes: []FileAccessMode{FileAccessCreate}},
			},
			BlockedShell: []ShellRule{{Command: "printf denied", Match: MatchExact}},
			BlockedNetwork: []NetworkRule{{
				Host:  "blocked.example",
				Match: MatchExact,
			}},
		},
	})
	ops := sessionRuntimeFileOps(t, session)

	read, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "secret.txt"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if read.Success || read.Status != string(files.ReadStatusDenied) {
		t.Fatalf("read result = %+v, want denied", read)
	}

	write, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "blocked.txt",
		Content:   "nope",
		Operation: files.WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if write.Success || write.Status != string(files.WriteStatusDenied) {
		t.Fatalf("write result = %+v, want denied", write)
	}
	assertPathMissingE2E(t, deniedWritePath)

	command, err := session.executeCommand(context.Background(), runtimeexec.CommandRequest{Command: "printf denied"})
	if err != nil {
		t.Fatalf("executeCommand: %v", err)
	}
	if command.Status != runtimeexec.CommandStatusBlocked || command.Executed {
		t.Fatalf("command result = %+v, want blocked without execution", command)
	}

	network, err := session.resources.runtime.executionPolicy.EvaluateNetwork(context.Background(), runtimepolicy.NetworkRequest{Host: "blocked.example"})
	if err != nil {
		t.Fatalf("EvaluateNetwork: %v", err)
	}
	if network.Allowed {
		t.Fatalf("network result = %+v, want denied", network)
	}
}

func TestSessionE2E_WorkspaceEscapeRejected(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	inside := writeFileInWorkspaceE2E(t, workspace, "safe.txt", "safe")
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	symlink := filepath.Join(workspace, "escape-link")
	if err := os.Symlink(outside, symlink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	session := newPublicSafetySession(t, Config{WorkspaceDir: workspace, LLM: noopLLM{}})
	ops := sessionRuntimeFileOps(t, session)

	for _, requestPath := range []string{outside, "../outside.txt", "escape-link"} {
		read, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: requestPath})
		if err != nil {
			t.Fatalf("ReadFile %q: %v", requestPath, err)
		}
		if read.Success || read.Status != string(files.ReadStatusUnsafe) {
			t.Fatalf("read %q = %+v, want unsafe", requestPath, read)
		}
	}

	write, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "../outside-write.txt",
		Content:   "nope",
		Operation: files.WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("WriteFile traversal: %v", err)
	}
	if write.Success || write.Status != string(files.WriteStatusUnsafe) {
		t.Fatalf("write traversal = %+v, want unsafe", write)
	}
	assertFileContentE2E(t, inside, "safe")
	assertFileContentE2E(t, outside, "outside")
}

func TestSessionE2E_ManagedMutationFailuresPreserveWorkspace(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	notes := writeFileInWorkspaceE2E(t, workspace, "notes.txt", "alpha beta alpha\n")
	group := writeFileInWorkspaceE2E(t, workspace, "group.txt", "one\ntwo\n")
	patchTarget := writeFileInWorkspaceE2E(t, workspace, "patch.txt", "safe\n")
	session := newPublicSafetySession(t, Config{WorkspaceDir: workspace, LLM: noopLLM{}})
	ops := sessionRuntimeFileOps(t, session)

	missingToken, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "notes.txt",
		Content:   "new",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("overwrite without token: %v", err)
	}
	if missingToken.Success || missingToken.Status != string(files.WriteStatusMissingReadToken) {
		t.Fatalf("overwrite without token = %+v, want missing token", missingToken)
	}
	assertFileContentE2E(t, notes, "alpha beta alpha\n")

	if read, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "notes.txt"}); err != nil || !read.Success {
		t.Fatalf("ReadFile before stale test = %+v/%v", read, err)
	}
	if err := os.WriteFile(notes, []byte("changed elsewhere\n"), 0o600); err != nil {
		t.Fatalf("external mutate notes: %v", err)
	}
	stale, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "notes.txt",
		Content:   "managed overwrite",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("stale overwrite: %v", err)
	}
	if stale.Success || stale.Status != string(files.WriteStatusStaleReadToken) {
		t.Fatalf("stale overwrite = %+v, want stale token", stale)
	}
	assertFileContentE2E(t, notes, "changed elsewhere\n")

	secret, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "secret.txt",
		Content:   "token sk-123456789012345678901234",
		Operation: files.WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("secret write: %v", err)
	}
	if secret.Success || secret.Status != string(files.WriteStatusSecretRejected) {
		t.Fatalf("secret write = %+v, want secret rejection", secret)
	}
	assertPathMissingE2E(t, filepath.Join(workspace, "secret.txt"))

	if read, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "notes.txt"}); err != nil || !read.Success {
		t.Fatalf("ReadFile before edit test = %+v/%v", read, err)
	}
	noMatch, err := ops.EditFile(context.Background(), runtimefiles.EditFileRequest{
		Path: "notes.txt",
		Old:  "not present",
		New:  "replacement",
	})
	if err != nil {
		t.Fatalf("EditFile no-match: %v", err)
	}
	if noMatch.Success || noMatch.Status != string(files.EditStatusNoMatch) {
		t.Fatalf("no-match edit = %+v, want no_match", noMatch)
	}
	assertFileContentE2E(t, notes, "changed elsewhere\n")

	if read, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "group.txt"}); err != nil || !read.Success {
		t.Fatalf("ReadFile before multi-edit = %+v/%v", read, err)
	}
	multi, err := ops.MultiEdit(context.Background(), runtimefiles.MultiEditRequest{Edits: []runtimefiles.EditFileRequest{
		{Path: "group.txt", Old: "one", New: "ONE"},
		{Path: "group.txt", Old: "missing", New: "MISSING"},
	}})
	if err != nil {
		t.Fatalf("MultiEdit: %v", err)
	}
	if multi.Success || multi.Status != string(files.MultiEditStatusFailed) {
		t.Fatalf("multi-edit = %+v, want failed", multi)
	}
	assertFileContentE2E(t, group, "one\ntwo\n")

	patch, err := ops.ApplyPatch(context.Background(), runtimefiles.ApplyPatchRequest{Patch: `diff --git a/patch.txt b/patch.txt
--- a/patch.txt
+++ b/patch.txt
@@ -1 +1 @@
-safe
+changed
diff --git a/secret-patch.txt b/secret-patch.txt
new file mode 100644
--- /dev/null
+++ b/secret-patch.txt
@@ -0,0 +1 @@
+token sk-123456789012345678901234
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if patch.Success || patch.Status != string(files.PatchStatusFailed) {
		t.Fatalf("patch = %+v, want failed", patch)
	}
	assertFileContentE2E(t, patchTarget, "safe\n")
	assertPathMissingE2E(t, filepath.Join(workspace, "secret-patch.txt"))
}

func TestSessionE2E_ShellSafetyBlocksBypassAndDestructiveCommands(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	marker := filepath.Join(workspace, "marker.txt")
	target := filepath.Join(workspace, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	keep := writeFileInWorkspaceE2E(t, workspace, "target/keep.txt", "keep")
	session := newPublicSafetySession(t, Config{WorkspaceDir: workspace, LLM: noopLLM{}})

	writeBypass, err := session.executeCommand(context.Background(), runtimeexec.CommandRequest{
		Command: "echo hi > marker.txt",
	})
	if err != nil {
		t.Fatalf("shell write command: %v", err)
	}
	if writeBypass.Status != runtimeexec.CommandStatusBlocked || writeBypass.Executed || !writeBypass.Policy.BlockedByShellWriteBypass {
		t.Fatalf("shell write result = %+v, want blocked bypass", writeBypass)
	}
	assertPathMissingE2E(t, marker)

	destructive, err := session.executeCommand(context.Background(), runtimeexec.CommandRequest{
		Command: "rm -rf target",
	})
	if err != nil {
		t.Fatalf("destructive command: %v", err)
	}
	if destructive.Status != runtimeexec.CommandStatusBlocked || destructive.Executed ||
		destructive.Policy.ApprovalStatus != runtimepolicy.ApprovalStatusDenied {
		t.Fatalf("destructive result = %+v, want blocked approval-required command", destructive)
	}
	assertFileContentE2E(t, keep, "keep")

	denied := newPublicSafetySession(t, Config{
		WorkspaceDir: tempWorkspace(t),
		LLM:          noopLLM{},
		Policy:       PolicyConfig{StrictCommandAllowlist: true},
	})
	result, err := denied.executeCommand(context.Background(), runtimeexec.CommandRequest{Command: "printf nope"})
	if err != nil {
		t.Fatalf("denied command: %v", err)
	}
	if result.Status != runtimeexec.CommandStatusBlocked || result.Executed {
		t.Fatalf("denied command result = %+v, want blocked", result)
	}
}

func TestSessionE2E_AgentRuntimeFailuresAndRecoverableToolErrors(t *testing.T) {
	setStateHomeEnv(t)

	callbackCalled := false
	var failureEvents []Event
	failingSession := newPublicSafetySession(t, Config{
		WorkspaceDir: tempWorkspace(t),
		LLM:          &sessionFakeLLM{err: errors.New("llm exploded")},
		Events:       func(event Event) { failureEvents = append(failureEvents, event) },
		OnCompleted:  func(context.Context, RunResult) error { callbackCalled = true; return nil },
	})
	failureResult, err := failingSession.Run(context.Background(), RunRequest{Prompt: "fail"})
	if err == nil || !strings.Contains(err.Error(), "llm exploded") {
		t.Fatalf("LLM failure error = %v, want llm exploded", err)
	}
	if failureResult.Completed || callbackCalled {
		t.Fatalf("failure result/callback = %+v/%v, want failed run and no callback", failureResult, callbackCalled)
	}
	if got := sessionAgentEventTypes(failureEvents); !reflect.DeepEqual(got, []EventType{EventPlanRouting, EventRunFailed}) {
		t.Fatalf("failure events = %v, want run_failed", got)
	}

	loopLLM := &sessionFakeLLM{}
	for i := 0; i < 9; i++ {
		loopLLM.responses = append(loopLLM.responses, CompletionResponse{
			ToolCalls: []ToolCall{{
				ID:        "loop-call",
				Name:      "unknown_tool",
				Arguments: json.RawMessage(`{}`),
			}},
			FinishReason: FinishReasonToolCalls,
		})
	}
	var loopEvents []Event
	loopSession := newPublicSafetySession(t, Config{
		WorkspaceDir: tempWorkspace(t),
		LLM:          loopLLM,
		Events:       func(event Event) { loopEvents = append(loopEvents, event) },
	})
	loopResult, err := loopSession.Run(context.Background(), RunRequest{Prompt: "loop"})
	if err == nil || !strings.Contains(err.Error(), "repeated identical tool failure") {
		t.Fatalf("loop guard error = %v, want repeated identical tool failure", err)
	}
	if loopResult.Completed {
		t.Fatalf("loop result = %+v, want failed", loopResult)
	}
	if got := sessionAgentEventTypes(loopEvents); len(got) == 0 || got[len(got)-1] != EventRunFailed {
		t.Fatalf("loop events = %v, want final run_failed", got)
	}

	recoverableLLM := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "bad_tool",
				Arguments: json.RawMessage(`{"unterminated"`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{
			ToolCalls: []ToolCall{{
				ID:        "call-2",
				Name:      "unknown_tool",
				Arguments: json.RawMessage(`{}`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{
			ToolCalls: []ToolCall{{
				ID:        "call-3",
				Name:      "bad_tool",
				Arguments: json.RawMessage(`{}`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "recovered", FinishReason: FinishReasonStop},
	}}
	var recoverableEvents []Event
	recoverableSession := newPublicSafetySession(t, Config{
		WorkspaceDir: tempWorkspace(t),
		ToolProviders: []ToolProvider{StaticToolProvider(ToolFunc(testToolDescriptor("bad_tool"), func(context.Context, ToolInvocation) (ToolExecutionResult, error) {
			return ToolExecutionResult{}, errors.New("provider runtime failed")
		}))},
		LLM:    recoverableLLM,
		Events: func(event Event) { recoverableEvents = append(recoverableEvents, event) },
	})
	recoverableResult, err := recoverableSession.Run(context.Background(), RunRequest{Prompt: "recover"})
	if err != nil {
		t.Fatalf("recoverable Run: %v", err)
	}
	if !recoverableResult.Completed || recoverableResult.FinalOutput != "recovered" {
		t.Fatalf("recoverable result = %+v, want completed", recoverableResult)
	}
	var toolErrors []string
	for _, event := range recoverableEvents {
		if event.Type == EventToolResult && event.ToolResult != nil {
			toolErrors = append(toolErrors, event.ToolResult.Error)
		}
	}
	if len(toolErrors) != 3 ||
		!strings.Contains(toolErrors[0], "valid JSON") ||
		!strings.Contains(toolErrors[1], "not active or unknown") ||
		!strings.Contains(toolErrors[2], "provider runtime failed") {
		t.Fatalf("tool errors = %#v, want malformed/unknown/runtime failures", toolErrors)
	}
}

func TestSessionE2E_PlanModeBlocksMutationAndCommandTools(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	unchanged := writeFileInWorkspaceE2E(t, workspace, "unchanged.txt", "safe")
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{
				{ID: "call-write", Name: "write_file", Arguments: json.RawMessage(`{"path":"unchanged.txt","content":"mutated"}`)},
				{ID: "call-command", Name: "execute_command", Arguments: json.RawMessage(`{"command":"echo hi > unchanged.txt"}`)},
			},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "planned safely", FinishReason: FinishReasonStop},
	}}
	var events []Event
	session := newPublicSafetySession(t, Config{
		WorkspaceDir: workspace,
		LLM:          llm,
		Events:       func(event Event) { events = append(events, event) },
	})
	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}

	result, err := session.Run(context.Background(), RunRequest{Prompt: "plan mutation"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "planned safely" {
		t.Fatalf("result = %+v, want completed after blocked tools", result)
	}
	assertFileContentE2E(t, unchanged, "safe")
	var blocked int
	for _, event := range events {
		if event.Type == EventToolResult && event.ToolResult != nil && strings.Contains(event.ToolResult.Error, "plan mode") {
			blocked++
		}
	}
	if blocked != 2 {
		t.Fatalf("blocked tool results = %d, want 2; events=%+v", blocked, events)
	}
}

func TestSessionE2E_LifecycleAfterFailureAndV1NonGoals(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{err: errors.New("temporary model failure")}
	session := newPublicSafetySession(t, Config{
		WorkspaceDir: tempWorkspace(t),
		LLM:          llm,
	})
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "fail"}); err == nil {
		t.Fatal("first Run succeeded, want failure")
	}
	llm.err = nil
	llm.responses = []CompletionResponse{{AssistantText: "normal completion", FinishReason: FinishReasonStop}}
	if result, err := session.Run(context.Background(), RunRequest{Prompt: "recover"}); err != nil || result.FinalOutput != "normal completion" {
		t.Fatalf("run after failure = %+v/%v, want normal completion", result, err)
	}
	if err := session.ResetConversationState(); err != nil {
		t.Fatalf("ResetConversationState after failure/recovery: %v", err)
	}
	history, err := session.stores.history.Read()
	if err != nil {
		t.Fatalf("history after reset: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history after reset = %+v, want empty", history)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close after failure: %v", err)
	}
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "closed"}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Run after close = %v, want ErrSessionClosed", err)
	}

	nonGoalLLM := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done without ceremony", FinishReason: FinishReasonStop}}}
	nonGoal := newPublicSafetySession(t, Config{
		WorkspaceDir: tempWorkspace(t),
		LLM:          nonGoalLLM,
	})
	if err := nonGoal.stores.todos.Write(store.TodoState{Items: []store.TodoItem{{ID: "todo-1", Text: "still pending", Status: "pending"}}}); err != nil {
		t.Fatalf("write pending todo: %v", err)
	}
	result, err := nonGoal.Run(context.Background(), RunRequest{Prompt: "finish"})
	if err == nil || !strings.Contains(err.Error(), "implementation submission required") {
		t.Fatalf("non-goal Run error = %v, want implementation submission required", err)
	}
	if result.Completed {
		t.Fatalf("non-goal result = %+v, want completion blocked", result)
	}
	if len(nonGoalLLM.requests) < 1 {
		t.Fatalf("non-goal LLM requests = %d, want main call", len(nonGoalLLM.requests))
	}
	for _, tool := range sessionAgentToolNames(nonGoalLLM.requests[0].Tools) {
		if strings.Contains(tool, "delegate") {
			t.Fatalf("non-goal tool %q exposed; tools=%+v", tool, nonGoalLLM.requests[0].Tools)
		}
	}
}

func newPublicSafetySession(t *testing.T, config Config) *Session {
	t.Helper()
	if config.Runtime == nil && config.ExecutionDetails.Mode == "" {
		config.ExecutionDetails.Mode = ExecutionModeLocal
	}
	if config.ApprovalHandler == nil {
		config.ApprovalHandler = projectApprovalHandler{fileScope: ApprovalScopeOnce}
	}

	session, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return session
}

func sessionRuntimeFileOps(t *testing.T, session *Session) *runtimefiles.Operations {
	t.Helper()

	service, err := files.NewServiceForLayout(session.layout, session.resources.runtime.executionPolicy, "e2e-session")
	if err != nil {
		t.Fatalf("NewServiceForLayout: %v", err)
	}
	ops, err := runtimefiles.NewOperations(service)
	if err != nil {
		t.Fatalf("NewOperations: %v", err)
	}
	return ops
}

func writeFileInWorkspaceE2E(t *testing.T, workspace, relativePath, content string) string {
	t.Helper()

	path := filepath.Join(workspace, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	return path
}

func assertFileContentE2E(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("content of %q = %q, want %q", path, string(data), want)
	}
}

func assertPathMissingE2E(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path %q stat err = %v, want missing", path, err)
	}
}

func realPathE2E(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("eval symlinks for %q: %v", path, err)
	}
	return filepath.Clean(resolved)
}

func realPathForCreateE2E(t *testing.T, path string) string {
	t.Helper()

	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatalf("eval parent symlinks for %q: %v", path, err)
	}
	return filepath.Join(filepath.Clean(parent), filepath.Base(path))
}
