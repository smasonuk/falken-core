package state_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/state"
)

func TestReadMetadataMissingReturnsNotFound(t *testing.T) {
	layout := testLayout(t)

	metadata, found, err := state.ReadMetadata(layout)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if found {
		t.Fatalf("expected metadata to be absent, got %+v", metadata)
	}
	if metadata != (state.Metadata{}) {
		t.Fatalf("expected zero metadata, got %+v", metadata)
	}
}

func TestWriteMetadataRoundTripPreservesFields(t *testing.T) {
	layout := testLayout(t)

	want := state.Metadata{
		WorkspaceRoot: layout.WorkspaceRoot,
		LayoutVersion: state.LayoutVersion,
		CreatedAt:     time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
		LastUsedAt:    time.Date(2026, time.January, 2, 4, 5, 6, 0, time.UTC),
	}

	if err := state.WriteMetadata(layout, want); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	got, found, err := state.ReadMetadata(layout)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !found {
		t.Fatal("expected metadata to exist after write")
	}
	if got != want {
		t.Fatalf("metadata = %+v, want %+v", got, want)
	}
}

func TestTouchMetadataCreatesMissingMetadata(t *testing.T) {
	layout := testLayout(t)

	metadata, err := state.TouchMetadata(layout)
	if err != nil {
		t.Fatalf("TouchMetadata: %v", err)
	}

	if metadata.WorkspaceRoot != layout.WorkspaceRoot {
		t.Fatalf("workspace root = %q, want %q", metadata.WorkspaceRoot, layout.WorkspaceRoot)
	}
	if metadata.LayoutVersion != state.LayoutVersion {
		t.Fatalf("layout version = %d, want %d", metadata.LayoutVersion, state.LayoutVersion)
	}
	if metadata.CreatedAt.IsZero() {
		t.Fatal("created_at must be set")
	}
	if metadata.LastUsedAt.IsZero() {
		t.Fatal("last_used_at must be set")
	}
	if metadata.LastUsedAt.Before(metadata.CreatedAt) {
		t.Fatalf("last_used_at %v must not be before created_at %v", metadata.LastUsedAt, metadata.CreatedAt)
	}

	persisted, found, err := state.ReadMetadata(layout)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !found {
		t.Fatal("expected metadata to exist after touch")
	}
	if persisted != metadata {
		t.Fatalf("persisted metadata = %+v, want %+v", persisted, metadata)
	}
}

func TestTouchMetadataPreservesCreatedAtAndUpdatesLastUsedAt(t *testing.T) {
	layout := testLayout(t)

	first, err := state.TouchMetadata(layout)
	if err != nil {
		t.Fatalf("first TouchMetadata: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	second, err := state.TouchMetadata(layout)
	if err != nil {
		t.Fatalf("second TouchMetadata: %v", err)
	}

	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("created_at changed: first=%v second=%v", first.CreatedAt, second.CreatedAt)
	}
	if !second.LastUsedAt.After(first.LastUsedAt) {
		t.Fatalf("last_used_at did not advance: first=%v second=%v", first.LastUsedAt, second.LastUsedAt)
	}
	if second.WorkspaceRoot != layout.WorkspaceRoot {
		t.Fatalf("workspace root = %q, want %q", second.WorkspaceRoot, layout.WorkspaceRoot)
	}
	if second.LayoutVersion != state.LayoutVersion {
		t.Fatalf("layout version = %d, want %d", second.LayoutVersion, state.LayoutVersion)
	}
}

func TestResolveLayoutDerivesMetadataPath(t *testing.T) {
	workspaceRoot := absPath(t, filepath.Join(t.TempDir(), "workspace"))
	stateRoot := filepath.Join(t.TempDir(), "state")

	layout, err := state.ResolveLayout(workspaceRoot, stateRoot)
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}

	want := filepath.Join(layout.StateRoot, "metadata.json")
	if layout.MetadataPath != want {
		t.Fatalf("metadata path = %q, want %q", layout.MetadataPath, want)
	}
}

func TestTouchMetadataWritesCanonicalLayoutVersion(t *testing.T) {
	layout := testLayout(t)

	metadata, err := state.TouchMetadata(layout)
	if err != nil {
		t.Fatalf("TouchMetadata: %v", err)
	}

	if metadata.LayoutVersion != state.LayoutVersion {
		t.Fatalf("layout version = %d, want %d", metadata.LayoutVersion, state.LayoutVersion)
	}
}

func testLayout(t *testing.T) state.Layout {
	t.Helper()

	workspaceRoot := absPath(t, filepath.Join(t.TempDir(), "workspace"))
	stateRoot := filepath.Join(t.TempDir(), "state")

	layout, err := state.ResolveLayout(workspaceRoot, stateRoot)
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}

	return layout
}

func absPath(t *testing.T, path string) string {
	t.Helper()

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", path, err)
	}

	return filepath.Clean(abs)
}
