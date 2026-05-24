package runtimefiles_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/smasonuk/falken-core/internal/files"
	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
	"github.com/smasonuk/falken-core/internal/state"
)

func TestReadFileFacadeCreatesTokenAndSupportsLineRanges(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "one\ntwo\nthree\n")
	ops, service, _ := newOperations(t, workspace, policy.Config{})

	result, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{
		Path:      "notes.txt",
		StartLine: 2,
		EndLine:   2,
	})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if !result.Success {
		t.Fatalf("success = false, result=%+v", result)
	}
	if result.Operation != runtimefiles.OperationReadFile {
		t.Fatalf("operation = %q, want %q", result.Operation, runtimefiles.OperationReadFile)
	}
	if result.Status != string(files.ReadStatusOK) {
		t.Fatalf("status = %q, want %q", result.Status, files.ReadStatusOK)
	}
	if result.Content != "two\n" || result.BytesRead != len("two\n") || result.TotalLines != 3 {
		t.Fatalf("content/bytes/lines = %q/%d/%d", result.Content, result.BytesRead, result.TotalLines)
	}
	if result.ResolvedPath != path {
		t.Fatalf("resolved path = %q, want %q", result.ResolvedPath, path)
	}
	if !result.HasToken {
		t.Fatal("expected read token in facade result")
	}
	if _, found := service.Tokens().Lookup(path); !found {
		t.Fatal("expected managed token registry to record read")
	}

	batch, err := ops.ReadFiles(context.Background(), runtimefiles.ReadFilesRequest{
		Files: []runtimefiles.ReadFileRequest{
			{Path: "notes.txt"},
			{Path: "missing.txt"},
		},
	})
	if err != nil {
		t.Fatalf("ReadFiles: %v", err)
	}
	if batch.Success || batch.Failed != 1 || batch.Total != 2 {
		t.Fatalf("batch success/failed/total = %v/%d/%d, want false/1/2", batch.Success, batch.Failed, batch.Total)
	}
	if batch.Status != "partial" {
		t.Fatalf("batch status = %q, want partial", batch.Status)
	}
	if batch.Files[1].Status != string(files.ReadStatusNotFound) {
		t.Fatalf("missing status = %q, want %q", batch.Files[1].Status, files.ReadStatusNotFound)
	}
}

func TestReadFileFacadeDeniedAndOutsidePathsFailDeterministically(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "secret.txt", "secret")
	outside := writeFile(t, t.TempDir(), "outside.txt", "outside")
	ops, _, _ := newOperations(t, workspace, policy.Config{
		BlockedFiles: []policy.FileRule{{
			Path:  path,
			Match: policy.MatchExact,
			Modes: []policy.FileAccessMode{policy.FileAccessRead},
		}},
	})

	denied, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "secret.txt"})
	if err != nil {
		t.Fatalf("ReadFile denied: %v", err)
	}
	if denied.Success || denied.Status != string(files.ReadStatusDenied) {
		t.Fatalf("denied result = %+v, want denied failure", denied)
	}

	unsafe, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: outside})
	if err != nil {
		t.Fatalf("ReadFile outside: %v", err)
	}
	if unsafe.Success || unsafe.Status != string(files.ReadStatusUnsafe) {
		t.Fatalf("unsafe result = %+v, want unsafe failure", unsafe)
	}
}

func TestSearchFacadeDelegatesGlobAndGrepWithoutReadTokens(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "internal/tool.go", "package internal\n\nfunc NewTool() {}\n")
	ops, service, _ := newOperations(t, workspace, policy.Config{})

	glob, err := ops.Glob(context.Background(), runtimefiles.GlobRequest{Pattern: "**/*.go"})
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if !glob.Success || glob.Operation != runtimefiles.OperationGlob || len(glob.Matches) != 1 || glob.Matches[0] != "internal/tool.go" {
		t.Fatalf("glob result = %+v, want internal/tool.go", glob)
	}

	grep, err := ops.Grep(context.Background(), runtimefiles.GrepRequest{Regex: "NewTool", TargetPaths: []string{"internal"}, Glob: "**/*.go"})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if !grep.Success || grep.Operation != runtimefiles.OperationGrep || len(grep.Matches) != 1 || grep.Matches[0].Path != "internal/tool.go" {
		t.Fatalf("grep result = %+v, want internal/tool.go match", grep)
	}
	if _, found := service.Tokens().Lookup(path); found {
		t.Fatal("search facade issued read token")
	}
}

