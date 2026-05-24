package files_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/files"
	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/state"
)

func TestReadFileInsideWorkspaceSucceedsAndIssuesToken(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "hello\nworld\n")
	service := newReadService(t, workspace, policy.Config{}, "run-1")

	result, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if result.Status != files.ReadStatusOK {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.ReadStatusOK, result)
	}
	if result.ResolvedPath != path {
		t.Fatalf("resolved path = %q, want %q", result.ResolvedPath, path)
	}
	if result.Content != "hello\nworld\n" {
		t.Fatalf("content = %q, want full file", result.Content)
	}
	if !result.Policy.Allowed {
		t.Fatal("expected policy to allow read")
	}
	if !result.HasToken {
		t.Fatal("expected read token to be issued")
	}
	if result.Token.Path != path {
		t.Fatalf("token path = %q, want %q", result.Token.Path, path)
	}
	if result.Token.ScopeID != "run-1" {
		t.Fatalf("token scope = %q, want run-1", result.Token.ScopeID)
	}
	if result.Token.Size != int64(len("hello\nworld\n")) {
		t.Fatalf("token size = %d, want %d", result.Token.Size, len("hello\nworld\n"))
	}
	if result.Token.ContentHash != sha256Hex("hello\nworld\n") {
		t.Fatalf("token hash = %q, want %q", result.Token.ContentHash, sha256Hex("hello\nworld\n"))
	}
	if result.Token.ModTime.IsZero() {
		t.Fatal("expected token modtime")
	}

	token, found := service.Tokens().Lookup(path)
	if !found {
		t.Fatal("expected token registry lookup to find token")
	}
	if token.ID != result.Token.ID {
		t.Fatalf("registered token id = %q, want %q", token.ID, result.Token.ID)
	}
}

