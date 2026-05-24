package files

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/policy"
	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/state"
)

func TestApplyPatchMultiFileCommitFailureRollsBackModifiedFile(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	first := patchRollbackWriteFile(t, workspace, "first.txt", "first\n")
	second := patchRollbackWriteFile(t, workspace, "second.txt", "second\n")
	service, _ := newPatchRollbackService(t, workspace)
	patchRollbackRead(t, service, "first.txt")
	patchRollbackRead(t, service, "second.txt")
	service.patchCommitHook = func(file patchPlanFile) (patchCommitOutcome, bool) {
		if file.path == "second.txt" {
			return patchCommitOutcome{Result: patchRollbackFailure(file, "forced commit failure")}, true
		}
		return patchCommitOutcome{}, false
	}

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `diff --git a/first.txt b/first.txt
--- a/first.txt
+++ b/first.txt
@@ -1 +1 @@
-first
+FIRST
diff --git a/second.txt b/second.txt
--- a/second.txt
+++ b/second.txt
@@ -1 +1 @@
-second
+SECOND
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != PatchStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, PatchStatusFailed, result)
	}
	if !result.RollbackAttempted || !result.RollbackSucceeded {
		t.Fatalf("rollback = attempted %v succeeded %v; result=%+v", result.RollbackAttempted, result.RollbackSucceeded, result)
	}
	if result.Applied {
		t.Fatalf("applied = true after successful rollback; result=%+v", result)
	}
	if result.FilesRolledBack != 1 {
		t.Fatalf("files rolled back = %d, want 1; result=%+v", result.FilesRolledBack, result)
	}
	if len(result.Modified) != 0 || len(result.Created) != 0 || len(result.Deleted) != 0 || len(result.Renamed) != 0 {
		t.Fatalf("committed summaries = created:%d modified:%d deleted:%d renamed:%d, want cleared after rollback", len(result.Created), len(result.Modified), len(result.Deleted), len(result.Renamed))
	}
	if !strings.Contains(result.Error, "prior patch changes were rolled back") {
		t.Fatalf("error = %q, want rollback explanation", result.Error)
	}
	patchRollbackAssertFile(t, first, "first\n")
	patchRollbackAssertFile(t, second, "second\n")
}

func TestApplyPatchCreateAndModifyCommitFailureClearsRolledBackSummaries(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	existing := patchRollbackWriteFile(t, workspace, "existing.txt", "existing\n")
	later := patchRollbackWriteFile(t, workspace, "later.txt", "later\n")
	service, _ := newPatchRollbackService(t, workspace)
	patchRollbackRead(t, service, "existing.txt")
	patchRollbackRead(t, service, "later.txt")
	service.patchCommitHook = func(file patchPlanFile) (patchCommitOutcome, bool) {
		if file.path == "later.txt" {
			return patchCommitOutcome{Result: patchRollbackFailure(file, "forced commit failure")}, true
		}
		return patchCommitOutcome{}, false
	}

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+new
diff --git a/existing.txt b/existing.txt
--- a/existing.txt
+++ b/existing.txt
@@ -1 +1 @@
-existing
+EXISTING
diff --git a/later.txt b/later.txt
--- a/later.txt
+++ b/later.txt
@@ -1 +1 @@
-later
+LATER
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != PatchStatusFailed || result.Applied {
		t.Fatalf("result = %+v, want failed and applied=false", result)
	}
	if !result.RollbackAttempted || !result.RollbackSucceeded {
		t.Fatalf("rollback = attempted %v succeeded %v; result=%+v", result.RollbackAttempted, result.RollbackSucceeded, result)
	}
	if result.FilesRolledBack != 2 {
		t.Fatalf("files rolled back = %d, want 2; result=%+v", result.FilesRolledBack, result)
	}
	if len(result.Created) != 0 || len(result.Modified) != 0 || len(result.Deleted) != 0 || len(result.Renamed) != 0 {
		t.Fatalf("committed summaries = created:%d modified:%d deleted:%d renamed:%d, want cleared after rollback", len(result.Created), len(result.Modified), len(result.Deleted), len(result.Renamed))
	}
	if !strings.Contains(result.Error, "forced commit failure") || !strings.Contains(result.Error, "prior patch changes were rolled back") {
		t.Fatalf("error = %q, want commit failure and rollback explanation", result.Error)
	}
	patchRollbackAssertMissing(t, filepath.Join(workspace, "new.txt"))
	patchRollbackAssertFile(t, existing, "existing\n")
	patchRollbackAssertFile(t, later, "later\n")
}

func TestApplyPatchCreatedFileRemovedOnRollback(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	service, _ := newPatchRollbackService(t, workspace)
	service.patchCommitHook = func(file patchPlanFile) (patchCommitOutcome, bool) {
		if file.path == "second.txt" {
			return patchCommitOutcome{Result: patchRollbackFailure(file, "forced commit failure")}, true
		}
		return patchCommitOutcome{}, false
	}

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `diff --git a/nested/first.txt b/nested/first.txt
new file mode 100644
--- /dev/null
+++ b/nested/first.txt
@@ -0,0 +1 @@
+first
diff --git a/second.txt b/second.txt
new file mode 100644
--- /dev/null
+++ b/second.txt
@@ -0,0 +1 @@
+second
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if !result.RollbackAttempted || !result.RollbackSucceeded {
		t.Fatalf("rollback = attempted %v succeeded %v; result=%+v", result.RollbackAttempted, result.RollbackSucceeded, result)
	}
	patchRollbackAssertMissing(t, filepath.Join(workspace, "nested", "first.txt"))
	patchRollbackAssertMissing(t, filepath.Join(workspace, "nested"))
	patchRollbackAssertMissing(t, filepath.Join(workspace, "second.txt"))
}

