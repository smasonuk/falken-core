package falken

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/files"
	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

func TestPolicyCheckingWorkspaceOperationsReadFile(t *testing.T) {
	root := "/virtual-workspace"

	t.Run("blocked rule denies before delegate", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			BlockedFiles: []policy.FileRule{{
				Path:  filepath.Join(root, "secret.txt"),
				Match: policy.MatchExact,
				Modes: []policy.FileAccessMode{policy.FileAccessRead},
			}},
		})

		result, err := ops.ReadFile(context.Background(), workspacefiles.ReadFileRequest{Path: "secret.txt"})
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if result.Success || result.Status != string(files.ReadStatusDenied) || result.Error == "" {
			t.Fatalf("result = %+v, want denied read", result)
		}
		if delegate.called("read_file") {
			t.Fatal("delegate was called for denied read")
		}
	})

	t.Run("project policy allows delegate", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			ProjectApprovedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessRead},
			}},
		})

		if _, err := ops.ReadFile(context.Background(), workspacefiles.ReadFileRequest{Path: "project.txt"}); err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !delegate.called("read_file") {
			t.Fatal("delegate was not called for allowed read")
		}
	})
}

func TestPolicyCheckingWorkspaceOperationsWriteFileModes(t *testing.T) {
	root := "/virtual-workspace"

	t.Run("create checks create permission", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessCreate},
			}},
		})

		if _, err := ops.WriteFile(context.Background(), workspacefiles.WriteFileRequest{
			Path:      "created.txt",
			Content:   "hello",
			Operation: files.WriteOperationCreate,
		}); err != nil {
			t.Fatalf("WriteFile create: %v", err)
		}
		if !delegate.called("write_file") {
			t.Fatal("delegate was not called for create-approved write")
		}
	})

	t.Run("overwrite checks write permission", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessWrite},
			}},
		})

		if _, err := ops.WriteFile(context.Background(), workspacefiles.WriteFileRequest{
			Path:      "existing.txt",
			Content:   "hello",
			Operation: files.WriteOperationOverwrite,
		}); err != nil {
			t.Fatalf("WriteFile overwrite: %v", err)
		}
		if !delegate.called("write_file") {
			t.Fatal("delegate was not called for write-approved overwrite")
		}
	})

	t.Run("create_or_overwrite requires create and write", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessCreate},
			}},
		})

		result, err := ops.WriteFile(context.Background(), workspacefiles.WriteFileRequest{
			Path:    "maybe.txt",
			Content: "hello",
		})
		if err != nil {
			t.Fatalf("WriteFile create_or_overwrite: %v", err)
		}
		if result.Success || result.Status != string(files.WriteStatusDenied) {
			t.Fatalf("result = %+v, want denied create_or_overwrite without write permission", result)
		}
		if delegate.called("write_file") {
			t.Fatal("delegate was called without write permission")
		}

		delegate, ops = newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessCreate, policy.FileAccessWrite},
			}},
		})
		if _, err := ops.WriteFile(context.Background(), workspacefiles.WriteFileRequest{Path: "maybe.txt", Content: "hello"}); err != nil {
			t.Fatalf("WriteFile create_or_overwrite with both modes: %v", err)
		}
		if !delegate.called("write_file") {
			t.Fatal("delegate was not called with create and write permission")
		}
	})

	t.Run("blocked create does not call delegate", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			BlockedFiles: []policy.FileRule{{
				Path:  filepath.Join(root, "blocked.txt"),
				Match: policy.MatchExact,
				Modes: []policy.FileAccessMode{policy.FileAccessCreate},
			}},
		})

		result, err := ops.WriteFile(context.Background(), workspacefiles.WriteFileRequest{
			Path:      "blocked.txt",
			Content:   "hello",
			Operation: files.WriteOperationCreate,
		})
		if err != nil {
			t.Fatalf("WriteFile blocked create: %v", err)
		}
		if result.Success || result.Status != string(files.WriteStatusDenied) {
			t.Fatalf("result = %+v, want denied write", result)
		}
		if delegate.called("write_file") {
			t.Fatal("delegate was called for denied write")
		}
	})
}

