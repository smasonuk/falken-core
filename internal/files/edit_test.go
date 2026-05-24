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

func TestEditSingleOccurrenceSucceedsThroughManagedOverwrite(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "hello target world")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	result, err := service.Edit(context.Background(), files.EditRequest{
		Path: "notes.txt",
		Old:  "target",
		New:  "managed edit",
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if result.Status != files.EditStatusChanged {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.EditStatusChanged, result)
	}
	if result.Replacements != 1 {
		t.Fatalf("replacements = %d, want 1", result.Replacements)
	}
	if !result.Changed {
		t.Fatal("expected changed result")
	}
	if result.ResolvedPath != path {
		t.Fatalf("resolved path = %q, want %q", result.ResolvedPath, path)
	}
	if result.Write.Status != files.WriteStatusOverwritten {
		t.Fatalf("write status = %q, want %q; write=%+v", result.Write.Status, files.WriteStatusOverwritten, result.Write)
	}
	if !result.Write.BackupCreated || result.Write.BackupPath == "" {
		t.Fatalf("expected managed overwrite backup: %+v", result.Write)
	}
	assertFileContent(t, result.Write.BackupPath, "hello target world")
	assertFileContent(t, path, "hello managed edit world")

	token, found := service.Tokens().Lookup(path)
	if !found {
		t.Fatal("expected refreshed token after edit")
	}
	if token.ContentHash != sha256Hex("hello managed edit world") {
		t.Fatalf("token hash = %q, want %q", token.ContentHash, sha256Hex("hello managed edit world"))
	}
}

func TestEditWithoutPriorReadTokenRejected(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "hello target")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	result, err := service.Edit(context.Background(), files.EditRequest{
		Path: "notes.txt",
		Old:  "target",
		New:  "replacement",
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if result.Status != files.EditStatusMissingReadToken {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.EditStatusMissingReadToken, result)
	}
	assertFileContent(t, path, "hello target")
}

func TestEditWithStaleReadTokenRejected(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "hello target")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := os.WriteFile(path, []byte("external target"), 0o600); err != nil {
		t.Fatalf("external write: %v", err)
	}

	result, err := service.Edit(context.Background(), files.EditRequest{
		Path: "notes.txt",
		Old:  "target",
		New:  "replacement",
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if result.Status != files.EditStatusStaleReadToken {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.EditStatusStaleReadToken, result)
	}
	assertFileContent(t, path, "external target")
}

func TestEditDeterministicMatchFailures(t *testing.T) {
	workspace := tempWorkspace(t)
	writeWorkspaceFile(t, workspace, "notes.txt", "alpha beta beta")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}

	tests := []struct {
		name string
		old  string
		want files.EditStatus
	}{
		{name: "missing old string", old: "gamma", want: files.EditStatusNoMatch},
		{name: "ambiguous old string", old: "beta", want: files.EditStatusAmbiguousMatch},
		{name: "empty old string", old: "", want: files.EditStatusEmptyOldString},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.Edit(context.Background(), files.EditRequest{
				Path: "notes.txt",
				Old:  tt.old,
				New:  "replacement",
			})
			if err != nil {
				t.Fatalf("Edit: %v", err)
			}
			if result.Status != tt.want {
				t.Fatalf("status = %q, want %q; result=%+v", result.Status, tt.want, result)
			}
			assertFileContent(t, filepath.Join(realPath(t, workspace), "notes.txt"), "alpha beta beta")
		})
	}
}

func TestEditReplaceAll(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "x alpha x beta x")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	result, err := service.Edit(context.Background(), files.EditRequest{
		Path:       "notes.txt",
		Old:        "x",
		New:        "y",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if result.Status != files.EditStatusChanged {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.EditStatusChanged, result)
	}
	if result.Replacements != 3 {
		t.Fatalf("replacements = %d, want 3", result.Replacements)
	}
	assertFileContent(t, path, "y alpha y beta y")
}

func TestEditReplaceAllNoMatchRejected(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "alpha beta")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	result, err := service.Edit(context.Background(), files.EditRequest{
		Path:       "notes.txt",
		Old:        "gamma",
		New:        "delta",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if result.Status != files.EditStatusNoMatch {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.EditStatusNoMatch, result)
	}
	assertFileContent(t, path, "alpha beta")
}

func TestEditSecretRejectedThroughManagedMutationLayer(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "replace me")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	result, err := service.Edit(context.Background(), files.EditRequest{
		Path: "notes.txt",
		Old:  "replace me",
		New:  "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----\n",
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if result.Status != files.EditStatusMutationRejected {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.EditStatusMutationRejected, result)
	}
	if result.Write.Status != files.WriteStatusSecretRejected {
		t.Fatalf("write status = %q, want %q", result.Write.Status, files.WriteStatusSecretRejected)
	}
	assertFileContent(t, path, "replace me")
	assertDirEmpty(t, layout.BackupRoot)
}