func TestReadFileUsesConfiguredSandboxMountPath(t *testing.T) {
	workspace := tempWorkspace(t)
	writeWorkspaceFile(t, workspace, "file.txt", "from repo mount")
	service := newReadService(t, workspace, policy.Config{}, "run-1")
	service.SetSandboxMountPath("/repo")

	result, err := service.Read(context.Background(), files.ReadRequest{Path: "/repo/file.txt"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if result.Status != files.ReadStatusOK || result.Content != "from repo mount" {
		t.Fatalf("result = %+v, want file content through configured mount", result)
	}

	unsafe, err := service.Read(context.Background(), files.ReadRequest{Path: "/workspace/file.txt"})
	if err != nil {
		t.Fatalf("Read /workspace: %v", err)
	}
	if unsafe.Status != files.ReadStatusUnsafe {
		t.Fatalf("/workspace result = %+v, want unsafe when configured mount is /repo", unsafe)
	}
}

func TestReadMissingFileReturnsDeterministicResult(t *testing.T) {
	workspace := tempWorkspace(t)
	service := newReadService(t, workspace, policy.Config{}, "run-1")

	result, err := service.Read(context.Background(), files.ReadRequest{Path: "missing.txt"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if result.Status != files.ReadStatusNotFound {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.ReadStatusNotFound, result)
	}
	if result.HasToken {
		t.Fatal("did not expect token for missing file")
	}
}

func TestReadDirectoryReturnsDeterministicResult(t *testing.T) {
	workspace := tempWorkspace(t)
	if err := os.MkdirAll(filepath.Join(workspace, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	service := newReadService(t, workspace, policy.Config{}, "run-1")

	result, err := service.Read(context.Background(), files.ReadRequest{Path: "dir"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if result.Status != files.ReadStatusDirectory {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.ReadStatusDirectory, result)
	}
	if result.HasToken {
		t.Fatal("did not expect token for directory read")
	}
}

func TestReadOutsideWorkspaceIsRejected(t *testing.T) {
	workspace := tempWorkspace(t)
	outside := writeFile(t, t.TempDir(), "outside.txt", "outside")
	service := newReadService(t, workspace, policy.Config{}, "run-1")

	result, err := service.Read(context.Background(), files.ReadRequest{Path: outside})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if result.Status != files.ReadStatusUnsafe {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.ReadStatusUnsafe, result)
	}
	if result.HasToken {
		t.Fatal("did not expect token for unsafe read")
	}
}

func TestReadDeniedByPolicyIsSurfaced(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "secret.txt", "secret")
	service := newReadService(t, workspace, policy.Config{
		BlockedFiles: []policy.FileRule{{
			Path:  path,
			Match: policy.MatchExact,
			Modes: []policy.FileAccessMode{policy.FileAccessRead},
		}},
	}, "run-1")

	result, err := service.Read(context.Background(), files.ReadRequest{Path: "secret.txt"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if result.Status != files.ReadStatusDenied {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.ReadStatusDenied, result)
	}
	if result.Policy.Allowed {
		t.Fatal("expected policy denial")
	}
	if result.HasToken {
		t.Fatal("did not expect token for denied read")
	}
}

func TestReadLineRanges(t *testing.T) {
	workspace := tempWorkspace(t)
	writeWorkspaceFile(t, workspace, "notes.txt", "one\ntwo\nthree\nfour")
	service := newReadService(t, workspace, policy.Config{}, "run-1")

	tests := []struct {
		name      string
		startLine int
		endLine   int
		want      string
	}{
		{name: "full file", want: "one\ntwo\nthree\nfour"},
		{name: "start and end", startLine: 2, endLine: 3, want: "two\nthree\n"},
		{name: "omitted start", endLine: 2, want: "one\ntwo\n"},
		{name: "omitted end", startLine: 3, want: "three\nfour"},
		{name: "end past file", startLine: 3, endLine: 99, want: "three\nfour"},
		{name: "start past file", startLine: 99, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.Read(context.Background(), files.ReadRequest{
				Path:      "notes.txt",
				StartLine: tt.startLine,
				EndLine:   tt.endLine,
			})
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if result.Status != files.ReadStatusOK {
				t.Fatalf("status = %q, want %q", result.Status, files.ReadStatusOK)
			}
			if result.Content != tt.want {
				t.Fatalf("content = %q, want %q", result.Content, tt.want)
			}
			if result.TotalLines != 4 {
				t.Fatalf("total lines = %d, want 4", result.TotalLines)
			}
		})
	}
}

func TestReadInvalidLineRangeIsRejected(t *testing.T) {
	workspace := tempWorkspace(t)
	writeWorkspaceFile(t, workspace, "notes.txt", "one\n")
	service := newReadService(t, workspace, policy.Config{}, "run-1")

	_, err := service.Read(context.Background(), files.ReadRequest{
		Path:      "notes.txt",
		StartLine: 3,
		EndLine:   2,
	})
	if !errors.Is(err, files.ErrInvalidLineRange) {
		t.Fatalf("expected ErrInvalidLineRange, got %v", err)
	}
}

func TestReadRelativeToCurrentWorkingDir(t *testing.T) {
	workspace := tempWorkspace(t)
	nested := filepath.Join(workspace, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	writeFile(t, nested, "notes.txt", "nested")
	service := newReadService(t, workspace, policy.Config{}, "run-1")

	result, err := service.Read(context.Background(), files.ReadRequest{
		Path:              "notes.txt",
		CurrentWorkingDir: nested,
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if result.Status != files.ReadStatusOK {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.ReadStatusOK, result)
	}
	if result.Content != "nested" {
		t.Fatalf("content = %q, want nested", result.Content)
	}
}

func TestReadTokensValidateCurrentFileState(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "first")
	service := newReadService(t, workspace, policy.Config{}, "run-1")

	first, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"})
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}

	second, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"})
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if second.Token.ID != first.Token.ID {
		t.Fatalf("unchanged token id = %q, want %q", second.Token.ID, first.Token.ID)
	}

	validation, err := service.Tokens().ValidateCurrent(path)
	if err != nil {
		t.Fatalf("ValidateCurrent unchanged: %v", err)
	}
	if !validation.Found || !validation.Matches {
		t.Fatalf("validation = %+v, want found and matching", validation)
	}

	writeWorkspaceFile(t, workspace, "notes.txt", "second")
	validation, err = service.Tokens().ValidateCurrent(path)
	if err != nil {
		t.Fatalf("ValidateCurrent changed: %v", err)
	}
	if !validation.Found || validation.Matches {
		t.Fatalf("validation = %+v, want found and not matching", validation)
	}
}

func TestReadTokensAreScopedInMemoryOnly(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "hello")
	firstService := newReadService(t, workspace, policy.Config{}, "run-1")

	if _, err := firstService.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, found := firstService.Tokens().Lookup(path); !found {
		t.Fatal("expected first service token")
	}

	secondService := newReadService(t, workspace, policy.Config{}, "run-1")
	if _, found := secondService.Tokens().Lookup(path); found {
		t.Fatal("did not expect token to persist into a new service")
	}
}

func TestWriteCreateNewFileSucceedsAndRefreshesToken(t *testing.T) {
	workspace := tempWorkspace(t)
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "nested/new.txt",
		Content:   "created",
		Operation: files.WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if result.Status != files.WriteStatusCreated {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusCreated, result)
	}
	if !result.Created || result.Overwritten {
		t.Fatalf("created/overwritten flags = %v/%v, want true/false", result.Created, result.Overwritten)
	}
	if result.BackupCreated || result.BackupPath != "" {
		t.Fatalf("unexpected backup for create: %+v", result)
	}
	if result.BytesWritten != len("created") {
		t.Fatalf("bytes written = %d, want %d", result.BytesWritten, len("created"))
	}
	assertFileContent(t, result.ResolvedPath, "created")
	assertFileMode(t, result.ResolvedPath, 0o644)
	assertInside(t, layout.BackupRoot, service.BackupRoot())

	token, found := service.Tokens().Lookup(result.ResolvedPath)
	if !found {
		t.Fatal("expected refreshed token after create")
	}
	if token.ContentHash != sha256Hex("created") {
		t.Fatalf("token hash = %q, want %q", token.ContentHash, sha256Hex("created"))
	}
}

func TestWriteCreateOutsideWorkspaceRejected(t *testing.T) {
	workspace := tempWorkspace(t)
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	outside := filepath.Join(t.TempDir(), "outside.txt")

	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      outside,
		Content:   "nope",
		Operation: files.WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if result.Status != files.WriteStatusUnsafe {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusUnsafe, result)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat errored unexpectedly: %v", err)
	}
}

func TestWriteCreateDeniedByPolicyLeavesNoFileOrBackup(t *testing.T) {
	workspace := tempWorkspace(t)
	target := filepath.Join(realPath(t, workspace), "denied.txt")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{
		BlockedFiles: []policy.FileRule{{
			Path:  target,
			Match: policy.MatchExact,
			Modes: []policy.FileAccessMode{policy.FileAccessCreate},
		}},
	}, "run-1")

	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "denied.txt",
		Content:   "denied",
		Operation: files.WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if result.Status != files.WriteStatusDenied {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusDenied, result)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target exists or stat errored unexpectedly: %v", err)
	}
	assertDirEmpty(t, layout.BackupRoot)
}