func TestPolicyCheckingWorkspaceOperationsEditAndMultiEdit(t *testing.T) {
	root := "/virtual-workspace"

	t.Run("edit allowed delegates", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessWrite},
			}},
		})
		if _, err := ops.EditFile(context.Background(), workspacefiles.EditFileRequest{Path: "allowed.txt", Old: "a", New: "b"}); err != nil {
			t.Fatalf("EditFile: %v", err)
		}
		if !delegate.called("edit_file") {
			t.Fatal("delegate was not called for allowed edit")
		}
	})

	t.Run("edit denied does not call delegate", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			BlockedFiles: []policy.FileRule{{
				Path:  filepath.Join(root, "blocked.txt"),
				Match: policy.MatchExact,
				Modes: []policy.FileAccessMode{policy.FileAccessWrite},
			}},
		})

		result, err := ops.EditFile(context.Background(), workspacefiles.EditFileRequest{Path: "blocked.txt", Old: "a", New: "b"})
		if err != nil {
			t.Fatalf("EditFile: %v", err)
		}
		if result.Success || result.Status != string(files.EditStatusMutationRejected) {
			t.Fatalf("result = %+v, want mutation rejection", result)
		}
		if delegate.called("edit_file") {
			t.Fatal("delegate was called for denied edit")
		}
	})

	t.Run("multi edit allowed delegates", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessWrite},
			}},
		})
		if _, err := ops.MultiEdit(context.Background(), workspacefiles.MultiEditRequest{Edits: []workspacefiles.EditFileRequest{
			{Path: "one.txt", Old: "a", New: "b"},
			{Path: "two.txt", Old: "a", New: "b"},
		}}); err != nil {
			t.Fatalf("MultiEdit: %v", err)
		}
		if !delegate.called("multi_edit") {
			t.Fatal("delegate was not called for allowed multi_edit")
		}
	})

	t.Run("multi edit stops on denied path", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			BlockedFiles: []policy.FileRule{{
				Path:  filepath.Join(root, "blocked.txt"),
				Match: policy.MatchExact,
				Modes: []policy.FileAccessMode{policy.FileAccessWrite},
			}},
		})

		result, err := ops.MultiEdit(context.Background(), workspacefiles.MultiEditRequest{Edits: []workspacefiles.EditFileRequest{
			{Path: "allowed.txt", Old: "a", New: "b"},
			{Path: "blocked.txt", Old: "a", New: "b"},
		}})
		if err != nil {
			t.Fatalf("MultiEdit: %v", err)
		}
		if result.Success || result.Status != string(files.MultiEditStatusFailed) {
			t.Fatalf("result = %+v, want failed multi_edit", result)
		}
		if delegate.called("multi_edit") {
			t.Fatal("delegate was called for denied multi_edit")
		}
	})
}

func TestPolicyCheckingWorkspaceOperationsDeleteFile(t *testing.T) {
	root := "/virtual-workspace"

	t.Run("allowed delete delegates", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessWrite},
			}},
		})
		if _, err := ops.DeleteFile(context.Background(), workspacefiles.DeleteFileRequest{Path: "allowed.txt"}); err != nil {
			t.Fatalf("DeleteFile: %v", err)
		}
		if !delegate.called("delete_file") {
			t.Fatal("delegate was not called for allowed delete")
		}
	})

	t.Run("blocked delete does not call delegate", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			BlockedFiles: []policy.FileRule{{
				Path:  filepath.Join(root, "blocked.txt"),
				Match: policy.MatchExact,
				Modes: []policy.FileAccessMode{policy.FileAccessWrite},
			}},
		})
		result, err := ops.DeleteFile(context.Background(), workspacefiles.DeleteFileRequest{Path: "blocked.txt"})
		if err != nil {
			t.Fatalf("DeleteFile: %v", err)
		}
		if result.Success || result.Status != string(files.DeleteStatusDenied) {
			t.Fatalf("result = %+v, want denied delete", result)
		}
		if delegate.called("delete_file") {
			t.Fatal("delegate was called for denied delete")
		}
	})
}