func TestMultiEditSameFileAppliesSequentiallyAndCommitsOnce(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "one two three")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	result, err := service.MultiEdit(context.Background(), files.MultiEditRequest{
		Edits: []files.EditRequest{
			{Path: "notes.txt", Old: "one", New: "1"},
			{Path: "notes.txt", Old: "two", New: "2"},
		},
	})
	if err != nil {
		t.Fatalf("MultiEdit: %v", err)
	}

	if result.Status != files.MultiEditStatusApplied {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.MultiEditStatusApplied, result)
	}
	if result.FilesChanged != 1 || result.TotalReplacements != 2 {
		t.Fatalf("files changed/replacements = %d/%d, want 1/2", result.FilesChanged, result.TotalReplacements)
	}
	if len(result.Files) != 1 {
		t.Fatalf("file result count = %d, want 1", len(result.Files))
	}
	if result.Files[0].Write.Status != files.WriteStatusOverwritten {
		t.Fatalf("write status = %q, want overwritten", result.Files[0].Write.Status)
	}
	assertFileContent(t, path, "1 2 three")
	if backups := countRegularFiles(t, layout.BackupRoot); backups != 1 {
		t.Fatalf("backup count = %d, want 1", backups)
	}
}

func TestMultiEditSameFilePreservesInputOrder(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "a b")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	result, err := service.MultiEdit(context.Background(), files.MultiEditRequest{
		Edits: []files.EditRequest{
			{Path: "notes.txt", Old: "a", New: "b"},
			{Path: "notes.txt", Old: "b", New: "c", ReplaceAll: true},
		},
	})
	if err != nil {
		t.Fatalf("MultiEdit: %v", err)
	}

	if result.Status != files.MultiEditStatusApplied {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.MultiEditStatusApplied, result)
	}
	assertFileContent(t, path, "c c")
}

func TestMultiEditSameFileFailureDoesNotPartiallyCommit(t *testing.T) {
	workspace := tempWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "notes.txt", "one two")
	service, layout := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	if _, err := service.Read(context.Background(), files.ReadRequest{Path: "notes.txt"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	result, err := service.MultiEdit(context.Background(), files.MultiEditRequest{
		Edits: []files.EditRequest{
			{Path: "notes.txt", Old: "one", New: "1"},
			{Path: "notes.txt", Old: "missing", New: "x"},
		},
	})
	if err != nil {
		t.Fatalf("MultiEdit: %v", err)
	}

	if result.Status != files.MultiEditStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.MultiEditStatusFailed, result)
	}
	assertFileContent(t, path, "one two")
	assertDirEmpty(t, layout.BackupRoot)
}

func TestMultiEditMultipleFilesSuccess(t *testing.T) {
	workspace := tempWorkspace(t)
	first := writeWorkspaceFile(t, workspace, "first.txt", "first old")
	second := writeWorkspaceFile(t, workspace, "second.txt", "second old")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	for _, path := range []string{"first.txt", "second.txt"} {
		if _, err := service.Read(context.Background(), files.ReadRequest{Path: path}); err != nil {
			t.Fatalf("Read %q: %v", path, err)
		}
	}

	result, err := service.MultiEdit(context.Background(), files.MultiEditRequest{
		Edits: []files.EditRequest{
			{Path: "first.txt", Old: "old", New: "new"},
			{Path: "second.txt", Old: "old", New: "new"},
		},
	})
	if err != nil {
		t.Fatalf("MultiEdit: %v", err)
	}

	if result.Status != files.MultiEditStatusApplied {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.MultiEditStatusApplied, result)
	}
	if result.FilesChanged != 2 || result.TotalReplacements != 2 {
		t.Fatalf("files changed/replacements = %d/%d, want 2/2", result.FilesChanged, result.TotalReplacements)
	}
	assertFileContent(t, first, "first new")
	assertFileContent(t, second, "second new")
}

func TestMultiEditMultipleFilesFailureRollsBackPriorFiles(t *testing.T) {
	workspace := tempWorkspace(t)
	first := writeWorkspaceFile(t, workspace, "first.txt", "first old")
	second := writeWorkspaceFile(t, workspace, "second.txt", "second old")
	service, _ := newFileServiceForLayout(t, workspace, policy.Config{}, "run-1")

	for _, path := range []string{"first.txt", "second.txt"} {
		if _, err := service.Read(context.Background(), files.ReadRequest{Path: path}); err != nil {
			t.Fatalf("Read %q: %v", path, err)
		}
	}

	result, err := service.MultiEdit(context.Background(), files.MultiEditRequest{
		Edits: []files.EditRequest{
			{Path: "first.txt", Old: "old", New: "new"},
			{Path: "second.txt", Old: "missing", New: "new"},
		},
	})
	if err != nil {
		t.Fatalf("MultiEdit: %v", err)
	}

	if result.Status != files.MultiEditStatusFailed {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, files.MultiEditStatusFailed, result)
	}
	if result.FilesChanged != 0 || result.FilesRolledBack != 1 {
		t.Fatalf("files changed/rolled back = %d/%d, want 0/1", result.FilesChanged, result.FilesRolledBack)
	}
	if !strings.Contains(result.Error, "prior changes were rolled back") {
		t.Fatalf("error = %q, want rollback explanation", result.Error)
	}
	if !result.RollbackAttempted || !result.RollbackSucceeded || result.RollbackError != "" {
		t.Fatalf("rollback = attempted:%t succeeded:%t error:%q, want successful rollback", result.RollbackAttempted, result.RollbackSucceeded, result.RollbackError)
	}
	assertFileContent(t, first, "first old")
	assertFileContent(t, second, "second old")
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