func TestWriteCreateSecretRejectedLeavesNoFile(t *testing.T) {
	workspace := tempWorkspace(t)
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "secret.txt",
		Content:   "-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----\n",
		Operation: files.WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if result.Status != files.WriteStatusSecretRejected {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusSecretRejected, result)
	}
	if _, err := os.Stat(filepath.Join(realPath(t, workspace), "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("secret file exists or stat errored unexpectedly: %v", err)
	}
	assertDirEmpty(t, layout.BackupRoot)
}

func TestWriteOverwriteExistingFileRequiresReadAndCreatesBackup(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "original")
	if err := os.Chmod(path, 0o750); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "notes.txt",
		Content:   "replacement",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if result.Status != files.WriteStatusOverwritten {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusOverwritten, result)
	}
	if !result.Overwritten || result.Created {
		t.Fatalf("created/overwritten flags = %v/%v, want false/true", result.Created, result.Overwritten)
	}
	if !result.BackupCreated || result.BackupPath == "" {
		t.Fatalf("expected backup path in result: %+v", result)
	}
	assertFileContent(t, result.BackupPath, "original")
	assertFileContent(t, path, "replacement")
	assertFileMode(t, path, 0o750)

	token, found := service.Tokens().Lookup(path)
	if !found {
		t.Fatal("expected refreshed token after overwrite")
	}
	if token.ContentHash != sha256Hex("replacement") {
		t.Fatalf("token hash = %q, want %q", token.ContentHash, sha256Hex("replacement"))
	}
	validation, err := service.Tokens().ValidateCurrent(path)
	if err != nil {
		t.Fatalf("ValidateCurrent: %v", err)
	}
	if !validation.Matches {
		t.Fatalf("validation after write = %+v, want matching", validation)
	}
}

func TestWriteOverwriteWithoutReadTokenRejected(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "original")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "notes.txt",
		Content:   "replacement",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if result.Status != files.WriteStatusMissingReadToken {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusMissingReadToken, result)
	}
	assertFileContent(t, path, "original")
	assertDirEmpty(t, layout.BackupRoot)
}

func TestWriteOverwriteWithStaleReadTokenRejected(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "original")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := os.WriteFile(path, []byte("external"), 0o600); err != nil {
		t.Fatalf("external write: %v", err)
	}

	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "notes.txt",
		Content:   "replacement",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if result.Status != files.WriteStatusStaleReadToken {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusStaleReadToken, result)
	}
	assertFileContent(t, path, "external")
	assertDirEmpty(t, layout.BackupRoot)
}

