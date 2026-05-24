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

func TestApplyPatchModifyExistingFileThroughManagedOverwrite(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "one\ntwo\nthree\n")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	readManagedFile(t, service, "notes.txt")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 one
-two
+TWO
 three
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != files.PatchStatusApplied {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusApplied, result)
	}
	if len(result.Modified) != 1 {
		t.Fatalf("modified count = %d, want 1; result=%+v", len(result.Modified), result)
	}
	modified := result.Modified[0]
	if modified.Operation != files.PatchOperationModify {
		t.Fatalf("operation = %q, want %q", modified.Operation, files.PatchOperationModify)
	}
	if !modified.BackupCreated || modified.BackupPath == "" {
		t.Fatalf("expected backup for modify: %+v", modified)
	}
	assertFileContent(t, modified.BackupPath, "one\ntwo\nthree\n")
	assertFileContent(t, path, "one\nTWO\nthree\n")

	token, found := service.Tokens().Lookup(path)
	if !found {
		t.Fatal("expected patch modify to refresh token")
	}
	if token.ContentHash != sha256Hex("one\nTWO\nthree\n") {
		t.Fatalf("token hash = %q, want updated content hash", token.ContentHash)
	}
}

func TestApplyPatchModifyWithoutPriorReadFails(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "file.txt", "hello\n")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/file.txt b/file.txt
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-hello
+goodbye
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != files.PatchStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusFailed, result)
	}
	if !strings.Contains(result.Error, "missing read token") {
		t.Fatalf("error = %q, want missing read token", result.Error)
	}
	assertFileContent(t, path, "hello\n")
	if _, found := service.Tokens().Lookup(path); found {
		t.Fatal("failed patch preflight minted a read token")
	}
}

func TestApplyPatchDeleteWithoutPriorReadFails(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "file.txt", "hello\n")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/file.txt b/file.txt
deleted file mode 100644
--- a/file.txt
+++ /dev/null
@@ -1 +0,0 @@
-hello
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != files.PatchStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusFailed, result)
	}
	if !strings.Contains(result.Error, "missing read token") {
		t.Fatalf("error = %q, want missing read token", result.Error)
	}
	assertFileContent(t, path, "hello\n")
}

func TestApplyPatchCreateNewFile(t *testing.T) {
	workspace := tempWorkspace(t)
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/nested/new.txt b/nested/new.txt
new file mode 100644
--- /dev/null
+++ b/nested/new.txt
@@ -0,0 +1,2 @@
+hello
+world
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != files.PatchStatusApplied {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusApplied, result)
	}
	if len(result.Created) != 1 {
		t.Fatalf("created count = %d, want 1; result=%+v", len(result.Created), result)
	}
	created := result.Created[0]
	if created.Write.Status != files.WriteStatusCreated {
		t.Fatalf("write status = %q, want %q; write=%+v", created.Write.Status, files.WriteStatusCreated, created.Write)
	}
	if created.BackupCreated || created.BackupPath != "" {
		t.Fatalf("did not expect backup for create: %+v", created)
	}
	assertFileContent(t, filepath.Join(realPath(t, workspace), "nested", "new.txt"), "hello\nworld\n")
}

func TestApplyPatchCreateExecutableFileMode(t *testing.T) {
	workspace := tempWorkspace(t)
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/script.sh b/script.sh
new file mode 100755
--- /dev/null
+++ b/script.sh
@@ -0,0 +1,2 @@
+#!/bin/sh
+echo ok
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if result.Status != files.PatchStatusApplied {
		t.Fatalf("result = %+v, want applied", result)
	}
	info, err := os.Stat(filepath.Join(realPath(t, workspace), "script.sh"))
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode = %04o, want 0755", got)
	}
}

func TestApplyPatchModifyContentAndMode(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "tool.sh", "echo old\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod setup: %v", err)
	}
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	readManagedFile(t, service, "tool.sh")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/tool.sh b/tool.sh
old mode 100644
new mode 100755
--- a/tool.sh
+++ b/tool.sh
@@ -1 +1 @@
-echo old
+echo new
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if result.Status != files.PatchStatusApplied {
		t.Fatalf("result = %+v, want applied", result)
	}
	assertFileContent(t, path, "echo new\n")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat modified file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode = %04o, want 0755", got)
	}
}

