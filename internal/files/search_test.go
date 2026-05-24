package files_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/files"
	"github.com/smasonuk/falken-core/internal/policy"
)

func TestGlobFindsRecursiveFilesAndSkipsHiddenIgnoredByDefault(t *testing.T) {
	workspace := tempWorkspace(t)
	a := writeWorkspaceFile(t, workspace, "a.go", "package main\n")
	writeWorkspaceFile(t, workspace, "internal/b.go", "package internal\n")
	writeWorkspaceFile(t, workspace, ".hidden.go", "package hidden\n")
	writeWorkspaceFile(t, workspace, "node_modules/pkg/c.go", "package ignored\n")
	writeWorkspaceFile(t, workspace, "vendor/d.go", "package vendor\n")
	service := newReadService(t, workspace, policy.Config{}, "run-1")

	result, err := service.Glob(context.Background(), files.GlobRequest{Pattern: "**/*.go"})
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if result.Status != files.GlobStatusOK {
		t.Fatalf("status = %q, result=%+v", result.Status, result)
	}
	want := []string{"a.go", "internal/b.go"}
	if !reflect.DeepEqual(result.Matches, want) {
		t.Fatalf("matches = %#v, want %#v", result.Matches, want)
	}
	if _, found := service.Tokens().Lookup(a); found {
		t.Fatal("glob issued a read token")
	}

	hidden, err := service.Glob(context.Background(), files.GlobRequest{Pattern: "**/*.go", IncludeHidden: true})
	if err != nil {
		t.Fatalf("Glob hidden: %v", err)
	}
	if !containsString(hidden.Matches, ".hidden.go") || containsString(hidden.Matches, "node_modules/pkg/c.go") {
		t.Fatalf("hidden matches = %#v, want hidden file but not ignored dirs", hidden.Matches)
	}

	ignored, err := service.Glob(context.Background(), files.GlobRequest{Pattern: "**/*.go", IncludeIgnored: true})
	if err != nil {
		t.Fatalf("Glob ignored: %v", err)
	}
	if !containsString(ignored.Matches, "node_modules/pkg/c.go") || !containsString(ignored.Matches, "vendor/d.go") {
		t.Fatalf("ignored matches = %#v, want ignored directories included", ignored.Matches)
	}
}