func TestApplyPatchRollbackPreservesPreExistingEmptyDirectory(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	existingDir := filepath.Join(workspace, "existing-empty-dir")
	if err := os.Mkdir(existingDir, 0o755); err != nil {
		t.Fatalf("mkdir existing dir: %v", err)
	}
	service, _ := newPatchRollbackService(t, workspace)
	service.patchCommitHook = func(file patchPlanFile) (patchCommitOutcome, bool) {
		if file.path == "later.txt" {
			return patchCommitOutcome{Result: patchRollbackFailure(file, "forced commit failure")}, true
		}
		return patchCommitOutcome{}, false
	}

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `diff --git a/existing-empty-dir/new.txt b/existing-empty-dir/new.txt
new file mode 100644
--- /dev/null
+++ b/existing-empty-dir/new.txt
@@ -0,0 +1 @@
+new
diff --git a/later.txt b/later.txt
new file mode 100644
--- /dev/null
+++ b/later.txt
@@ -0,0 +1 @@
+later
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if !result.RollbackAttempted || !result.RollbackSucceeded {
		t.Fatalf("rollback = attempted %v succeeded %v; result=%+v", result.RollbackAttempted, result.RollbackSucceeded, result)
	}
	patchRollbackAssertMissing(t, filepath.Join(existingDir, "new.txt"))
	info, err := os.Stat(existingDir)
	if err != nil {
		t.Fatalf("stat existing dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("existing path mode = %v, want directory", info.Mode())
	}
}

func TestApplyPatchDeletedFileRestoredOnRollback(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	first := patchRollbackWriteFile(t, workspace, "first.txt", "first\n")
	second := patchRollbackWriteFile(t, workspace, "second.txt", "second\n")
	service, _ := newPatchRollbackService(t, workspace)
	patchRollbackRead(t, service, "first.txt")
	patchRollbackRead(t, service, "second.txt")
	service.patchCommitHook = func(file patchPlanFile) (patchCommitOutcome, bool) {
		if file.path == "second.txt" {
			return patchCommitOutcome{Result: patchRollbackFailure(file, "forced commit failure")}, true
		}
		return patchCommitOutcome{}, false
	}

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `diff --git a/first.txt b/first.txt
deleted file mode 100644
--- a/first.txt
+++ /dev/null
@@ -1 +0,0 @@
-first
diff --git a/second.txt b/second.txt
deleted file mode 100644
--- a/second.txt
+++ /dev/null
@@ -1 +0,0 @@
-second
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if !result.RollbackAttempted || !result.RollbackSucceeded {
		t.Fatalf("rollback = attempted %v succeeded %v; result=%+v", result.RollbackAttempted, result.RollbackSucceeded, result)
	}
	patchRollbackAssertFile(t, first, "first\n")
	patchRollbackAssertFile(t, second, "second\n")
}

func TestApplyPatchSecretFailureLeavesWorkspaceUnchanged(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	first := patchRollbackWriteFile(t, workspace, "first.txt", "first\n")
	service, _ := newPatchRollbackService(t, workspace)
	patchRollbackRead(t, service, "first.txt")

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `diff --git a/first.txt b/first.txt
--- a/first.txt
+++ b/first.txt
@@ -1 +1 @@
-first
+FIRST
diff --git a/secret.txt b/secret.txt
new file mode 100644
--- /dev/null
+++ b/secret.txt
@@ -0,0 +1 @@
+token sk-123456789012345678901234
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != PatchStatusFailed || result.RollbackAttempted {
		t.Fatalf("result = %+v, want preflight failure without rollback", result)
	}
	if !strings.Contains(result.Error, "secret") {
		t.Fatalf("error = %q, want secret rejection", result.Error)
	}
	patchRollbackAssertFile(t, first, "first\n")
	patchRollbackAssertMissing(t, filepath.Join(workspace, "secret.txt"))
}

func TestApplyPatchMalformedPatchLeavesWorkspaceUnchanged(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	first := patchRollbackWriteFile(t, workspace, "first.txt", "first\n")
	service, _ := newPatchRollbackService(t, workspace)

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `not a patch
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != PatchStatusFailed || result.RollbackAttempted {
		t.Fatalf("result = %+v, want parse failure without rollback", result)
	}
	patchRollbackAssertFile(t, first, "first\n")
}