func TestApplyPatchRejectsModeOnlyAndUnsupportedModes(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "tool.sh", "echo ok\n")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	readManagedFile(t, service, "tool.sh")

	tests := []struct {
		name      string
		patch     string
		wantError string
	}{
		{
			name: "mode only",
			patch: `diff --git a/tool.sh b/tool.sh
old mode 100644
new mode 100755
--- a/tool.sh
+++ b/tool.sh
`,
			wantError: "mode-only patches are not supported",
		},
		{
			name: "symlink mode",
			patch: `diff --git a/link b/link
new file mode 120000
--- /dev/null
+++ b/link
@@ -0,0 +1 @@
+target
`,
			wantError: "unsupported git file mode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: tt.patch})
			if err != nil {
				t.Fatalf("ApplyPatch: %v", err)
			}
			if result.Status != files.PatchStatusFailed || !strings.Contains(result.Error, tt.wantError) {
				t.Fatalf("result = %+v, want error containing %q", result, tt.wantError)
			}
			assertFileContent(t, path, "echo ok\n")
		})
	}
}

func TestApplyPatchZeroCountInsertionAfterFirstLine(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "a\nb\n")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	readManagedFile(t, service, "notes.txt")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1,0 +2,1 @@
+inserted
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if result.Status != files.PatchStatusApplied {
		t.Fatalf("result = %+v, want applied", result)
	}
	assertFileContent(t, path, "a\ninserted\nb\n")
}

func TestApplyPatchSupportsQuotedPathsWithSpaces(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes dir/file name.txt", "old\n")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	readManagedFile(t, service, "notes dir/file name.txt")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git "a/notes dir/file name.txt" "b/notes dir/file name.txt"
--- "a/notes dir/file name.txt"
+++ "b/notes dir/file name.txt"
@@ -1 +1 @@
-old
+new
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if result.Status != files.PatchStatusApplied {
		t.Fatalf("status = %q, want applied; result=%+v", result.Status, result)
	}
	assertFileContent(t, path, "new\n")
}

func TestApplyPatchHonorsNoNewlineMarker(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "old")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	readManagedFile(t, service, "notes.txt")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1 +1 @@
-old
\ No newline at end of file
+new
\ No newline at end of file
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if result.Status != files.PatchStatusApplied {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusApplied, result)
	}
	assertFileContent(t, path, "new")
}

func TestApplyPatchDeleteExistingFileThroughManagedDelete(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "one\ntwo\n")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	readManagedFile(t, service, "notes.txt")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/notes.txt b/notes.txt
deleted file mode 100644
--- a/notes.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-one
-two
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != files.PatchStatusApplied {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusApplied, result)
	}
	if len(result.Deleted) != 1 {
		t.Fatalf("deleted count = %d, want 1; result=%+v", len(result.Deleted), result)
	}
	deleted := result.Deleted[0]
	if deleted.Delete.Status != files.DeleteStatusDeleted {
		t.Fatalf("delete status = %q, want %q; delete=%+v", deleted.Delete.Status, files.DeleteStatusDeleted, deleted.Delete)
	}
	if !deleted.BackupCreated || deleted.BackupPath == "" {
		t.Fatalf("expected backup for delete: %+v", deleted)
	}
	assertFileContent(t, deleted.BackupPath, "one\ntwo\n")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("deleted file exists or stat errored unexpectedly: %v", err)
	}
	if _, found := service.Tokens().Lookup(path); found {
		t.Fatal("did not expect read token to remain after delete")
	}
}

func TestApplyPatchModifyWithStaleReadTokenFails(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "file.txt", "alpha\n")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	readManagedFile(t, service, "file.txt")
	if err := os.WriteFile(path, []byte("beta\n"), 0o600); err != nil {
		t.Fatalf("external write: %v", err)
	}

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/file.txt b/file.txt
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-beta
+gamma
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != files.PatchStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusFailed, result)
	}
	if !strings.Contains(result.Error, "stale read token") {
		t.Fatalf("error = %q, want stale read token", result.Error)
	}
	assertFileContent(t, path, "beta\n")
}

