package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestHistoryStoreMissingReturnsEmptyAndUsesCanonicalPath(t *testing.T) {
	layout := testLayout(t)
	historyStore := store.NewHistoryStore(layout)

	got, err := historyStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("history length = %d, want 0", len(got))
	}
	if historyStore.Path() != layout.HistoryPath {
		t.Fatalf("history path = %q, want %q", historyStore.Path(), layout.HistoryPath)
	}
}

func TestHistoryStoreWriteReadAndAppendRoundTrip(t *testing.T) {
	layout := testLayout(t)
	historyStore := store.NewHistoryStore(layout)

	want := make([]string, 0, 3)
	want = append(want, "hello", "world")
	if err := historyStore.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := historyStore.Append("again"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := historyStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	want = append(want, "again")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

func TestMemoryStoreMissingReturnsEmptyAndUsesCanonicalPath(t *testing.T) {
	layout := testLayout(t)
	memoryStore := store.NewMemoryStore(layout)

	got, err := memoryStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Entries) != 0 {
		t.Fatalf("memory entries length = %d, want 0", len(got.Entries))
	}
	if memoryStore.Path() != layout.MemoryPath {
		t.Fatalf("memory path = %q, want %q", memoryStore.Path(), layout.MemoryPath)
	}
}

func TestMemoryStoreWriteReadRoundTrip(t *testing.T) {
	layout := testLayout(t)
	memoryStore := store.NewMemoryStore(layout)
	want := store.MemoryState{
		Entries:        []string{"fact one", "fact two"},
		ImportantFiles: []string{},
		Decisions:      []string{},
		OpenQuestions:  []string{},
	}

	if err := memoryStore.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := memoryStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("memory = %#v, want %#v", got, want)
	}
}

func TestMemoryStoreStructuredRoundTrip(t *testing.T) {
	layout := testLayout(t)
	memoryStore := store.NewMemoryStore(layout)
	want := store.MemoryState{
		CurrentGoal:    "add built-in search",
		Entries:        []string{"legacy note"},
		ImportantFiles: []string{"internal/files/search.go"},
		Decisions:      []string{"search does not issue read tokens"},
		OpenQuestions:  []string{"which docs need examples?"},
	}

	if err := memoryStore.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := memoryStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("memory = %#v, want %#v", got, want)
	}
}

func TestTodoStoreMissingReturnsEmptyAndUsesCanonicalPath(t *testing.T) {
	layout := testLayout(t)
	todoStore := store.NewTodoStore(layout)

	got, err := todoStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Items) != 0 {
		t.Fatalf("todo items length = %d, want 0", len(got.Items))
	}
	if todoStore.Path() != layout.TodosPath {
		t.Fatalf("todo path = %q, want %q", todoStore.Path(), layout.TodosPath)
	}
}

func TestTodoStoreWriteReadRoundTrip(t *testing.T) {
	layout := testLayout(t)
	todoStore := store.NewTodoStore(layout)
	want := store.TodoState{
		Items: []store.TodoItem{
			{Text: "first", Done: false},
			{Text: "second", Done: true},
		},
	}

	if err := todoStore.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := todoStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("todos = %#v, want %#v", got, want)
	}
}

func TestCommandEvidenceStoreMissingReturnsEmptyAndUsesCanonicalPath(t *testing.T) {
	layout := testLayout(t)
	evidenceStore := store.NewCommandEvidenceStore(layout)

	got, err := evidenceStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Records) != 0 {
		t.Fatalf("command evidence records length = %d, want 0", len(got.Records))
	}
	if evidenceStore.Path() != layout.CommandEvidencePath {
		t.Fatalf("command evidence path = %q, want %q", evidenceStore.Path(), layout.CommandEvidencePath)
	}
}

func TestCommandEvidenceStoreWriteReadRoundTrip(t *testing.T) {
	layout := testLayout(t)
	evidenceStore := store.NewCommandEvidenceStore(layout)
	want := store.CommandEvidenceState{
		Records: []store.CommandEvidenceRecord{{
			Command:             "go test ./...",
			WorkingDir:          ".",
			Status:              "succeeded",
			ExitCode:            0,
			Executed:            true,
			Succeeded:           true,
			OutputTruncated:     true,
			OutputOriginalBytes: 100000,
			OutputPreviewBytes:  65536,
			RecordedAt:          "2026-01-02T03:04:05Z",
		}},
		ReviewAttempts: 1,
		LastReview: &store.CommandEvidenceReview{
			Verdict:    "unclear",
			Confidence: "low",
			Reason:     "no clear command",
		},
		PlanBaselineRevision:          3,
		LastWorkspaceMutationRevision: 4,
		LastWorkspaceMutationAt:       "2026-01-02T03:05:06Z",
		LastWorkspaceMutationTool:     "edit_file",
	}

	if err := evidenceStore.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := evidenceStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command evidence = %#v, want %#v", got, want)
	}
}

func TestCommandEvidenceStoreReadsLegacyVerificationPath(t *testing.T) {
	layout := testLayout(t)
	evidenceStore := store.NewCommandEvidenceStore(layout)
	legacyJSON := `{"records":[{"command":"go test ./...","status":"succeeded","exit_code":0,"executed":true,"succeeded":true,"recorded_at":"2026-01-02T03:04:05Z","revision":7}],"workspace_dirty":true,"dirty_revision":8}`
	if err := os.MkdirAll(filepath.Dir(layout.VerificationPath), 0o755); err != nil {
		t.Fatalf("mkdir legacy parent: %v", err)
	}
	if err := os.WriteFile(layout.VerificationPath, []byte(legacyJSON), 0o600); err != nil {
		t.Fatalf("write legacy evidence: %v", err)
	}
	got, err := evidenceStore.Read()
	if err != nil {
		t.Fatalf("Read legacy: %v", err)
	}
	if len(got.Records) != 1 || got.Records[0].Revision != 7 {
		t.Fatalf("legacy command evidence = %+v, want decoded state", got)
	}
	if err := evidenceStore.Write(got); err != nil {
		t.Fatalf("Write migrated: %v", err)
	}
	if _, err := os.Stat(layout.CommandEvidencePath); err != nil {
		t.Fatalf("canonical command evidence path missing after write: %v", err)
	}
}

func TestPlanStoreMissingReturnsEmptyAndUsesCanonicalPath(t *testing.T) {
	layout := testLayout(t)
	planStore := store.NewPlanStore(layout)

	got, err := planStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "" {
		t.Fatalf("plan = %q, want empty string", got)
	}
	if planStore.Path() != layout.PlanPath {
		t.Fatalf("plan path = %q, want %q", planStore.Path(), layout.PlanPath)
	}
}

func TestPlanStoreWriteReadRoundTrip(t *testing.T) {
	layout := testLayout(t)
	planStore := store.NewPlanStore(layout)
	want := "1. gather facts\n2. write summary\n"

	if err := planStore.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := planStore.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != want {
		t.Fatalf("plan = %q, want %q", got, want)
	}
}

func TestPlanStoreRequiresPath(t *testing.T) {
	var planStore store.PlanStore
	if _, err := planStore.Read(); !errors.Is(err, store.ErrPlanStorePathRequired) {
		t.Fatalf("Read error = %v, want ErrPlanStorePathRequired", err)
	}
	if err := planStore.Write("plan"); !errors.Is(err, store.ErrPlanStorePathRequired) {
		t.Fatalf("Write error = %v, want ErrPlanStorePathRequired", err)
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