func TestWriteFileFacadePreservesManagedMutationInvariants(t *testing.T) {
	workspace := tempWorkspace(t)
	existing := writeWorkspaceFile(t, workspace, "existing.txt", "original")
	ops, service, layout := newOperations(t, workspace, policy.Config{})

	created, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "nested/new.txt",
		Content:   "created",
		Operation: files.WriteOperationCreate,
		Mode:      "0640",
	})
	if err != nil {
		t.Fatalf("WriteFile create: %v", err)
	}
	if !created.Success || !created.Created || created.BytesWritten != len("created") {
		t.Fatalf("created result = %+v, want successful create", created)
	}
	createdPath := filepath.Join(realPath(t, workspace), "nested", "new.txt")
	assertFileContent(t, createdPath, "created")
	stat, err := os.Stat(createdPath)
	if err != nil {
		t.Fatalf("stat created file: %v", err)
	}
	if stat.Mode().Perm() != 0o640 {
		t.Fatalf("created mode = %#o, want 0640", stat.Mode().Perm())
	}

	invalidMode, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "nested/bad.txt",
		Content:   "bad",
		Operation: files.WriteOperationCreate,
		Mode:      "1000",
	})
	if err != nil || invalidMode.Success || invalidMode.Status != "invalid_arguments" {
		t.Fatalf("WriteFile invalid mode = %+v/%v, want structured invalid_arguments", invalidMode, err)
	}

	missingToken, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "existing.txt",
		Content:   "replacement",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("WriteFile overwrite without read: %v", err)
	}
	if missingToken.Success || missingToken.Status != string(files.WriteStatusMissingReadToken) {
		t.Fatalf("missing token result = %+v, want missing read token", missingToken)
	}
	assertFileContent(t, existing, "original")

	if _, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "existing.txt"}); err != nil {
		t.Fatalf("ReadFile before overwrite: %v", err)
	}
	overwritten, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "existing.txt",
		Content:   "replacement",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("WriteFile overwrite: %v", err)
	}
	if !overwritten.Success || !overwritten.Overwritten || len(overwritten.BackupPaths) != 1 {
		t.Fatalf("overwrite result = %+v, want successful overwrite with backup", overwritten)
	}
	assertFileContent(t, overwritten.BackupPaths[0], "original")
	assertFileContent(t, existing, "replacement")
	token, found := service.Tokens().Lookup(existing)
	if !found || token.ContentHash != sha256Hex("replacement") {
		t.Fatalf("refreshed token found/hash = %v/%q, want updated token", found, token.ContentHash)
	}

	secret, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "secret.txt",
		Content:   "-----BEGIN PRIVATE KEY-----\nsecret\n",
		Operation: files.WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("WriteFile secret: %v", err)
	}
	if secret.Success || secret.Status != string(files.WriteStatusSecretRejected) {
		t.Fatalf("secret result = %+v, want secret rejection", secret)
	}
	if _, err := os.Stat(filepath.Join(realPath(t, workspace), "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("secret file exists or stat errored unexpectedly: %v", err)
	}
	if countRegularFiles(t, layout.BackupRoot) != 1 {
		t.Fatalf("backup count = %d, want only overwrite backup", countRegularFiles(t, layout.BackupRoot))
	}
}

func TestEditFacadePreservesManagedEditBehavior(t *testing.T) {
	workspace := tempWorkspace(t)
	editPath := writeWorkspaceFile(t, workspace, "edit.txt", "alpha beta beta")
	ambiguousPath := writeWorkspaceFile(t, workspace, "ambiguous.txt", "x x")
	stalePath := writeWorkspaceFile(t, workspace, "stale.txt", "old target")
	ops, _, _ := newOperations(t, workspace, policy.Config{})

	if _, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "edit.txt"}); err != nil {
		t.Fatalf("ReadFile edit: %v", err)
	}
	edit, err := ops.EditFile(context.Background(), runtimefiles.EditFileRequest{
		Path: "edit.txt",
		Old:  "alpha",
		New:  "ALPHA",
	})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if !edit.Success || !edit.Changed || edit.Replacements != 1 || len(edit.BackupPaths) != 1 {
		t.Fatalf("edit result = %+v, want changed edit with backup", edit)
	}
	assertFileContent(t, editPath, "ALPHA beta beta")

	multi, err := ops.MultiEdit(context.Background(), runtimefiles.MultiEditRequest{
		Edits: []runtimefiles.EditFileRequest{
			{Path: "edit.txt", Old: "ALPHA", New: "A"},
			{Path: "edit.txt", Old: "beta", New: "b", ReplaceAll: true},
		},
	})
	if err != nil {
		t.Fatalf("MultiEdit: %v", err)
	}
	if !multi.Success || multi.FilesChanged != 1 || multi.TotalReplacements != 3 || len(multi.BackupPaths) != 1 {
		t.Fatalf("multi result = %+v, want successful grouped edit", multi)
	}
	assertFileContent(t, editPath, "A b b")

	if _, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "ambiguous.txt"}); err != nil {
		t.Fatalf("ReadFile ambiguous: %v", err)
	}
	ambiguous, err := ops.EditFile(context.Background(), runtimefiles.EditFileRequest{
		Path: "ambiguous.txt",
		Old:  "x",
		New:  "y",
	})
	if err != nil {
		t.Fatalf("EditFile ambiguous: %v", err)
	}
	if ambiguous.Success || ambiguous.Status != string(files.EditStatusAmbiguousMatch) {
		t.Fatalf("ambiguous result = %+v, want ambiguous match failure", ambiguous)
	}
	assertFileContent(t, ambiguousPath, "x x")

	if _, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "stale.txt"}); err != nil {
		t.Fatalf("ReadFile stale: %v", err)
	}
	if err := os.WriteFile(stalePath, []byte("external target"), 0o600); err != nil {
		t.Fatalf("external stale write: %v", err)
	}
	stale, err := ops.EditFile(context.Background(), runtimefiles.EditFileRequest{
		Path: "stale.txt",
		Old:  "target",
		New:  "replacement",
	})
	if err != nil {
		t.Fatalf("EditFile stale: %v", err)
	}
	if stale.Success || stale.Status != string(files.EditStatusStaleReadToken) {
		t.Fatalf("stale result = %+v, want stale read token failure", stale)
	}
}

