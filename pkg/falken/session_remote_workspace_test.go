package falken

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

func TestSessionRemoteWorkspaceModeDoesNotRequireLocalWorkspace(t *testing.T) {
	setStateHomeEnv(t)

	workspaceRoot := filepath.Join(t.TempDir(), "missing-agent-workspace")
	workspaceOps := &sessionRemoteWorkspaceOperations{}
	sandbox := &countingSandboxRuntime{stdout: "command output"}
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{
				{ID: "read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"remote.txt"}`)},
				{ID: "write-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"created.txt","content":"hello","operation":"create"}`)},
				{ID: "command-1", Name: "execute_command", Arguments: json.RawMessage(`{"command":"pwd"}`)},
			},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}

	session, err := New(Config{
		WorkspaceDir:     workspaceRoot,
		StateDir:         filepath.Join(t.TempDir(), "agent-state"),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeSandbox, WorkspaceMountPath: "/workspace"},
		Runtime: runtimeAdaptersProvider{adapters: RuntimeAdapters{
			SandboxRuntime: sandbox,
			WorkspaceFiles: workspaceOps,
		}},
		LLM:         llm,
		PlanRouting: PlanRoutingDisabled,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.StartContext(context.Background()); err != nil {
		t.Fatalf("StartContext: %v", err)
	}
	if _, err := os.Stat(workspaceRoot); !os.IsNotExist(err) {
		t.Fatalf("agent workspace stat error = %v, want missing local workspace", err)
	}

	result, err := session.Run(context.Background(), RunRequest{Prompt: "exercise remote workspace"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "done" {
		t.Fatalf("result = %+v, want completed done", result)
	}
	if workspaceOps.starts != 1 {
		t.Fatalf("workspace ops starts = %d, want 1", workspaceOps.starts)
	}
	if workspaceOps.calls["read_file"] != 1 || workspaceOps.calls["write_file"] != 1 {
		t.Fatalf("workspace op calls = %+v, want read_file/write_file", workspaceOps.calls)
	}
	if sandbox.executes != 1 {
		t.Fatalf("sandbox executes = %d, want 1", sandbox.executes)
	}
	if sandbox.lastRequest.HostWorkingDir != filepath.Clean(workspaceRoot) {
		t.Fatalf("sandbox host working dir = %q, want virtual workspace root %q", sandbox.lastRequest.HostWorkingDir, filepath.Clean(workspaceRoot))
	}
	if _, err := os.Stat(workspaceRoot); !os.IsNotExist(err) {
		t.Fatalf("agent workspace after run stat error = %v, want still missing", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if workspaceOps.closes != 1 {
		t.Fatalf("workspace ops closes = %d, want 1", workspaceOps.closes)
	}
}

type sessionRemoteWorkspaceOperations struct {
	calls  map[string]int
	starts int
	closes int
}

func (o *sessionRemoteWorkspaceOperations) record(operation string) {
	if o.calls == nil {
		o.calls = make(map[string]int)
	}
	o.calls[operation]++
}

func (o *sessionRemoteWorkspaceOperations) Start(context.Context) error {
	o.starts++
	return nil
}

func (o *sessionRemoteWorkspaceOperations) Close(context.Context) error {
	o.closes++
	return nil
}

func (o *sessionRemoteWorkspaceOperations) ReadFile(_ context.Context, req workspacefiles.ReadFileRequest) (workspacefiles.ReadFileResult, error) {
	o.record("read_file")
	return workspacefiles.ReadFileResult{
		CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationReadFile, Success: true, Status: "ok", Path: req.Path},
		Content:      "remote content",
		HasToken:     true,
	}, nil
}

func (o *sessionRemoteWorkspaceOperations) ReadFiles(context.Context, workspacefiles.ReadFilesRequest) (workspacefiles.ReadFilesResult, error) {
	o.record("read_files")
	return workspacefiles.ReadFilesResult{Operation: workspacefiles.OperationReadFiles, Success: true, Status: "ok"}, nil
}

func (o *sessionRemoteWorkspaceOperations) Glob(context.Context, workspacefiles.GlobRequest) (workspacefiles.GlobResult, error) {
	o.record("glob")
	return workspacefiles.GlobResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationGlob, Success: true, Status: "ok"}}, nil
}

func (o *sessionRemoteWorkspaceOperations) Grep(context.Context, workspacefiles.GrepRequest) (workspacefiles.GrepResult, error) {
	o.record("grep")
	return workspacefiles.GrepResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationGrep, Success: true, Status: "ok"}}, nil
}

func (o *sessionRemoteWorkspaceOperations) WriteFile(_ context.Context, req workspacefiles.WriteFileRequest) (workspacefiles.WriteFileResult, error) {
	o.record("write_file")
	return workspacefiles.WriteFileResult{
		CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationWriteFile, Success: true, Status: "created", Path: req.Path},
		Created:      true,
	}, nil
}

func (o *sessionRemoteWorkspaceOperations) EditFile(context.Context, workspacefiles.EditFileRequest) (workspacefiles.EditFileResult, error) {
	o.record("edit_file")
	return workspacefiles.EditFileResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationEditFile, Success: true, Status: "changed"}, Changed: true}, nil
}

func (o *sessionRemoteWorkspaceOperations) MultiEdit(context.Context, workspacefiles.MultiEditRequest) (workspacefiles.MultiEditResult, error) {
	o.record("multi_edit")
	return workspacefiles.MultiEditResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationMultiEdit, Success: true, Status: "applied"}}, nil
}

func (o *sessionRemoteWorkspaceOperations) ApplyPatch(context.Context, workspacefiles.ApplyPatchRequest) (workspacefiles.ApplyPatchResult, error) {
	o.record("apply_patch")
	return workspacefiles.ApplyPatchResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationApplyPatch, Success: true, Status: "applied"}}, nil
}

func (o *sessionRemoteWorkspaceOperations) DeleteFile(context.Context, workspacefiles.DeleteFileRequest) (workspacefiles.DeleteFileResult, error) {
	o.record("delete_file")
	return workspacefiles.DeleteFileResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationDeleteFile, Success: true, Status: "deleted"}, Deleted: true}, nil
}