func TestWriteOverwriteRejectsChangedModTimeEvenWhenContentHashMatches(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "original")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := os.Chtimes(path, testTimePlusOneHour(), testTimePlusOneHour()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "notes.txt",
		Content:   "replacement",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if result.Status != files.WriteStatusStaleReadToken {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusStaleReadToken, result)
	}
	assertFileContent(t, path, "original")
}

func TestWriteOverwriteSecretRejectedBeforeBackupOrCommit(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "original")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}

	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "notes.txt",
		Content:   "token sk-123456789012345678901234",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if result.Status != files.WriteStatusSecretRejected {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusSecretRejected, result)
	}
	assertFileContent(t, path, "original")
	assertDirEmpty(t, layout.BackupRoot)
}

func TestWriteInvalidOperationReturnsStructuredResult(t *testing.T) {
	workspace := tempWorkspace(t)
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "notes.txt",
		Content:   "content",
		Operation: files.WriteOperation("bogus"),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.Status != files.WriteStatusInvalidOperation {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusInvalidOperation, result)
	}
}

func TestWriteOverwriteDeniedBeforeBackup(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "original")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{
		BlockedFiles: []policy.FileRule{{
			Path:  path,
			Match: policy.MatchExact,
			Modes: []policy.FileAccessMode{policy.FileAccessWrite},
		}},
	}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}

	result, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "notes.txt",
		Content:   "replacement",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if result.Status != files.WriteStatusDenied {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.WriteStatusDenied, result)
	}
	assertFileContent(t, path, "original")
	assertDirEmpty(t, layout.BackupRoot)
}

func newReadService(t *testing.T, workspace string, config policy.Config, scopeID string) *files.Service {
	t.Helper()

	engine := policy.NewEngine(config, allowAllApprovalHandler{})
	evaluator := runtimepolicy.NewEvaluator(engine)
	service, err := files.NewService(workspace, evaluator, scopeID)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func newFileServiceForLayout(t *testing.T, workspace string, config policy.Config, scopeID string) (*files.Service, state.Layout) {
	t.Helper()

	layout, err := state.ResolveLayout(workspace, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	engine := policy.NewEngine(config, allowAllApprovalHandler{})
	evaluator := runtimepolicy.NewEvaluator(engine)
	service, err := files.NewServiceForLayout(layout, evaluator, scopeID)
	if err != nil {
		t.Fatalf("NewServiceForLayout: %v", err)
	}
	return service, layout
}

type allowAllApprovalHandler struct{}

func (allowAllApprovalHandler) ApproveFile(context.Context, policy.FileRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeOnce, nil
}

func (allowAllApprovalHandler) ApproveShell(context.Context, policy.ShellRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeOnce, nil
}

func (allowAllApprovalHandler) ApproveNetwork(context.Context, policy.NetworkRequest) (policy.ApprovalScope, error) {
	return policy.ApprovalScopeOnce, nil
}

func readManagedFile(t *testing.T, service *files.Service, path string) files.ReadResult {
	t.Helper()

	result, err := service.Read(context.Background(), files.ReadRequest{Path: path})
	if err != nil {
		t.Fatalf("Read %q: %v", path, err)
	}
	if result.Status != files.ReadStatusOK {
		t.Fatalf("Read %q status = %q, want %q; result=%+v", path, result.Status, files.ReadStatusOK, result)
	}
	if !result.HasToken {
		t.Fatalf("Read %q did not issue token; result=%+v", path, result)
	}
	return result
}

func tempWorkspace(t *testing.T) string {
	t.Helper()

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	return workspace
}

func writeWorkspaceFile(t *testing.T, workspace, name, content string) string {
	t.Helper()
	return writeFile(t, workspace, name, content)
}

func writeFile(t *testing.T, root, name, content string) string {
	t.Helper()

	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	return filepath.Clean(resolved)
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("content of %q = %q, want %q", path, string(data), want)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if stat.Mode().Perm() != want {
		t.Fatalf("mode of %q = %#o, want %#o", path, stat.Mode().Perm(), want)
	}
}

func assertDirEmpty(t *testing.T, path string) {
	t.Helper()

	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read dir %q: %v", path, err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected %q to be empty, found %d entries", path, len(entries))
	}
}

func assertInside(t *testing.T, root, path string) {
	t.Helper()

	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("rel %q %q: %v", root, path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("path %q is not inside %q", path, root)
	}
}

func realPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	return filepath.Clean(resolved)
}

func testTimePlusOneHour() time.Time {
	return time.Now().Add(time.Hour).UTC()
}

func sha256Hex(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}
