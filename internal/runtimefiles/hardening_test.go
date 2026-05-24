package runtimefiles_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smasonuk/falken-core/internal/files"
	"github.com/smasonuk/falken-core/internal/policy"
	"github.com/smasonuk/falken-core/internal/runtimefiles"
)

func TestFileFacade_ResultShapeConsistency(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "one\ntwo\n")
	ops, _, _ := newOperations(t, workspace, policy.Config{})

	read, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "notes.txt"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if read.Operation != runtimefiles.OperationReadFile || !read.Success || read.Status != string(files.ReadStatusOK) || !read.HasToken {
		t.Fatalf("read result = %+v, want successful read result with token", read)
	}

	write, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
		Path:      "notes.txt",
		Content:   "changed\n",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if write.Operation != runtimefiles.OperationWriteFile || !write.Success || !write.Overwritten || len(write.BackupPaths) != 1 {
		t.Fatalf("write result = %+v, want successful overwrite result with backup", write)
	}
	assertFileContent(t, write.BackupPaths[0], "one\ntwo\n")

	if _, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("ReadFile after write: %v", err)
	}
	edit, err := ops.EditFile(context.Background(), runtimefiles.EditFileRequest{
		Path: "notes.txt",
		Old:  "changed",
		New:  "edited",
	})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if edit.Operation != runtimefiles.OperationEditFile || !edit.Success || !edit.Changed || edit.Replacements != 1 || len(edit.BackupPaths) != 1 {
		t.Fatalf("edit result = %+v, want successful edit result with backup", edit)
	}

	if _, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("ReadFile before patch: %v", err)
	}
	patch, err := ops.ApplyPatch(context.Background(), runtimefiles.ApplyPatchRequest{Patch: `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1 +1 @@
-edited
+patched
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if patch.Operation != runtimefiles.OperationApplyPatch || !patch.Success || len(patch.Modified) != 1 || len(patch.BackupPaths) != 1 {
		t.Fatalf("patch result = %+v, want successful patch result with modified summary", patch)
	}
	if patch.Modified[0].ResolvedPath != path {
		t.Fatalf("patch resolved path = %q, want %q", patch.Modified[0].ResolvedPath, path)
	}

	if _, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("ReadFile before delete: %v", err)
	}
	deleted, err := ops.DeleteFile(context.Background(), runtimefiles.DeleteFileRequest{Path: "notes.txt"})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if deleted.Operation != runtimefiles.OperationDeleteFile || !deleted.Success || !deleted.Deleted || len(deleted.BackupPaths) != 1 {
		t.Fatalf("delete result = %+v, want successful delete result with backup", deleted)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("deleted file exists or stat errored unexpectedly: %v", err)
	}
}

func TestFileFacade_SafetyInvariantsCannotBeBypassed(t *testing.T) {
	t.Run("policy denied write", func(t *testing.T) {
		workspace := tempWorkspace(t)
		target := filepath.Join(realPath(t, workspace), "denied.txt")
		ops, _, layout := newOperations(t, workspace, policy.Config{
			BlockedFiles: []policy.FileRule{{
				Path:  target,
				Match: policy.MatchExact,
				Modes: []policy.FileAccessMode{policy.FileAccessCreate},
			}},
		})

		result, err := ops.WriteFile(context.Background(), runtimefiles.WriteFileRequest{
			Path:      "denied.txt",
			Content:   "denied",
			Operation: files.WriteOperationCreate,
		})
		if err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if result.Success || result.Status != string(files.WriteStatusDenied) || result.Operation != runtimefiles.OperationWriteFile {
			t.Fatalf("result = %+v, want structured denied write", result)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("denied target exists or stat errored unexpectedly: %v", err)
		}
		if countRegularFiles(t, layout.BackupRoot) != 0 {
			t.Fatalf("backup count = %d, want 0", countRegularFiles(t, layout.BackupRoot))
		}
	})

	t.Run("patch secret failure leaves all files unchanged", func(t *testing.T) {
		workspace := tempWorkspace(t)
		path := writeWorkspaceFile(t, workspace, "safe.txt", "safe\n")
		ops, _, layout := newOperations(t, workspace, policy.Config{})
		if _, err := ops.ReadFile(context.Background(), runtimefiles.ReadFileRequest{Path: "safe.txt"}); err != nil {
			t.Fatalf("ReadFile before patch: %v", err)
		}

		result, err := ops.ApplyPatch(context.Background(), runtimefiles.ApplyPatchRequest{Patch: `diff --git a/safe.txt b/safe.txt
--- a/safe.txt
+++ b/safe.txt
@@ -1 +1 @@
-safe
+changed
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
		if result.Success || result.Status != string(files.PatchStatusFailed) || result.Operation != runtimefiles.OperationApplyPatch {
			t.Fatalf("result = %+v, want structured patch failure", result)
		}
		assertFileContent(t, path, "safe\n")
		if _, err := os.Stat(filepath.Join(realPath(t, workspace), "secret.txt")); !os.IsNotExist(err) {
			t.Fatalf("secret file exists or stat errored unexpectedly: %v", err)
		}
		if countRegularFiles(t, layout.BackupRoot) != 0 {
			t.Fatalf("backup count = %d, want 0", countRegularFiles(t, layout.BackupRoot))
		}
	})
}
