package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultiEditRollbackReportsRestoreFailure(t *testing.T) {
	workspace := t.TempDir()
	service, _ := newPatchRollbackService(t, workspace)
	blocker := filepath.Join(workspace, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	var result MultiEditResult
	service.rollbackMultiEdit([]multiEditRollbackEntry{{
		path:      filepath.Join(blocker, "restore.txt"),
		content:   []byte("original"),
		mode:      0o600,
		scopeID:   service.tokens.ScopeID(),
		committed: true,
	}}, &result)

	if !result.RollbackAttempted {
		t.Fatal("RollbackAttempted = false, want true")
	}
	if result.RollbackSucceeded {
		t.Fatal("RollbackSucceeded = true, want false")
	}
	if result.RollbackError == "" || !strings.Contains(result.RollbackError, "restore") {
		t.Fatalf("RollbackError = %q, want restore failure", result.RollbackError)
	}
}

func TestMultiEditRollbackFailureKeepsOriginalFailureContext(t *testing.T) {
	workspace := t.TempDir()
	first := filepath.Join(workspace, "first.txt")
	second := filepath.Join(workspace, "second.txt")
	if err := os.WriteFile(first, []byte("first old"), 0o600); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := os.WriteFile(second, []byte("second old"), 0o600); err != nil {
		t.Fatalf("write second: %v", err)
	}
	service, _ := newPatchRollbackService(t, workspace)
	for _, path := range []string{"first.txt", "second.txt"} {
		if _, err := service.Read(context.Background(), ReadRequest{Path: path}); err != nil {
			t.Fatalf("Read %q: %v", path, err)
		}
	}
	service.multiEditRollbackHook = func(multiEditRollbackEntry) error {
		return errors.New("rollback unavailable")
	}

	result, err := service.MultiEdit(context.Background(), MultiEditRequest{
		Edits: []EditRequest{
			{Path: "first.txt", Old: "old", New: "new"},
			{Path: "second.txt", Old: "missing", New: "new"},
		},
	})
	if err != nil {
		t.Fatalf("MultiEdit: %v", err)
	}

	if result.Status != MultiEditStatusPartial {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, MultiEditStatusPartial, result)
	}
	if result.FilesChanged != 1 || result.FilesRolledBack != 0 {
		t.Fatalf("files changed/rolled back = %d/%d, want 1/0", result.FilesChanged, result.FilesRolledBack)
	}
	if !result.RollbackAttempted || result.RollbackSucceeded {
		t.Fatalf("rollback = attempted:%t succeeded:%t, want attempted failed rollback", result.RollbackAttempted, result.RollbackSucceeded)
	}
	if !strings.Contains(result.Error, "one or more file groups failed") || !strings.Contains(result.Error, "rollback failed") {
		t.Fatalf("error = %q, want original failure and rollback failure", result.Error)
	}
	if !strings.Contains(result.RollbackError, "rollback unavailable") {
		t.Fatalf("rollback error = %q, want hook failure", result.RollbackError)
	}
}

func TestApplyExactEditFuzzyLevenshteinMatchesIndentation(t *testing.T) {
	content := "func example() {\n\talpha()\n\tbeta(42)\n\tgamma()\n}\n"
	updated, result := applyExactEdit(content, EditRequest{
		Path:          "code.go",
		Old:           "\talpha()\n\tbeta(7)\n\tgamma()",
		New:           "alpha()\nbeta(99)\ngamma()",
		MatchStrategy: MatchClose,
	}, 0)

	if result.Status != EditStatusChanged {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, EditStatusChanged, result)
	}
	if !result.UsedFuzzy {
		t.Fatal("UsedFuzzy = false, want true")
	}
	want := "func example() {\n\talpha()\n\tbeta(99)\n\tgamma()\n}\n"
	if updated != want {
		t.Fatalf("updated = %q, want %q", updated, want)
	}
}

func TestApplyExactEditFuzzyLevenshteinStopsAtFirstClosingAnchor(t *testing.T) {
	content := strings.Join([]string{
		"before",
		"alpha()",
		"beta(42)",
		"gamma()",
		"middle",
		"gamma()",
		"after",
	}, "\n")
	updated, result := applyExactEdit(content, EditRequest{
		Path:          "code.go",
		Old:           "alpha()\nbeta(7)\ngamma()",
		New:           "alpha()\nbeta(99)\ngamma()",
		MatchStrategy: MatchClose,
	}, 0)

	if result.Status != EditStatusChanged {
		t.Fatalf("status = %q, want %q; result=%+v", result.Status, EditStatusChanged, result)
	}
	if !result.UsedFuzzy {
		t.Fatal("UsedFuzzy = false, want true")
	}
	want := strings.Join([]string{
		"before",
		"alpha()",
		"beta(99)",
		"gamma()",
		"middle",
		"gamma()",
		"after",
	}, "\n")
	if updated != want {
		t.Fatalf("updated = %q, want %q", updated, want)
	}
}
