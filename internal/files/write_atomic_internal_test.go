package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteWorkspaceFileNoReplaceAtomicDoesNotReplaceExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatalf("WriteFile original: %v", err)
	}

	err := writeWorkspaceFileNoReplaceAtomic(path, []byte("replacement"), 0o600)
	if !errors.Is(err, errWorkspaceFileAlreadyExists) {
		t.Fatalf("writeWorkspaceFileNoReplaceAtomic error = %v, want already exists", err)
	}
	patchRollbackAssertFile(t, path, "original")
}

func TestWriteWorkspaceFileAtomicRejectsSymlinkParentEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	err := writeWorkspaceFileAtomicMode(filepath.Join(workspace, "link", "escape.txt"), []byte("escape"), 0o600, false, workspace)
	if err == nil {
		t.Fatal("writeWorkspaceFileAtomicMode succeeded, want symlink parent rejection")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside stat = %v, want missing file", statErr)
	}
}

func TestWriteWorkspaceFileAtomicCreateUsesOpenParentHandleAfterCreateSwap(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	parent := filepath.Join(workspace, "parent")
	movedParent := filepath.Join(workspace, "parent-inside")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("Mkdir parent: %v", err)
	}
	managedWriteBeforeCreateTempHook = func(string) {
		if err := os.Rename(parent, movedParent); err != nil {
			t.Fatalf("rename parent: %v", err)
		}
		if err := os.Symlink(outside, parent); err != nil {
			t.Fatalf("symlink parent: %v", err)
		}
	}
	defer func() { managedWriteBeforeCreateTempHook = nil }()

	err := writeWorkspaceFileAtomicMode(filepath.Join(parent, "created.txt"), []byte("created"), 0o600, true, workspace)
	if err != nil {
		t.Fatalf("writeWorkspaceFileAtomicMode: %v", err)
	}
	patchRollbackAssertFile(t, filepath.Join(movedParent, "created.txt"), "created")
	if _, statErr := os.Stat(filepath.Join(outside, "created.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside stat = %v, want missing file", statErr)
	}
}

func TestWriteWorkspaceFileAtomicOverwriteUsesOpenParentHandleAfterCommitSwap(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	parent := filepath.Join(workspace, "parent")
	movedParent := filepath.Join(workspace, "parent-inside")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("Mkdir parent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parent, "target.txt"), []byte("original"), 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}
	managedWriteBeforeCommitHook = func(string) {
		if err := os.Rename(parent, movedParent); err != nil {
			t.Fatalf("rename parent: %v", err)
		}
		if err := os.Symlink(outside, parent); err != nil {
			t.Fatalf("symlink parent: %v", err)
		}
	}
	defer func() { managedWriteBeforeCommitHook = nil }()

	err := writeWorkspaceFileAtomicMode(filepath.Join(parent, "target.txt"), []byte("replacement"), 0o600, false, workspace)
	if err != nil {
		t.Fatalf("writeWorkspaceFileAtomicMode: %v", err)
	}
	patchRollbackAssertFile(t, filepath.Join(movedParent, "target.txt"), "replacement")
	if _, statErr := os.Stat(filepath.Join(outside, "target.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside stat = %v, want missing file", statErr)
	}
}

func TestWriteWorkspaceFileAtomicRequiresTrustedRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target.txt")
	err := writeWorkspaceFileAtomicMode(path, []byte("content"), 0o600, false)
	if err == nil || !strings.Contains(err.Error(), "trusted write root is required") {
		t.Fatalf("writeWorkspaceFileAtomicMode error = %v, want trusted root required", err)
	}
}