func TestApplyPatchFailedPreflightDoesNotMintReadToken(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "file.txt", "hello\n")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	first, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/file.txt b/file.txt
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-hello
+goodbye
`})
	if err != nil {
		t.Fatalf("first ApplyPatch: %v", err)
	}
	if first.Status != files.PatchStatusFailed || !strings.Contains(first.Error, "missing read token") {
		t.Fatalf("first result = %+v, want missing read token failure", first)
	}

	write, err := service.Write(context.Background(), files.WriteRequest{
		Path:      "file.txt",
		Content:   "second mutation\n",
		Operation: files.WriteOperationOverwrite,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if write.Status != files.WriteStatusMissingReadToken {
		t.Fatalf("write status = %q, want %q; result=%+v", write.Status, files.WriteStatusMissingReadToken, write)
	}
	assertFileContent(t, path, "hello\n")
}

func TestApplyPatchMalformedAndUnsupportedInputsAreRejected(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "original\n")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	tests := []struct {
		name      string
		patch     string
		wantError string
	}{
		{
			name:      "malformed",
			patch:     "not a patch",
			wantError: "expected diff --git header",
		},
		{
			name: "custom envelope",
			patch: `*** Begin Patch
*** Update File: notes.txt
@@
-original
+changed
*** End Patch
`,
			wantError: "unsupported patch envelope",
		},
		{
			name: "rename unsupported",
			patch: `diff --git a/notes.txt b/renamed.txt
similarity index 100%
rename from notes.txt
rename to renamed.txt
`,
			wantError: "rename patches are not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: tt.patch})
			if err != nil {
				t.Fatalf("ApplyPatch: %v", err)
			}
			if result.Status != files.PatchStatusFailed {
				t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusFailed, result)
			}
			if !strings.Contains(result.Error, tt.wantError) {
				t.Fatalf("error = %q, want to contain %q", result.Error, tt.wantError)
			}
			assertFileContent(t, path, "original\n")
		})
	}
}

func TestApplyPatchCreateOutsideWorkspaceRejected(t *testing.T) {
	workspace := tempWorkspace(t)
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	outside := filepath.Join(filepath.Dir(realPath(t, workspace)), "outside.txt")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/../outside.txt b/../outside.txt
new file mode 100644
--- /dev/null
+++ b/../outside.txt
@@ -0,0 +1 @@
+nope
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != files.PatchStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusFailed, result)
	}
	if !strings.Contains(result.Error, "path escapes workspace") {
		t.Fatalf("error = %q, want workspace escape", result.Error)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat errored unexpectedly: %v", err)
	}
}

func TestApplyPatchPreflightSecretFailureLeavesAllFilesUnchanged(t *testing.T) {
	workspace := tempWorkspace(t)
	notes := writeWorkspaceFile(t, workspace, "notes.txt", "safe\n")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
	readManagedFile(t, service, "notes.txt")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
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

	if result.Status != files.PatchStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusFailed, result)
	}
	if !strings.Contains(result.Error, "secret") {
		t.Fatalf("error = %q, want secret rejection", result.Error)
	}
	assertFileContent(t, notes, "safe\n")
	if _, err := os.Stat(filepath.Join(realPath(t, workspace), "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("secret file exists or stat errored unexpectedly: %v", err)
	}
	assertDirEmpty(t, layout.BackupRoot)
}

func TestApplyPatchMultiFileFailureLeavesAllFilesUnchanged(t *testing.T) {
	workspace := tempWorkspace(t)
	first := writeWorkspaceFile(t, workspace, "first.txt", "first\n")
	second := writeWorkspaceFile(t, workspace, "second.txt", "second\n")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")
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
-missing
+SECOND
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != files.PatchStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusFailed, result)
	}
	if !strings.Contains(result.Error, "patch removal does not match") {
		t.Fatalf("error = %q, want hunk mismatch", result.Error)
	}
	assertFileContent(t, first, "first\n")
	assertFileContent(t, second, "second\n")
	assertDirEmpty(t, layout.BackupRoot)
}

func TestApplyPatchDeleteMissingFileFailsDeterministically(t *testing.T) {
	workspace := tempWorkspace(t)
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	result, err := service.ApplyPatch(context.Background(), files.PatchRequest{Patch: `diff --git a/missing.txt b/missing.txt
deleted file mode 100644
--- a/missing.txt
+++ /dev/null
@@ -1 +0,0 @@
-missing
`})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	if result.Status != files.PatchStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.PatchStatusFailed, result)
	}
	if !strings.Contains(result.Error, "managed read failed") {
		t.Fatalf("error = %q, want managed read failure", result.Error)
	}
	assertDirEmpty(t, layout.BackupRoot)
}