func TestPatchFacadeSurfacesChangedFilesAndPreservesAtomicFailure(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "one\ntwo\n")
	ops, _, layout := newOperations(t, workspace, policy.Config{})
	if _, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("ReadFile before patch: %v", err)
	}

	applied, err := ops.ApplyPatch(context.Background(), runtimefiles.ApplyPatchRequest{Patch: `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1,2 +1,2 @@
 one
-two
+TWO
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if !applied.Success || len(applied.Modified) != 1 || len(applied.BackupPaths) != 1 {
		t.Fatalf("applied result = %+v, want modified file with backup", applied)
	}
	if applied.Modified[0].ResolvedPath != path {
		t.Fatalf("modified resolved path = %q, want %q", applied.Modified[0].ResolvedPath, path)
	}
	assertFileContent(t, path, "one\nTWO\n")

	failed, err := ops.ApplyPatch(context.Background(), runtimefiles.ApplyPatchRequest{Patch: `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1,2 +1,2 @@
-missing
+MISSING
 TWO
`})
	if err != nil {
		t.Fatalf("ApplyPatch failed: %v", err)
	}
	if failed.Success || failed.Status != string(files.PatchStatusFailed) {
		t.Fatalf("failed result = %+v, want patch failure", failed)
	}
	assertFileContent(t, path, "one\nTWO\n")
	if backups := countRegularFiles(t, layout.BackupRoot); backups != 1 {
		t.Fatalf("backup count = %d, want unchanged after failed patch", backups)
	}
}

func TestDeleteFacadeDelegatesToManagedDelete(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "delete.txt", "remove me")
	ops, _, _ := newOperations(t, workspace, policy.Config{})

	if _, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "delete.txt"}); err != nil {
		t.Fatalf("ReadFile before delete: %v", err)
	}
	deleted, err := ops.DeleteFile(context.Background(), runtimefiles.DeleteFileRequest{Path: "delete.txt"})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if !deleted.Success || !deleted.Deleted || len(deleted.BackupPaths) != 1 {
		t.Fatalf("delete result = %+v, want successful delete with backup", deleted)
	}
	assertFileContent(t, deleted.BackupPaths[0], "remove me")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("deleted file exists or stat errored unexpectedly: %v", err)
	}
}

func newOperations(t *testing.T, workspace string, config policy.Config) (*runtimefiles.Operations, *files.Service, state.Layout) {
	t.Helper()

	layout, err := state.ResolveLayout(realPath(t, workspace), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	engine := policy.NewEngine(config, allowAllApprovalHandler{})
	evaluator := runtimepolicy.NewEvaluator(engine)
	service, err := files.NewServiceForLayout(layout, evaluator, "run-1")
	if err != nil {
		t.Fatalf("NewServiceForLayout: %v", err)
	}
	ops, err := runtimefiles.NewOperations(service)
	if err != nil {
		t.Fatalf("NewOperations: %v", err)
	}
	return ops, service, layout
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
	return realPath(t, path)
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

func realPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	return filepath.Clean(resolved)
}

func sha256Hex(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

func countRegularFiles(t *testing.T, root string) int {
	t.Helper()

	count := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			count++
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("walk %q: %v", root, err)
	}
	return count
}