func TestGlobOptionsDirsFilesLimitOffsetSortAndUnsafe(t *testing.T) {
	workspace := tempWorkspace(t)
	first := writeWorkspaceFile(t, workspace, "first.txt", "first")
	second := writeWorkspaceFile(t, workspace, "nested/second.txt", "second")
	third := writeWorkspaceFile(t, workspace, "nested/third.txt", "third")
	service := newReadService(t, workspace, policy.Config{}, "run-1")
	now := time.Now()
	if err := os.Chtimes(first, now.Add(-3*time.Hour), now.Add(-3*time.Hour)); err != nil {
		t.Fatalf("chtimes first: %v", err)
	}
	if err := os.Chtimes(second, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("chtimes second: %v", err)
	}
	if err := os.Chtimes(third, now.Add(-1*time.Hour), now.Add(-1*time.Hour)); err != nil {
		t.Fatalf("chtimes third: %v", err)
	}

	nonRecursive, err := service.Glob(context.Background(), files.GlobRequest{Pattern: "*.txt"})
	if err != nil {
		t.Fatalf("Glob non-recursive: %v", err)
	}
	if !reflect.DeepEqual(nonRecursive.Matches, []string{"first.txt"}) {
		t.Fatalf("non-recursive matches = %#v, want first only", nonRecursive.Matches)
	}

	includeDirs, err := service.Glob(context.Background(), files.GlobRequest{Pattern: "**", IncludeDirs: true, IncludeFiles: boolPtr(false)})
	if err != nil {
		t.Fatalf("Glob dirs: %v", err)
	}
	if !reflect.DeepEqual(includeDirs.Matches, []string{"nested"}) {
		t.Fatalf("dir matches = %#v, want nested", includeDirs.Matches)
	}

	page, err := service.Glob(context.Background(), files.GlobRequest{Pattern: "**/*.txt", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("Glob page: %v", err)
	}
	if !page.Truncated || page.TotalMatches != 3 || !reflect.DeepEqual(page.Matches, []string{"nested/second.txt"}) {
		t.Fatalf("page result = %+v, want one truncated middle match", page)
	}

	modified, err := service.Glob(context.Background(), files.GlobRequest{Pattern: "**/*.txt", Sort: "modified_desc"})
	if err != nil {
		t.Fatalf("Glob modified: %v", err)
	}
	if !reflect.DeepEqual(modified.Matches, []string{"nested/third.txt", "nested/second.txt", "first.txt"}) {
		t.Fatalf("modified matches = %#v, want newest first", modified.Matches)
	}

	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	unsafe, err := service.Glob(context.Background(), files.GlobRequest{Pattern: "*", Path: outside})
	if err != nil {
		t.Fatalf("Glob unsafe: %v", err)
	}
	if unsafe.Status != files.GlobStatusUnsafe {
		t.Fatalf("unsafe status = %q, want unsafe_path", unsafe.Status)
	}
}

func TestGrepContentModesContextFiltersAndTokens(t *testing.T) {
	workspace := tempWorkspace(t)
	app := writeWorkspaceFile(t, workspace, "app/main.go", "package main\n\nfunc NewTool() {}\nfunc oldTool() {}\n")
	writeWorkspaceFile(t, workspace, "app/main.txt", "func NewTextTool\n")
	writeWorkspaceFile(t, workspace, ".hidden.go", "func NewHiddenTool() {}\n")
	writeWorkspaceFile(t, workspace, "dist/generated.go", "func NewGeneratedTool() {}\n")
	writeWorkspaceFile(t, workspace, "asset.bin", "func NewBinaryTool\x00")
	service := newReadService(t, workspace, policy.Config{}, "run-1")

	result, err := service.Grep(context.Background(), files.GrepRequest{
		Regex:       "func New.*Tool",
		TargetPaths: []string{"."},
		Glob:        "**/*.go",
		Context:     1,
	})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if result.Status != files.GrepStatusOK || len(result.Matches) != 1 {
		t.Fatalf("result = %+v, want one go match", result)
	}
	match := result.Matches[0]
	if match.Path != "app/main.go" || match.Line != 3 || match.Text != "func NewTool() {}" {
		t.Fatalf("match = %+v, want app/main.go line 3", match)
	}
	if !reflect.DeepEqual(match.ContextBefore, []string{""}) || !reflect.DeepEqual(match.ContextAfter, []string{"func oldTool() {}"}) {
		t.Fatalf("context = %#v/%#v, want before blank and after oldTool", match.ContextBefore, match.ContextAfter)
	}
	if _, found := service.Tokens().Lookup(app); found {
		t.Fatal("grep issued a read token")
	}

	includeHidden, err := service.Grep(context.Background(), files.GrepRequest{Regex: "NewHidden", IncludeHidden: true})
	if err != nil {
		t.Fatalf("Grep hidden: %v", err)
	}
	if len(includeHidden.Matches) != 1 || includeHidden.Matches[0].Path != ".hidden.go" {
		t.Fatalf("hidden result = %+v, want hidden match", includeHidden)
	}
}

func TestGrepOutputModesCaseLimitOffsetBinaryAndInvalidRegex(t *testing.T) {
	workspace := tempWorkspace(t)
	writeWorkspaceFile(t, workspace, "one.txt", "TODO one\nnope\nTODO two\n")
	writeWorkspaceFile(t, workspace, "two.txt", "todo lower\n")
	writeWorkspaceFile(t, workspace, "image.png", "TODO binary by extension\n")
	service := newReadService(t, workspace, policy.Config{}, "run-1")
	caseSensitive := false

	filesMode, err := service.Grep(context.Background(), files.GrepRequest{
		Regex:         "todo",
		CaseSensitive: &caseSensitive,
		OutputMode:    "files_with_matches",
		Limit:         1,
	})
	if err != nil {
		t.Fatalf("Grep files mode: %v", err)
	}
	if !filesMode.Truncated || filesMode.Returned != 1 || filesMode.FilesWithMatches != 2 || !reflect.DeepEqual(filesMode.Files, []string{"one.txt"}) {
		t.Fatalf("files mode = %+v, want first file and truncation", filesMode)
	}

	countMode, err := service.Grep(context.Background(), files.GrepRequest{
		Regex:         "todo",
		CaseSensitive: &caseSensitive,
		OutputMode:    "count",
		Limit:         1,
	})
	if err != nil {
		t.Fatalf("Grep count mode: %v", err)
	}
	if !countMode.Truncated || countMode.NextOffset != 1 || countMode.TotalMatchesSeen != 3 || countMode.FilesWithMatches != 2 || len(countMode.Counts) != 1 || countMode.Counts[0].Path != "one.txt" || countMode.Counts[0].Matches != 2 {
		t.Fatalf("count mode = %+v, want first count page with totals", countMode)
	}
	countPage, err := service.Grep(context.Background(), files.GrepRequest{
		Regex:         "todo",
		CaseSensitive: &caseSensitive,
		OutputMode:    "count",
		Limit:         1,
		Offset:        1,
	})
	if err != nil {
		t.Fatalf("Grep count page: %v", err)
	}
	if countPage.Truncated || countPage.NextOffset != 0 || countPage.TotalMatchesSeen != 3 || countPage.FilesWithMatches != 2 || len(countPage.Counts) != 1 || countPage.Counts[0].Path != "two.txt" || countPage.Counts[0].Matches != 1 {
		t.Fatalf("count page = %+v, want second count page", countPage)
	}

	page, err := service.Grep(context.Background(), files.GrepRequest{Regex: "TODO", Offset: 1, Limit: 1})
	if err != nil {
		t.Fatalf("Grep page: %v", err)
	}
	if page.Truncated || len(page.Matches) != 1 || page.Matches[0].Text != "TODO two" {
		t.Fatalf("page = %+v, want second TODO without truncation", page)
	}

	invalid, err := service.Grep(context.Background(), files.GrepRequest{Regex: "["})
	if err != nil {
		t.Fatalf("Grep invalid regex: %v", err)
	}
	if invalid.Status != files.GrepStatusInvalidRegex {
		t.Fatalf("invalid status = %q, want invalid_regex", invalid.Status)
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
