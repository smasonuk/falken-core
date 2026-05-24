package state_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/state"
)

func TestResetConversationStateRemovesConversationScopedStateAndPreservesDurableState(t *testing.T) {
	layout := testLayout(t)
	populateConversationState(t, layout)
	backupFile := filepath.Join(layout.BackupRoot, "keep.txt")
	writeFile(t, backupFile, "backup")

	metadata := state.Metadata{
		WorkspaceRoot: layout.WorkspaceRoot,
		LayoutVersion: state.LayoutVersion,
		CreatedAt:     mustTime("2026-01-02T03:04:05Z"),
		LastUsedAt:    mustTime("2026-01-02T04:05:06Z"),
	}
	if err := state.WriteMetadata(layout, metadata); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	if err := state.ResetConversationState(layout); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}

	for _, path := range []string{
		layout.HistoryPath,
		layout.MemoryPath,
		layout.TodosPath,
		layout.PlanPath,
		layout.CommandEvidencePath,
		layout.VerificationPath,
		filepath.Join(layout.RecentTruncationRoot, "trace.json"),
		filepath.Join(layout.RecentArtifactRoot, "output.log"),
	} {
		assertNotExists(t, path)
	}

	assertExists(t, layout.CurrentConversationRoot)
	assertExists(t, layout.RecentTruncationRoot)
	assertExists(t, layout.RecentArtifactRoot)
	assertFileContent(t, backupFile, "backup")

	readMetadata, found, err := state.ReadMetadata(layout)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !found {
		t.Fatal("expected metadata to remain after reset")
	}
	if readMetadata != metadata {
		t.Fatalf("metadata changed: got %+v want %+v", readMetadata, metadata)
	}
}

func TestResetConversationStateIsSafeWhenConversationFilesAreMissing(t *testing.T) {
	layout := testLayout(t)

	if err := state.ResetConversationState(layout); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}

	assertExists(t, layout.CurrentConversationRoot)
	assertExists(t, layout.RecentTruncationRoot)
	assertExists(t, layout.RecentArtifactRoot)
}

func TestResetConversationStateIsIdempotent(t *testing.T) {
	layout := testLayout(t)
	populateConversationState(t, layout)

	if err := state.ResetConversationState(layout); err != nil {
		t.Fatalf("first ResetConversationState: %v", err)
	}
	if err := state.ResetConversationState(layout); err != nil {
		t.Fatalf("second ResetConversationState: %v", err)
	}

	for _, path := range []string{
		layout.HistoryPath,
		layout.MemoryPath,
		layout.TodosPath,
		layout.PlanPath,
		layout.CommandEvidencePath,
		layout.VerificationPath,
	} {
		assertNotExists(t, path)
	}
}

func TestResetConversationStateLeavesLayoutUsable(t *testing.T) {
	layout := testLayout(t)
	populateConversationState(t, layout)

	if err := state.ResetConversationState(layout); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}

	writeFile(t, layout.HistoryPath, "[]")
	writeFile(t, filepath.Join(layout.RecentArtifactRoot, "new.log"), "artifact")

	assertFileContent(t, layout.HistoryPath, "[]")
	assertFileContent(t, filepath.Join(layout.RecentArtifactRoot, "new.log"), "artifact")
}

func TestResetBoundariesAreExplicit(t *testing.T) {
	layout := testLayout(t)

	conversationPaths := state.ConversationScopedPaths(layout)
	durablePaths := state.DurableStatePaths(layout)

	if len(conversationPaths) != 8 {
		t.Fatalf("conversation path count = %d, want 8", len(conversationPaths))
	}
	if len(durablePaths) != 2 {
		t.Fatalf("durable path count = %d, want 2", len(durablePaths))
	}
	if durablePaths[0] != layout.MetadataPath || durablePaths[1] != layout.BackupRoot {
		t.Fatalf("durable paths = %v", durablePaths)
	}
}

func populateConversationState(t *testing.T, layout state.Layout) {
	t.Helper()

	if err := state.EnsureConversationState(layout); err != nil {
		t.Fatalf("EnsureConversationState: %v", err)
	}

	writeFile(t, layout.HistoryPath, "history")
	writeFile(t, layout.MemoryPath, "memory")
	writeFile(t, layout.TodosPath, "todos")
	writeFile(t, layout.PlanPath, "plan")
	writeFile(t, layout.CommandEvidencePath, "command evidence")
	writeFile(t, layout.VerificationPath, "verification")
	writeFile(t, filepath.Join(layout.RecentTruncationRoot, "trace.json"), "truncation")
	writeFile(t, filepath.Join(layout.RecentArtifactRoot, "output.log"), "artifact")
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %q to exist: %v", path, err)
	}
}

func assertNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be absent, got err=%v", path, err)
	}
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func mustTime(value string) (ts time.Time) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