func TestPolicyCheckingWorkspaceOperationsSearchRoots(t *testing.T) {
	root := "/virtual-workspace"
	config := policy.Config{
		StrictFileAllowlist: true,
		AllowedFiles: []policy.FileRule{{
			Path:  root,
			Match: policy.MatchExact,
			Modes: []policy.FileAccessMode{policy.FileAccessRead},
		}},
	}

	t.Run("glob empty path checks workspace root", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, config)
		if _, err := ops.Glob(context.Background(), workspacefiles.GlobRequest{Pattern: "**/*.go"}); err != nil {
			t.Fatalf("Glob: %v", err)
		}
		if !delegate.called("glob") {
			t.Fatal("delegate was not called after root glob policy check")
		}
	})

	t.Run("grep empty targets check workspace root", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, config)
		if _, err := ops.Grep(context.Background(), workspacefiles.GrepRequest{Regex: "needle"}); err != nil {
			t.Fatalf("Grep: %v", err)
		}
		if !delegate.called("grep") {
			t.Fatal("delegate was not called after root grep policy check")
		}
	})

	t.Run("grep checks every target path before delegation", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  filepath.Join(root, "allowed.txt"),
				Match: policy.MatchExact,
				Modes: []policy.FileAccessMode{policy.FileAccessRead},
			}},
		})
		result, err := ops.Grep(context.Background(), workspacefiles.GrepRequest{
			Regex:       "needle",
			TargetPaths: []string{"allowed.txt", "blocked.txt"},
		})
		if err != nil {
			t.Fatalf("Grep: %v", err)
		}
		if result.Success || result.Status != string(files.GrepStatusDenied) {
			t.Fatalf("result = %+v, want denied grep", result)
		}
		if delegate.called("grep") {
			t.Fatal("delegate was called before all grep target paths were allowed")
		}
	})
}

func TestPolicyCheckingWorkspaceOperationsApplyPatch(t *testing.T) {
	root := "/virtual-workspace"

	t.Run("create patch checks create permission", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessCreate},
			}},
		})
		if _, err := ops.ApplyPatch(context.Background(), workspacefiles.ApplyPatchRequest{Patch: createPatch("new.txt")}); err != nil {
			t.Fatalf("ApplyPatch create: %v", err)
		}
		if !delegate.called("apply_patch") {
			t.Fatal("delegate was not called for create-approved patch")
		}
	})

	t.Run("modify patch checks write permission", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessCreate},
			}},
		})
		result, err := ops.ApplyPatch(context.Background(), workspacefiles.ApplyPatchRequest{Patch: modifyPatch("existing.txt")})
		if err != nil {
			t.Fatalf("ApplyPatch modify: %v", err)
		}
		if result.Success || result.Status != string(files.PatchStatusFailed) {
			t.Fatalf("result = %+v, want failed modify patch without write permission", result)
		}
		if delegate.called("apply_patch") {
			t.Fatal("delegate was called for write-denied patch")
		}
	})

	t.Run("delete patch checks write permission", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessWrite},
			}},
		})
		if _, err := ops.ApplyPatch(context.Background(), workspacefiles.ApplyPatchRequest{Patch: deletePatch("old.txt")}); err != nil {
			t.Fatalf("ApplyPatch delete: %v", err)
		}
		if !delegate.called("apply_patch") {
			t.Fatal("delegate was not called for write-approved delete patch")
		}
	})

	t.Run("malformed patch does not call delegate", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{})
		result, err := ops.ApplyPatch(context.Background(), workspacefiles.ApplyPatchRequest{Patch: "not a git diff"})
		if err != nil {
			t.Fatalf("ApplyPatch malformed: %v", err)
		}
		if result.Success || result.Status != string(files.PatchStatusFailed) || result.Error == "" {
			t.Fatalf("result = %+v, want malformed patch failure", result)
		}
		if delegate.called("apply_patch") {
			t.Fatal("delegate was called for malformed patch")
		}
	})
}