func TestApplyPatchRollbackFailureReported(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	first := patchRollbackWriteFile(t, workspace, "first.txt", "first\n")
	second := patchRollbackWriteFile(t, workspace, "second.txt", "second\n")
	service, _ := newPatchRollbackService(t, workspace)
	patchRollbackRead(t, service, "first.txt")
	patchRollbackRead(t, service, "second.txt")
	service.patchCommitHook = func(file patchPlanFile) (patchCommitOutcome, bool) {
		if file.path == "second.txt" {
			return patchCommitOutcome{Result: patchRollbackFailure(file, "forced commit failure")}, true
		}
		return patchCommitOutcome{}, false
	}
	service.patchRollbackHook = func(entry patchRollbackEntry) error {
		if strings.HasSuffix(entry.path, "first.txt") {
			return errors.New("forced rollback failure")
		}
		return nil
	}

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `diff --git a/first.txt b/first.txt
--- a/first.txt
+++ b/first.txt
@@ -1 +1 @@
-first
+FIRST
diff --git a/second.txt b/second.txt
--- a/second.txt
+++ b/second.txt
@@ -1 +1 @@
-second
+SECOND
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if !result.RollbackAttempted || result.RollbackSucceeded {
		t.Fatalf("rollback = attempted %v succeeded %v; result=%+v", result.RollbackAttempted, result.RollbackSucceeded, result)
	}
	if !strings.Contains(result.RollbackError, "forced rollback failure") {
		t.Fatalf("rollback error = %q, want forced failure", result.RollbackError)
	}
	patchRollbackAssertFile(t, first, "FIRST\n")
	patchRollbackAssertFile(t, second, "second\n")
}

func TestApplyPatchPreMutationWriteErrorDoesNotMarkFailedFileCommitted(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	first := patchRollbackWriteFile(t, workspace, "first.txt", "first\n")
	second := patchRollbackWriteFile(t, workspace, "second.txt", "second\n")
	service, _ := newPatchRollbackService(t, workspace)
	patchRollbackRead(t, service, "first.txt")
	patchRollbackRead(t, service, "second.txt")
	service.writeFileAtomic = func(path string, data []byte, perm os.FileMode, noReplace bool) error {
		if strings.HasSuffix(path, "second.txt") {
			return errors.New("forced prewrite failure")
		}
		return writeWorkspaceFileAtomicMode(path, data, perm, noReplace, filepath.Dir(path))
	}

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `diff --git a/first.txt b/first.txt
--- a/first.txt
+++ b/first.txt
@@ -1 +1 @@
-first
+FIRST
diff --git a/second.txt b/second.txt
--- a/second.txt
+++ b/second.txt
@@ -1 +1 @@
-second
+SECOND
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if !result.RollbackAttempted || !result.RollbackSucceeded {
		t.Fatalf("rollback = attempted %v succeeded %v; result=%+v", result.RollbackAttempted, result.RollbackSucceeded, result)
	}
	if result.FilesRolledBack != 1 {
		t.Fatalf("files rolled back = %d, want only first file rolled back; result=%+v", result.FilesRolledBack, result)
	}
	patchRollbackAssertFile(t, first, "first\n")
	patchRollbackAssertFile(t, second, "second\n")
}