func TestManagedBackupWriteRejectsSymlinkComponent(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	target := patchRollbackWriteFile(t, workspace, "target.txt", "original")
	service, layout := newPatchRollbackService(t, workspace)
	if _, err := service.Read(context.Background(), ReadRequest{Path: "target.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}

	outside := t.TempDir()
	managedWrites := filepath.Join(layout.BackupRoot, "managed-writes")
	if err := os.Symlink(outside, managedWrites); err != nil {
		t.Fatalf("Symlink managed-writes: %v", err)
	}

	result, err := service.Write(context.Background(), WriteRequest{
		Path:      "target.txt",
		Content:   "replacement",
		Operation: WriteOperationOverwrite,
	})
	if err == nil || !strings.Contains(err.Error(), "write backup") {
		t.Fatalf("Write result = %+v error = %v, want backup write failure", result, err)
	}
	patchRollbackAssertFile(t, target, "original")
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("ReadDir outside: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("outside backup target entries = %d, want empty", len(entries))
	}
}

func TestManagedBackupReadRejectsSwappedParentSymlink(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	parent := filepath.Join(workspace, "parent")
	patchRollbackWriteFile(t, workspace, "parent/target.txt", "original")
	service, _ := newPatchRollbackService(t, workspace)
	if _, err := service.Read(context.Background(), ReadRequest{Path: "parent/target.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "target.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside target: %v", err)
	}
	movedParent := filepath.Join(workspace, "parent-moved")
	managedBackupBeforeReadHook = func(string) {
		if err := os.Rename(parent, movedParent); err != nil {
			t.Fatalf("rename parent: %v", err)
		}
		if err := os.Symlink(outside, parent); err != nil {
			t.Fatalf("symlink parent: %v", err)
		}
	}
	defer func() { managedBackupBeforeReadHook = nil }()

	result, err := service.Write(context.Background(), WriteRequest{
		Path:      "parent/target.txt",
		Content:   "replacement",
		Operation: WriteOperationOverwrite,
	})
	if err == nil || !strings.Contains(err.Error(), "read original file for backup") {
		t.Fatalf("Write result = %+v error = %v, want backup source read failure", result, err)
	}
	patchRollbackAssertFile(t, filepath.Join(movedParent, "target.txt"), "original")
	patchRollbackAssertFile(t, filepath.Join(outside, "target.txt"), "outside")
}

func TestManagedDeleteRejectsSwappedParentSymlinkBeforeUnlink(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	parent := filepath.Join(workspace, "parent")
	patchRollbackWriteFile(t, workspace, "parent/delete.txt", "original")
	service, _ := newPatchRollbackService(t, workspace)
	if _, err := service.Read(context.Background(), ReadRequest{Path: "parent/delete.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "delete.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside target: %v", err)
	}
	movedParent := filepath.Join(workspace, "parent-moved")
	managedDeleteBeforeUnlinkHook = func(string) {
		if err := os.Rename(parent, movedParent); err != nil {
			t.Fatalf("rename parent: %v", err)
		}
		if err := os.Symlink(outside, parent); err != nil {
			t.Fatalf("symlink parent: %v", err)
		}
	}
	defer func() { managedDeleteBeforeUnlinkHook = nil }()

	result, err := service.Delete(context.Background(), DeleteRequest{Path: "parent/delete.txt"})
	if err != nil || result.Status != DeleteStatusStaleReadToken {
		t.Fatalf("Delete result = %+v error = %v, want stale-token rejection", result, err)
	}
	patchRollbackAssertFile(t, filepath.Join(movedParent, "delete.txt"), "original")
	patchRollbackAssertFile(t, filepath.Join(outside, "delete.txt"), "outside")
}

func TestDeleteReportsCommitUncertainWhenParentSyncFails(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	target := patchRollbackWriteFile(t, workspace, "delete.txt", "remove me")
	service, _ := newPatchRollbackService(t, workspace)
	if _, err := service.Read(context.Background(), ReadRequest{Path: "delete.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}

	originalSync := syncManagedParentDir
	managedDeleteBeforeUnlinkHook = func(string) {
		syncManagedParentDir = func(*os.File, string) error {
			return errors.New("forced delete sync failure")
		}
	}
	defer func() {
		managedDeleteBeforeUnlinkHook = nil
		syncManagedParentDir = originalSync
	}()

	result, err := service.Delete(context.Background(), DeleteRequest{Path: "delete.txt"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if result.Status != DeleteStatusCommitUncertain || !result.Deleted || !result.MutationMayHaveOccurred {
		t.Fatalf("result = %+v, want commit_uncertain deleted mutation", result)
	}
	if !strings.Contains(result.Error, "forced delete sync failure") {
		t.Fatalf("error = %q, want sync failure", result.Error)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted file stat = %v, want missing", err)
	}
}

func TestWriteWorkspaceFileAtomicReportsMutationMayHaveOccurredAfterSyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target.txt")
	originalSync := syncManagedParentDir
	syncManagedParentDir = func(*os.File, string) error {
		return errors.New("forced sync failure")
	}
	defer func() { syncManagedParentDir = originalSync }()

	err := writeWorkspaceFileAtomicMode(path, []byte("committed"), 0o600, false, filepath.Dir(path))
	if !errors.Is(err, errMutationMayHaveOccurred) {
		t.Fatalf("writeWorkspaceFileAtomicMode error = %v, want mutation may have occurred", err)
	}
	patchRollbackAssertFile(t, path, "committed")
}

func TestWriteReportsCommitUncertainWhenAtomicCommitMayHaveSucceeded(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	service, _ := newPatchRollbackService(t, workspace)
	service.writeFileAtomic = func(path string, data []byte, perm os.FileMode, noReplace bool) error {
		if err := writeWorkspaceFileAtomicMode(path, data, perm, noReplace, filepath.Dir(path)); err != nil {
			return err
		}
		return mutationMayHaveOccurredError{operation: "test commit", err: errors.New("post-commit sync failed")}
	}

	result, err := service.Write(context.Background(), WriteRequest{
		Path:      "created.txt",
		Content:   "created",
		Operation: WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.Status != WriteStatusCommitUncertain || !result.MutationMayHaveOccurred || !result.Created {
		t.Fatalf("result = %+v, want commit_uncertain created mutation", result)
	}
	patchRollbackAssertFile(t, filepath.Join(workspace, "created.txt"), "created")
}

func TestWriteCreateRaceDoesNotOverwriteAppearingFile(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	service, _ := newPatchRollbackService(t, workspace)
	var raced bool
	service.writeFileAtomic = func(path string, data []byte, perm os.FileMode, noReplace bool) error {
		if noReplace && !raced {
			raced = true
			if err := os.WriteFile(path, []byte("competing write"), perm); err != nil {
				t.Fatalf("write competing file: %v", err)
			}
		}
		return writeWorkspaceFileAtomicMode(path, data, perm, noReplace, filepath.Dir(path))
	}

	result, err := service.Write(context.Background(), WriteRequest{
		Path:      "race.txt",
		Content:   "created by falken",
		Operation: WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.Status != WriteStatusAlreadyExists || result.Created {
		t.Fatalf("result = %+v, want already_exists without create", result)
	}
	patchRollbackAssertFile(t, filepath.Join(workspace, "race.txt"), "competing write")
}

func TestWriteCreateOrOverwriteMissingTargetRaceDoesNotOverwriteAppearingFile(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	service, _ := newPatchRollbackService(t, workspace)
	var raced bool
	service.writeFileAtomic = func(path string, data []byte, perm os.FileMode, noReplace bool) error {
		if !noReplace {
			t.Fatal("missing-target create_or_overwrite must use no-replace commit")
		}
		if !raced {
			raced = true
			if err := os.WriteFile(path, []byte("competing write"), perm); err != nil {
				t.Fatalf("write competing file: %v", err)
			}
		}
		return writeWorkspaceFileAtomicMode(path, data, perm, noReplace, filepath.Dir(path))
	}

	result, err := service.Write(context.Background(), WriteRequest{
		Path:    "race.txt",
		Content: "created by falken",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.Status != WriteStatusAlreadyExists || result.Created || result.Overwritten {
		t.Fatalf("result = %+v, want already_exists without mutation", result)
	}
	patchRollbackAssertFile(t, filepath.Join(workspace, "race.txt"), "competing write")
}

func TestWriteCreateOrOverwriteExistingTargetUsesOverwriteCommitAfterReadToken(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	path := patchRollbackWriteFile(t, workspace, "existing.txt", "original")
	service, _ := newPatchRollbackService(t, workspace)
	if _, err := service.Read(context.Background(), ReadRequest{Path: "existing.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	var sawOverwriteCommit bool
	service.writeFileAtomic = func(path string, data []byte, perm os.FileMode, noReplace bool) error {
		if noReplace {
			t.Fatal("existing-target create_or_overwrite should use overwrite commit after read-token validation")
		}
		sawOverwriteCommit = true
		return writeWorkspaceFileAtomicMode(path, data, perm, noReplace, filepath.Dir(path))
	}

	result, err := service.Write(context.Background(), WriteRequest{
		Path:    "existing.txt",
		Content: "replacement",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !sawOverwriteCommit {
		t.Fatal("expected overwrite commit path")
	}
	if result.Status != WriteStatusOverwritten || !result.Overwritten {
		t.Fatalf("result = %+v, want overwritten", result)
	}
	patchRollbackAssertFile(t, path, "replacement")
}

func TestWriteOverwriteExistingTargetUsesOverwriteCommitAfterReadToken(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	path := patchRollbackWriteFile(t, workspace, "existing.txt", "original")
	service, _ := newPatchRollbackService(t, workspace)
	if _, err := service.Read(context.Background(), ReadRequest{Path: "existing.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	service.writeFileAtomic = func(path string, data []byte, perm os.FileMode, noReplace bool) error {
		if noReplace {
			t.Fatal("explicit overwrite should use overwrite commit after read-token validation")
		}
		return writeWorkspaceFileAtomicMode(path, data, perm, noReplace, filepath.Dir(path))
	}

	result, err := service.Write(context.Background(), WriteRequest{
		Path:      "existing.txt",
		Content:   "replacement",
		Operation: WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.Status != WriteStatusOverwritten || !result.Overwritten {
		t.Fatalf("result = %+v, want overwritten", result)
	}
	patchRollbackAssertFile(t, path, "replacement")
}

func TestApplyPatchCreateRaceDoesNotOverwriteOrRemoveCompetingFile(t *testing.T) {
	workspace := patchRollbackTempWorkspace(t)
	service, _ := newPatchRollbackService(t, workspace)
	var raced bool
	service.writeFileAtomic = func(path string, data []byte, perm os.FileMode, noReplace bool) error {
		if noReplace && !raced {
			raced = true
			if err := os.WriteFile(path, []byte("competing write\n"), perm); err != nil {
				t.Fatalf("write competing file: %v", err)
			}
		}
		return writeWorkspaceFileAtomicMode(path, data, perm, noReplace, filepath.Dir(path))
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
	if result.Status != PatchStatusFailed || result.RollbackAttempted || result.Applied {
		t.Fatalf("result = %+v, want failed patch without rollback or applied state", result)
	}
	patchRollbackAssertFile(t, filepath.Join(workspace, "race.txt"), "competing write\n")
}