func TestExtractPatchPolicyChecksRobustPaths(t *testing.T) {
	quotedPath := "path with spaces.txt"
	escapedPath := `quote " file.txt`
	tabPath := "path\twith-tab.txt"

	tests := []struct {
		name      string
		patch     string
		wantPath  string
		wantMode  policy.FileAccessMode
		wantCount int
		wantError string
	}{
		{name: "simple modify", patch: modifyPatch("existing.txt"), wantPath: "existing.txt", wantMode: policy.FileAccessWrite, wantCount: 1},
		{name: "create from dev null", patch: createPatch("new.txt"), wantPath: "new.txt", wantMode: policy.FileAccessCreate, wantCount: 1},
		{name: "delete to dev null", patch: deletePatch("old.txt"), wantPath: "old.txt", wantMode: policy.FileAccessWrite, wantCount: 1},
		{name: "quoted path with spaces", patch: modifyPatchQuoted(quotedPath), wantPath: quotedPath, wantMode: policy.FileAccessWrite, wantCount: 1},
		{name: "escaped quoted path", patch: modifyPatchQuoted(escapedPath), wantPath: escapedPath, wantMode: policy.FileAccessWrite, wantCount: 1},
		{name: "tab path in marker line", patch: modifyPatchQuoted(tabPath), wantPath: tabPath, wantMode: policy.FileAccessWrite, wantCount: 1},
		{name: "rename metadata fails safely", patch: "diff --git a/old.txt b/new.txt\nsimilarity index 90%\nrename from old.txt\nrename to new.txt\n--- a/old.txt\n+++ b/new.txt\n", wantError: "rename"},
		{name: "binary patch fails safely", patch: "diff --git a/blob.bin b/blob.bin\nBinary files a/blob.bin and b/blob.bin differ\n", wantError: "binary"},
		{name: "malformed diff header fails safely", patch: "diff --git a/only-one-path\n--- a/file.txt\n+++ b/file.txt\n", wantError: "malformed"},
		{name: "duplicate paths are deduplicated", patch: modifyPatch("dup.txt") + modifyPatch("dup.txt"), wantPath: "dup.txt", wantMode: policy.FileAccessWrite, wantCount: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checks, err := extractPatchPolicyChecks(tt.patch)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("extractPatchPolicyChecks error = %v, want containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractPatchPolicyChecks: %v", err)
			}
			if len(checks) != tt.wantCount {
				t.Fatalf("checks = %+v, want count %d", checks, tt.wantCount)
			}
			if checks[0].path != tt.wantPath || checks[0].mode != tt.wantMode {
				t.Fatalf("check = %+v, want path %q mode %s", checks[0], tt.wantPath, tt.wantMode)
			}
		})
	}
}