func TestApplyPatchCreateRaceDoesNotRemoveCompetingFile(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	service, _ := newPatchRollbackService(t, workspace)
	service.patchCommitHook = func(file patchPlanFile) (patchCommitOutcome, bool) {
		if file.path != "race.txt" {
			return patchCommitOutcome{}, false
		}
		patchRollbackWriteFile(t, workspace, "race.txt", "competing write\n")
		return patchCommitOutcome{Result: patchRollbackFailure(file, "target already exists")}, true
	}

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `diff --git a/race.txt b/race.txt
new file mode 100644
--- /dev/null
+++ b/race.txt
@@ -0,0 +1 @@
+created by patch
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.RollbackAttempted {
		t.Fatalf("rollback attempted for non-mutating create failure; result=%+v", result)
	}
	patchRollbackAssertFile(t, filepath.Join(workspace, "race.txt"), "competing write\n")
}

func TestRemoveEmptyParentsReturnsUnexpectedErrors(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	filePath := patchRollbackWriteFile(t, workspace, "not-a-dir", "content")

	err := removeEmptyParents(filePath, workspace)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("removeEmptyParents error = %v, want not-a-directory error", err)
	}
	patchRollbackAssertFile(t, filePath, "content")
}

func TestApplyPatchPostMutationFailureRollsBackCurrentFile(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	first := patchRollbackWriteFile(t, workspace, "first.txt", "first\n")
	service, _ := newPatchRollbackService(t, workspace)
	patchRollbackRead(t, service, "first.txt")
	service.patchCommitHook = func(file patchPlanFile) (patchCommitOutcome, bool) {
		if file.path != "first.txt" {
			return patchCommitOutcome{}, false
		}
		if err := os.WriteFile(file.resolvedPath, []byte("BROKEN\n"), 0o600); err != nil {
			t.Fatalf("write simulated mutation: %v", err)
		}
		return patchCommitOutcome{
			Result:                  patchRollbackFailure(file, "forced post-mutation failure"),
			MutationMayHaveOccurred: true,
			Committed:               true,
		}, true
	}

	result, err := service.ApplyPatch(context.Background(), PatchRequest{Patch: `diff --git a/first.txt b/first.txt
--- a/first.txt
+++ b/first.txt
@@ -1 +1 @@
-first
+FIRST
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if !result.RollbackAttempted || !result.RollbackSucceeded || result.Applied {
		t.Fatalf("rollback result = %+v, want successful rollback with applied=false", result)
	}
	patchRollbackAssertFile(t, first, "first\n")
}

func TestPatchRollbackRestoresOriginalBytesExactly(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	path := filepath.Join(workspace, "data.bin")
	original := []byte{0xff, 0x00, 'a', '\r', '\n', 0xfe}
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	service, _ := newPatchRollbackService(t, workspace)

	if err := service.rollbackPatchEntry(patchRollbackEntry{
		path:          path,
		workspaceRoot: workspace,
		existed:       true,
		content:       original,
		mode:          0o600,
		scopeID:       service.tokens.ScopeID(),
		committed:     true,
	}); err != nil {
		t.Fatalf("rollbackPatchEntry: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("bytes = %#v, want %#v", got, original)
	}
}

func TestPatchRollbackEntryRemovesRenameDestinationAndRestoresSource(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	oldPath := filepath.Join(workspace, "old.txt")
	newPath := filepath.Join(workspace, "new.txt")
	if err := os.WriteFile(newPath, []byte("renamed"), 0o600); err != nil {
		t.Fatalf("write rename destination: %v", err)
	}
	service, _ := newPatchRollbackService(t, workspace)

	if err := service.rollbackPatchEntry(patchRollbackEntry{
		path:          oldPath,
		oldPath:       oldPath,
		newPath:       newPath,
		workspaceRoot: workspace,
		existed:       true,
		content:       []byte("original"),
		mode:          0o600,
		scopeID:       service.tokens.ScopeID(),
		committed:     true,
	}); err != nil {
		t.Fatalf("rollbackPatchEntry: %v", err)
	}
	patchRollbackAssertFile(t, oldPath, "original")
	if _, err := os.Stat(newPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new path stat = %v, want removed", err)
	}
}

func newPatchRollbackService(t *testing.T, workspace string) (*Service, state.Layout) {
	t.Helper()

	layout, err := state.ResolveLayout(workspace, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	engine := policy.NewEngine(policy.Config{}, allowAllApprovalHandler{})
	evaluator := runtimepolicy.NewEvaluator(engine)
	service, err := NewServiceForLayout(layout, evaluator, "run-1")
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

func patchRollbackRead(t *testing.T, service *Service, path string) {
	t.Helper()

	result, err := service.Read(context.Background(), ReadRequest{Path: path})
	if err != nil {
		t.Fatalf("Read %q: %v", path, err)
	}
	if result.Status != ReadStatusOK || !result.HasToken {
		t.Fatalf("Read %q result = %+v, want ok with token", path, result)
	}
}

func patchRollbackFailure(file patchPlanFile, msg string) PatchFileResult {
	return PatchFileResult{
		Operation:    file.operation,
		Path:         file.path,
		OldPath:      file.oldPath,
		NewPath:      file.newPath,
		ResolvedPath: file.resolvedPath,
		Error:        msg,
	}
}

func patchRollbackTempWorkspace(t *testing.T) string {
	t.Helper()

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	return workspace
}

func patchRollbackWriteFile(t *testing.T, workspace, name, content string) string {
	t.Helper()

	path := filepath.Join(workspace, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	return filepath.Clean(path)
}

func patchRollbackAssertFile(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("content of %q = %q, want %q", path, string(data), want)
	}
}

func patchRollbackAssertMissing(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path %q exists or stat failed unexpectedly: %v", path, err)
	}
}
