package files_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/files"
	"github.com/smasonuk/falken-core/internal/policy"
)

func TestFileSafetyInvariant_MutationsRejectSymlinkEscapes(t *testing.T) {
	workspace := tempWorkspace(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "outside-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	writeResult, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "outside-link/created.txt",
		Content:   "escape",
		Operation: files.WriteOperationCreate,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if writeResult.Status != files.WriteStatusUnsafe {
		t.Fatalf("write status = %q, want %q; result=%+v", writeResult.Status, files.WriteStatusUnsafe, writeResult)
	}

	patchResult, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/outside-link/patched.txt b/outside-link/patched.txt
new file mode 100644
--- /dev/null
+++ b/outside-link/patched.txt
@@ -0,0 +1 @@
+escape
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if patchResult.Status != files.PatchStatusFailed {
		t.Fatalf("patch status = %q, want %q; result=%+v", patchResult.Status, files.PatchStatusFailed, patchResult)
	}
	if !strings.Contains(patchResult.Error, "path escapes workspace") {
		t.Fatalf("patch error = %q, want workspace escape", patchResult.Error)
	}

	if _, err := os.Stat(filepath.Join(outside, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside write target exists or stat errored unexpectedly: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "patched.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside patch target exists or stat errored unexpectedly: %v", err)
	}
	assertDirEmpty(t, layout.BackupRoot)
}

func TestFileSafetyInvariant_EditPolicyDeniedBeforeBackupOrCommit(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "original target")
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
	result, err := service.Edit(context.Background(), files.EditRequest{
		Path: "notes.txt",
		Old:  "target",
		New:  "replacement",
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if result.Status != files.EditStatusMutationRejected {
		t.Fatalf("edit status = %q, want %q; result=%+v", result.Status, files.EditStatusMutationRejected, result)
	}
	if result.Write.Status != files.WriteStatusDenied {
		t.Fatalf("write status = %q, want %q; write=%+v", result.Write.Status, files.WriteStatusDenied, result.Write)
	}
	assertFileContent(t, path, "original target")
	assertDirEmpty(t, layout.BackupRoot)
}

func TestManagedDelete_HardeningSemantics(t *testing.T) {
	t.Run("without read token", func(t *testing.T) {
		workspace := tempWorkspace(t)
		path := writeWorkspaceFile(t, workspace, "delete.txt", "original")
		service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

		result, err := service.Delete(context.Background(), files.DeleteRequest{Path: "delete.txt"})
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if result.Status != files.DeleteStatusMissingReadToken {
			t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.DeleteStatusMissingReadToken, result)
		}
		assertFileContent(t, path, "original")
		assertDirEmpty(t, layout.BackupRoot)
	})

	t.Run("stale token", func(t *testing.T) {
		workspace := tempWorkspace(t)
		path := writeWorkspaceFile(t, workspace, "delete.txt", "original")
		service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

		if _, err := service.Read(context.Background(), files.ReadRequest{Path: "delete.txt"}); err != nil {
			t.Fatalf("Read: %v", err)
		}
		if err := os.WriteFile(path, []byte("external"), 0o600); err != nil {
			t.Fatalf("external write: %v", err)
		}

		result, err := service.Delete(context.Background(), files.DeleteRequest{Path: "delete.txt"})
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if result.Status != files.DeleteStatusStaleReadToken {
			t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.DeleteStatusStaleReadToken, result)
		}
		assertFileContent(t, path, "external")
		assertDirEmpty(t, layout.BackupRoot)
	})

	t.Run("policy denied", func(t *testing.T) {
		workspace := tempWorkspace(t)
		path := writeWorkspaceFile(t, workspace, "delete.txt", "original")
		service, layout := newFileServiceForLayout(t, workspace, policy.Config{
			BlockedFiles: []policy.FileRule{{
				Path:  path,
				Match: policy.MatchExact,
				Modes: []policy.FileAccessMode{policy.FileAccessWrite},
			}},
		}, "run-1")

		if _, err := service.Read(context.Background(), files.ReadRequest{Path: "delete.txt"}); err != nil {
			t.Fatalf("Read: %v", err)
		}

		result, err := service.Delete(context.Background(), files.DeleteRequest{Path: "delete.txt"})
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if result.Status != files.DeleteStatusDenied {
			t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.DeleteStatusDenied, result)
		}
		assertFileContent(t, path, "original")
		assertDirEmpty(t, layout.BackupRoot)
	})

	t.Run("success backs up and forgets token", func(t *testing.T) {
		workspace := tempWorkspace(t)
		path := writeWorkspaceFile(t, workspace, "delete.txt", "original")
		service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

		if _, err := service.Read(context.Background(), files.ReadRequest{Path: "delete.txt"}); err != nil {
			t.Fatalf("Read: %v", err)
		}

		result, err := service.Delete(context.Background(), files.DeleteRequest{Path: "delete.txt"})
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if result.Status != files.DeleteStatusDeleted || !result.BackupCreated || result.BackupPath == "" {
			t.Fatalf("result = %+v, want successful delete with backup", result)
		}
		assertFileContent(t, result.BackupPath, "original")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("deleted file exists or stat errored unexpectedly: %v", err)
		}
		if _, found := service.Tokens().Lookup(path); found {
			t.Fatal("did not expect token to remain after delete")
		}
	})
}

func TestManagedPatch_PolicyDeniedLeavesAllFilesUnchanged(t *testing.T) {
	workspace := tempWorkspace(t)
	first := writeWorkspaceFile(t, workspace, "first.txt", "first\n")
	second := writeWorkspaceFile(t, workspace, "second.txt", "second\n")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{
		BlockedFiles: []policy.FileRule{{
			Path:  second,
			Match: policy.MatchExact,
			Modes: []policy.FileAccessMode{policy.FileAccessWrite},
		}},
	}, "run-1")
	readManagedFile(t, service, "first.txt")
	readManagedFile(t, service, "second.txt")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/first.txt b/first.txt
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

	if result.Status != files.PatchStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusFailed, result)
	}
	assertFileContent(t, first, "first\n")
	assertFileContent(t, second, "second\n")
	assertDirEmpty(t, layout.BackupRoot)
}

func TestManagedPatch_DuplicateFileEntriesRejectedBeforeCommit(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "one\ntwo\n")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	readManagedFile(t, service, "notes.txt")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1,2 +1,2 @@
-one
+ONE
 two
diff --git a/notes.txt b/notes.txt
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

	if result.Status != files.PatchStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusFailed, result)
	}
	if !strings.Contains(result.Error, "multiple file entries") {
		t.Fatalf("error = %q, want duplicate-file rejection", result.Error)
	}
	assertFileContent(t, path, "one\ntwo\n")
	assertDirEmpty(t, layout.BackupRoot)
}