func TestPolicyCheckingWorkspaceOperationsVirtualPaths(t *testing.T) {
	root := "/virtual-workspace"

	t.Run("sandbox mount path resolves to virtual workspace", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{
			StrictFileAllowlist: true,
			AllowedFiles: []policy.FileRule{{
				Path:  filepath.Join(root, "foo.txt"),
				Match: policy.MatchExact,
				Modes: []policy.FileAccessMode{policy.FileAccessRead},
			}},
		})
		if _, err := ops.ReadFile(context.Background(), workspacefiles.ReadFileRequest{Path: "/workspace/foo.txt"}); err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !delegate.called("read_file") {
			t.Fatal("delegate was not called for sandbox mount path")
		}
	})

	t.Run("path traversal denied before delegate", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{})
		result, err := ops.ReadFile(context.Background(), workspacefiles.ReadFileRequest{Path: "../secret.txt"})
		if err != nil {
			t.Fatalf("ReadFile traversal: %v", err)
		}
		if result.Success || result.Status != string(files.ReadStatusUnsafe) {
			t.Fatalf("result = %+v, want unsafe path", result)
		}
		if delegate.called("read_file") {
			t.Fatal("delegate was called for traversal path")
		}
	})

	t.Run("absolute path outside workspace denied before delegate", func(t *testing.T) {
		delegate, ops := newTestPolicyWorkspaceOperations(t, root, policy.Config{})
		result, err := ops.ReadFile(context.Background(), workspacefiles.ReadFileRequest{Path: "/tmp/outside.txt"})
		if err != nil {
			t.Fatalf("ReadFile outside absolute: %v", err)
		}
		if result.Success || result.Status != string(files.ReadStatusUnsafe) {
			t.Fatalf("result = %+v, want unsafe path", result)
		}
		if delegate.called("read_file") {
			t.Fatal("delegate was called for outside absolute path")
		}
	})
}

func TestPolicyCheckingWorkspaceOperationsEmitsWorkspaceOperationEvent(t *testing.T) {
	root := "/virtual-workspace"
	state, err := runtimeexec.NewVirtualExecutionState(root)
	if err != nil {
		t.Fatalf("NewVirtualExecutionState: %v", err)
	}
	state.SetSandboxMountPath("/workspace")

	var events []Event
	delegate := &recordingWorkspaceOperations{}
	ops := newPolicyCheckingWorkspaceOperations(
		delegate,
		runtimepolicy.NewEvaluator(policy.NewEngine(policy.Config{
			AllowedFiles: []policy.FileRule{{
				Path:  root,
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessCreate},
			}},
		}, nil)),
		state,
		func(event Event) {
			events = append(events, event)
		},
	)

	const secretContent = "do-not-log-this-content"
	result, err := ops.WriteFile(context.Background(), workspacefiles.WriteFileRequest{
		Path:      "created.txt",
		Content:   secretContent,
		Operation: files.WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !result.Success {
		t.Fatalf("WriteFile success = false, result = %+v", result)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != EventWorkspaceOperation {
		t.Fatalf("event type = %q, want %q", event.Type, EventWorkspaceOperation)
	}
	if event.WorkspaceOperation == nil {
		t.Fatal("workspace operation event is nil")
	}
	payload := event.WorkspaceOperation
	if payload.Operation != string(workspacefiles.OperationWriteFile) || !payload.Remote || !payload.Success || !payload.Mutated || payload.Status != "ok" {
		t.Fatalf("workspace operation payload = %+v, want successful remote write mutation", payload)
	}
	if len(payload.Paths) != 1 || payload.Paths[0] != "created.txt" {
		t.Fatalf("paths = %#v, want [created.txt]", payload.Paths)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if strings.Contains(string(encoded), secretContent) {
		t.Fatalf("workspace operation event leaked file content: %s", encoded)
	}
}

func newTestPolicyWorkspaceOperations(t *testing.T, root string, config policy.Config) (*recordingWorkspaceOperations, workspacefiles.Operations) {
	t.Helper()
	state, err := runtimeexec.NewVirtualExecutionState(root)
	if err != nil {
		t.Fatalf("NewVirtualExecutionState: %v", err)
	}
	state.SetSandboxMountPath("/workspace")
	delegate := &recordingWorkspaceOperations{}
	ops := newPolicyCheckingWorkspaceOperations(delegate, runtimepolicy.NewEvaluator(policy.NewEngine(config, nil)), state)
	return delegate, ops
}

type recordingWorkspaceOperations struct {
	calls []string
}

func (r *recordingWorkspaceOperations) record(operation string) {
	r.calls = append(r.calls, operation)
}

func (r *recordingWorkspaceOperations) called(operation string) bool {
	for _, call := range r.calls {
		if call == operation {
			return true
		}
	}
	return false
}

func (r *recordingWorkspaceOperations) ReadFile(context.Context, workspacefiles.ReadFileRequest) (workspacefiles.ReadFileResult, error) {
	r.record("read_file")
	return workspacefiles.ReadFileResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationReadFile, Success: true, Status: "ok"}}, nil
}

func (r *recordingWorkspaceOperations) ReadFiles(context.Context, workspacefiles.ReadFilesRequest) (workspacefiles.ReadFilesResult, error) {
	r.record("read_files")
	return workspacefiles.ReadFilesResult{Operation: workspacefiles.OperationReadFiles, Success: true, Status: "ok"}, nil
}

func (r *recordingWorkspaceOperations) Glob(context.Context, workspacefiles.GlobRequest) (workspacefiles.GlobResult, error) {
	r.record("glob")
	return workspacefiles.GlobResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationGlob, Success: true, Status: "ok"}}, nil
}

func (r *recordingWorkspaceOperations) Grep(context.Context, workspacefiles.GrepRequest) (workspacefiles.GrepResult, error) {
	r.record("grep")
	return workspacefiles.GrepResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationGrep, Success: true, Status: "ok"}}, nil
}

func (r *recordingWorkspaceOperations) WriteFile(context.Context, workspacefiles.WriteFileRequest) (workspacefiles.WriteFileResult, error) {
	r.record("write_file")
	return workspacefiles.WriteFileResult{
		CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationWriteFile, Success: true, Status: "ok"},
		WorkspaceMutation: workspacefiles.WorkspaceMutationSummary{
			Observed:     true,
			FilesChanged: 1,
		},
	}, nil
}

func (r *recordingWorkspaceOperations) EditFile(context.Context, workspacefiles.EditFileRequest) (workspacefiles.EditFileResult, error) {
	r.record("edit_file")
	return workspacefiles.EditFileResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationEditFile, Success: true, Status: "ok"}}, nil
}

func (r *recordingWorkspaceOperations) MultiEdit(context.Context, workspacefiles.MultiEditRequest) (workspacefiles.MultiEditResult, error) {
	r.record("multi_edit")
	return workspacefiles.MultiEditResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationMultiEdit, Success: true, Status: "ok"}}, nil
}

func (r *recordingWorkspaceOperations) ApplyPatch(context.Context, workspacefiles.ApplyPatchRequest) (workspacefiles.ApplyPatchResult, error) {
	r.record("apply_patch")
	return workspacefiles.ApplyPatchResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationApplyPatch, Success: true, Status: "ok"}}, nil
}

func (r *recordingWorkspaceOperations) DeleteFile(context.Context, workspacefiles.DeleteFileRequest) (workspacefiles.DeleteFileResult, error) {
	r.record("delete_file")
	return workspacefiles.DeleteFileResult{CommonResult: workspacefiles.CommonResult{Operation: workspacefiles.OperationDeleteFile, Success: true, Status: "ok"}}, nil
}

func createPatch(path string) string {
	return "diff --git a/" + path + " b/" + path + "\n" +
		"--- /dev/null\n" +
		"+++ b/" + path + "\n" +
		"@@ -0,0 +1 @@\n" +
		"+hello\n"
}

func modifyPatch(path string) string {
	return "diff --git a/" + path + " b/" + path + "\n" +
		"--- a/" + path + "\n" +
		"+++ b/" + path + "\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new\n"
}

func deletePatch(path string) string {
	return "diff --git a/" + path + " b/" + path + "\n" +
		"--- a/" + path + "\n" +
		"+++ /dev/null\n" +
		"@@ -1 +0,0 @@\n" +
		"-old\n"
}

func modifyPatchQuoted(path string) string {
	oldPath := strconv.Quote("a/" + path)
	newPath := strconv.Quote("b/" + path)
	return "diff --git " + oldPath + " " + newPath + "\n" +
		"--- " + oldPath + "\t2026-05-16\n" +
		"+++ " + newPath + "\t2026-05-16\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new\n"
}
