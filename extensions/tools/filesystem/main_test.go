package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/pkg/pluginsdk"
)

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func TestResolvePath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_resolve_path")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir
	workspaceRoot = tempDir
	canonicalTempDir := canonicalPath(t, tempDir)

	tests := []struct {
		name     string
		path     string
		expected string
		wantErr  bool
	}{
		{"Relative path", "test.txt", filepath.Join(canonicalTempDir, "test.txt"), false},
		{"Absolute path in workspace", filepath.Join(tempDir, "test.txt"), filepath.Join(canonicalTempDir, "test.txt"), false},
		{"Path with .. staying in workspace", "subdir/../test.txt", filepath.Join(canonicalTempDir, "test.txt"), false},
		{"Path with .. escaping workspace", "../outside.txt", "", true},
		{"Absolute path outside workspace", "/tmp/outside.txt", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolvePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("resolvePath() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestResolvePath_AllowsParentWithinWorkspaceRoot(t *testing.T) {
	workspace := t.TempDir()
	subdir := filepath.Join(workspace, "src", "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	currentCWD = subdir
	workspaceRoot = workspace

	got, err := resolvePath("../README.md")
	if err != nil {
		t.Fatalf("resolvePath returned error: %v", err)
	}
	want := filepath.Join(canonicalPath(t, filepath.Join(workspace, "src")), "README.md")
	if got != want {
		t.Fatalf("resolvePath() = %q, want %q", got, want)
	}
}

func TestResolvePath_RejectsSymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	linkPath := filepath.Join(workspace, "link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	currentCWD = workspace
	workspaceRoot = workspace

	if _, err := resolvePath("link/secret.txt"); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestHandleWriteAndReadFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_write_read")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir

	// Test WriteFile
	writeFileArgs := map[string]any{
		"path":    "hello.txt",
		"content": "Hello, World!\nLine 2\nLine 3",
	}
	writeResult := handleWriteFile(writeFileArgs)
	if !strings.HasPrefix(writeResult, "Successfully wrote") {
		t.Errorf("handleWriteFile failed: %s", writeResult)
	}

	// Test ReadFile (full)
	readFileArgs := map[string]any{
		"path": "hello.txt",
	}
	readResult := handleReadFile(readFileArgs)
	expectedContent := "1 | Hello, World!\n2 | Line 2\n3 | Line 3"
	if strings.TrimSpace(readResult) != strings.TrimSpace(expectedContent) {
		t.Errorf("handleReadFile (full) = %q, want %q", readResult, expectedContent)
	}

	// Test ReadFile (lines)
	readLinesArgs := map[string]any{
		"path":      "hello.txt",
		"startline": float64(2), // JSON numbers parse as float64 in generic maps
		"endline":   float64(2),
	}
	readLinesResult := handleReadFile(readLinesArgs)
	expectedLinesContent := "2 | Line 2"
	if strings.TrimSpace(readLinesResult) != expectedLinesContent {
		t.Errorf("handleReadFile (lines) = %q, want %q", readLinesResult, expectedLinesContent)
	}
}

func TestHandleEditFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_edit")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir
	filePath := filepath.Join(tempDir, "editme.txt")
	content := "Line 1\nLine 2\nLine 3"
	os.WriteFile(filePath, []byte(content), 0644)
	handleReadFile(map[string]any{"path": "editme.txt", "startline": float64(1), "endline": float64(3)})

	// Test successful edit
	editArgs := map[string]any{
		"path":      "editme.txt",
		"oldstring": "Line 2",
		"newstring": "Line Two",
	}
	editResult := handleEditFile(editArgs)
	if !strings.HasPrefix(editResult, "Successfully edited") {
		t.Errorf("handleEditFile failed: %s", editResult)
	}
	// Verify diff output is present
	if !strings.Contains(editResult, "--- a/editme.txt") || !strings.Contains(editResult, "+++ b/editme.txt") {
		t.Errorf("handleEditFile result should contain unified diff: %s", editResult)
	}
	if !strings.Contains(editResult, "-Line 2") || !strings.Contains(editResult, "+Line Two") {
		t.Errorf("handleEditFile result should show change in diff: %s", editResult)
	}

	newContent, _ := os.ReadFile(filePath)
	expectedContent := "Line 1\nLine Two\nLine 3"
	if string(newContent) != expectedContent {
		t.Errorf("handleEditFile content mismatch: %q, want %q", string(newContent), expectedContent)
	}

	// Test multi-match error
	os.WriteFile(filePath, []byte("foo\nfoo\n"), 0644)
	handleReadFile(map[string]any{"path": "editme.txt", "startline": float64(1), "endline": float64(3)})
	editArgs = map[string]any{
		"path":      "editme.txt",
		"oldstring": "foo",
		"newstring": "bar",
	}
	editResult = handleEditFile(editArgs)
	if !strings.Contains(editResult, "matches multiple locations") {
		t.Errorf("handleEditFile should have failed with multi-match: %s", editResult)
	}

	// Test fuzzy match error message
	os.WriteFile(filePath, []byte("Line 1\n\nLine 2"), 0644)
	handleReadFile(map[string]any{"path": "editme.txt", "startline": float64(1), "endline": float64(3)})
	editArgs = map[string]any{
		"path":      "editme.txt",
		"oldstring": "Line 1 Line 2",
		"newstring": "Changed",
	}
	editResult = handleEditFile(editArgs)
	if !strings.Contains(editResult, "fuzzy match succeeded") {
		t.Errorf("handleEditFile should have reported fuzzy match success: %s", editResult)
	}
}

func TestHandleMultiEdit_SameFileAppliesAllEditsInOneWrite(t *testing.T) {
	if err := pluginsdk.SetState(""); err != nil {
		t.Fatalf("reset plugin state: %v", err)
	}

	tempDir, err := os.MkdirTemp("", "test_multi_edit")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir
	workspaceRoot = tempDir

	filePath := filepath.Join(tempDir, "editme.txt")
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	handleReadFile(map[string]any{"path": "editme.txt"})

	result := handleMultiEdit(map[string]any{
		"Edits": []any{
			map[string]any{
				"Path":      "editme.txt",
				"OldString": "alpha",
				"NewString": "ALPHA",
			},
			map[string]any{
				"Path":      "editme.txt",
				"OldString": "gamma",
				"NewString": "GAMMA",
			},
		},
	})

	if !strings.Contains(result, "Successfully edited") {
		t.Fatalf("expected multi_edit success, got %q", result)
	}

	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	if got := string(updated); got != "ALPHA\nbeta\nGAMMA\n" {
		t.Fatalf("unexpected multi_edit content: %q", got)
	}
}

func TestHandleGlob(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_glob")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir
	os.MkdirAll(filepath.Join(tempDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(tempDir, "a.txt"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tempDir, "b.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tempDir, "subdir", "c.txt"), []byte(""), 0644)

	// Add node_modules test file
	os.MkdirAll(filepath.Join(tempDir, "node_modules"), 0755)
	os.WriteFile(filepath.Join(tempDir, "node_modules", "bad.txt"), []byte(""), 0644)

	// Test simple glob
	globArgs := map[string]any{"pattern": "*.txt"}
	globResult := handleGlob(globArgs)
	if !strings.Contains(globResult, "a.txt") || strings.Contains(globResult, "b.go") || strings.Contains(globResult, "c.txt") {
		t.Errorf("handleGlob (*.txt) = %q", globResult)
	}

	// Test recursive glob
	globArgs = map[string]any{"pattern": "**/*.txt"}
	globResult = handleGlob(globArgs)
	if !strings.Contains(globResult, "a.txt") || !strings.Contains(globResult, "subdir/c.txt") {
		t.Errorf("handleGlob (**/*.txt) = %q", globResult)
	}
	if strings.Contains(globResult, "node_modules") || strings.Contains(globResult, "bad.txt") {
		t.Errorf("handleGlob (**/*.txt) should not contain node_modules: %q", globResult)
	}

	// Test truncation
	os.MkdirAll(filepath.Join(tempDir, "many"), 0755)
	for i := 0; i < 105; i++ {
		os.WriteFile(filepath.Join(tempDir, "many", fmt.Sprintf("file%d.log", i)), []byte(""), 0644)
	}
	globArgs = map[string]any{"pattern": "many/*.log"}
	globResult = handleGlob(globArgs)
	if !strings.Contains(globResult, "(Results are truncated") {
		t.Errorf("handleGlob truncation string not found: %q", globResult)
	}
	lines := strings.Split(strings.TrimSpace(globResult), "\n")
	//files + 1 truncation warning = 101 lines
	if len(lines) != 101 {
		t.Errorf("handleGlob expected 101 lines, got %d", len(lines))
	}
}

func TestHandleGrep(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_grep")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir
	os.WriteFile(filepath.Join(tempDir, "grep1.txt"), []byte("hello world\nfoo bar"), 0644)
	os.WriteFile(filepath.Join(tempDir, "grep2.txt"), []byte("goodbye world\nbaz qux"), 0644)

	grepArgs := map[string]any{
		"regex":       "world",
		"targetpaths": []any{"."},
	}
	grepResult := handleGrep(grepArgs)
	if !strings.Contains(grepResult, "grep1.txt:1: hello world") || !strings.Contains(grepResult, "grep2.txt:1: goodbye world") {
		t.Errorf("handleGrep (world) = %q", grepResult)
	}

	grepArgs = map[string]any{
		"regex":       "foo",
		"targetpaths": []any{"grep1.txt"},
	}
	grepResult = handleGrep(grepArgs)
	if !strings.Contains(grepResult, "grep1.txt:2: foo bar") || strings.Contains(grepResult, "grep2.txt") {
		t.Errorf("handleGrep (foo) = %q", grepResult)
	}
}

func TestHandleReadFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_read_files")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir
	os.WriteFile(filepath.Join(tempDir, "f1.txt"), []byte("content 1"), 0644)
	os.WriteFile(filepath.Join(tempDir, "f2.txt"), []byte("content 2"), 0644)

	readFilesArgs := map[string]any{
		"paths": []any{"f1.txt", "f2.txt"},
	}
	readFilesResult := handleReadFiles(readFilesArgs)
	if !strings.Contains(readFilesResult, "--- FILE: f1.txt ---\n1 | content 1") || !strings.Contains(readFilesResult, "--- FILE: f2.txt ---\n1 | content 2") {
		t.Errorf("handleReadFiles result mismatch: %q", readFilesResult)
	}
}

func TestHandleWriteFile_PreservesExecutableModeAndCreatesParents(t *testing.T) {
	tempDir := t.TempDir()
	currentCWD = tempDir
	workspaceRoot = tempDir

	scriptPath := filepath.Join(tempDir, "bin", "script.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho old\n"), 0755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	result := handleWriteFile(map[string]any{
		"path":    "bin/script.sh",
		"content": "#!/bin/sh\necho new\n",
	})
	if !strings.Contains(result, "Successfully overwrote") {
		t.Fatalf("unexpected write result: %q", result)
	}

	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("expected executable mode 0755, got %o", info.Mode().Perm())
	}

	nestedResult := handleWriteFile(map[string]any{
		"path":    "a/b/c.txt",
		"content": "nested\n",
	})
	if !strings.Contains(nestedResult, "Successfully wrote new file") {
		t.Fatalf("unexpected nested write result: %q", nestedResult)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "a", "b", "c.txt")); err != nil {
		t.Fatalf("expected nested file to exist: %v", err)
	}
}

func TestHandleApplyPatch_PreservesModeOnExistingFile(t *testing.T) {
	tempDir := t.TempDir()
	currentCWD = tempDir
	workspaceRoot = tempDir

	scriptPath := filepath.Join(tempDir, "tool.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho old\n"), 0755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	result := handleApplyPatch(map[string]any{
		"PatchText": `diff --git a/tool.sh b/tool.sh
index 1111111..2222222 100755
--- a/tool.sh
+++ b/tool.sh
@@ -1,2 +1,2 @@
 #!/bin/sh
-echo old
+echo new
`,
	})
	if !strings.Contains(result, "Successfully patched tool.sh") {
		t.Fatalf("unexpected patch result: %q", result)
	}

	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("expected executable mode 0755, got %o", info.Mode().Perm())
	}
}

func TestHandleApplyPatch_DeletesFile(t *testing.T) {
	tempDir := t.TempDir()
	currentCWD = tempDir
	workspaceRoot = tempDir

	filePath := filepath.Join(tempDir, "delete-me.txt")
	if err := os.WriteFile(filePath, []byte("gone\n"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	result := handleApplyPatch(map[string]any{
		"PatchText": `diff --git a/delete-me.txt b/delete-me.txt
deleted file mode 100644
index 1111111..0000000
--- a/delete-me.txt
+++ /dev/null
@@ -1 +0,0 @@
-gone
`,
	})

	if !strings.Contains(result, "Successfully deleted delete-me.txt") {
		t.Fatalf("unexpected patch result: %q", result)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, stat err=%v", err)
	}
}

func TestHandleApplyPatch_RenamesFile(t *testing.T) {
	tempDir := t.TempDir()
	currentCWD = tempDir
	workspaceRoot = tempDir

	oldPath := filepath.Join(tempDir, "old.txt")
	newPath := filepath.Join(tempDir, "new.txt")
	if err := os.WriteFile(oldPath, []byte("same\n"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	result := handleApplyPatch(map[string]any{
		"PatchText": `diff --git a/old.txt b/new.txt
similarity index 100%
rename from old.txt
rename to new.txt
`,
	})

	if !strings.Contains(result, "Successfully renamed old.txt to new.txt") {
		t.Fatalf("unexpected patch result: %q", result)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old path to be removed, stat err=%v", err)
	}
	updated, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("expected renamed file to exist: %v", err)
	}
	if string(updated) != "same\n" {
		t.Fatalf("unexpected renamed file content: %q", string(updated))
	}
}

func TestMainFunction(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_main")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	payload := PluginPayload{
		Command: "write_file",
		Args: map[string]any{
			"path":    "main_test.txt",
			"content": "main test content",
		},
		RealCWD: tempDir,
	}
	input, _ := json.Marshal(payload)

	// Mock stdin and capture stdout
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.Write(input)
		w.Close()
	}()

	rOut, wOut, _ := os.Pipe()
	os.Stdout = wOut

	main()

	wOut.Close()
	var out bytes.Buffer
	io.Copy(&out, rOut)

	var response map[string]string
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse main output: %v", err)
	}

	if !strings.Contains(response["result"], "Successfully wrote") {
		t.Errorf("main output unexpected: %v", response["result"])
	}

	if _, err := os.Stat(filepath.Join(tempDir, "main_test.txt")); err != nil {
		t.Errorf("File not written by main: %v", err)
	}
}

// Token Context Safeguards & Expanded Ignore Lists
func TestGrepExpandedIgnoreLists(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_ignore_dirs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir

	// Create test files in various ignored directories
	os.Mkdir(filepath.Join(tempDir, ".git"), 0755)
	os.Mkdir(filepath.Join(tempDir, "node_modules"), 0755)
	os.Mkdir(filepath.Join(tempDir, "vendor"), 0755)

	os.WriteFile(filepath.Join(tempDir, ".git", "ignored.txt"), []byte("test pattern"), 0644)
	os.WriteFile(filepath.Join(tempDir, "node_modules", "ignored.txt"), []byte("test pattern"), 0644)
	os.WriteFile(filepath.Join(tempDir, "vendor", "ignored.txt"), []byte("test pattern"), 0644)
	os.WriteFile(filepath.Join(tempDir, "included.txt"), []byte("test pattern"), 0644)

	grepArgs := map[string]any{
		"regex":       "test pattern",
		"targetpaths": []any{"."},
	}
	grepResult := handleGrep(grepArgs)

	// Should find the file in root but not in ignored directories
	if !strings.Contains(grepResult, "included.txt") {
		t.Errorf("Should find file in root directory")
	}
	if strings.Contains(grepResult, ".git") || strings.Contains(grepResult, "node_modules") || strings.Contains(grepResult, "vendor") {
		t.Errorf("Should not search in ignored directories")
	}
}

// Test line truncation at 500 characters
func TestGrepLineTruncation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_truncation")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir

	// Create a file with a very long line
	longLine := strings.Repeat("x", 600) + " pattern"
	os.WriteFile(filepath.Join(tempDir, "longline.txt"), []byte(longLine), 0644)

	grepArgs := map[string]any{
		"regex":       "pattern",
		"targetpaths": []any{"longline.txt"},
	}
	grepResult := handleGrep(grepArgs)

	// Should contain the truncation marker
	if !strings.Contains(grepResult, "[TRUNCATED]") {
		t.Errorf("Should truncate long lines and add marker")
	}
	// Should include file and line number
	if !strings.Contains(grepResult, "longline.txt:1:") {
		t.Errorf("Should still include file and line number")
	}
}

// Output Modes
func TestGrepOutputModes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_output_modes")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir

	os.WriteFile(filepath.Join(tempDir, "test1.txt"), []byte("match1\nmatch1"), 0644)
	os.WriteFile(filepath.Join(tempDir, "test2.txt"), []byte("match1"), 0644)

	// Test files_with_matches mode
	grepArgs := map[string]any{
		"regex":       "match1",
		"targetpaths": []any{"."},
		"OutputMode":  "files_with_matches",
	}
	grepResult := handleGrep(grepArgs)
	// Should return only filenames, not line numbers
	if strings.Contains(grepResult, ":1:") {
		t.Errorf("files_with_matches should not include line numbers")
	}
	if !strings.Contains(grepResult, "test1.txt") {
		t.Errorf("Should contain test1.txt")
	}

	// Test count mode
	grepArgs = map[string]any{
		"regex":       "match1",
		"targetpaths": []any{"."},
		"OutputMode":  "count",
	}
	grepResult = handleGrep(grepArgs)
	if !strings.Contains(grepResult, "Total matches:") {
		t.Errorf("count mode should return total matches, got: %q", grepResult)
	}
}

// Limit and Offset pagination
func TestGrepPagination(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_pagination")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir

	// Create a file with 10 matches
	content := strings.Repeat("match\n", 10)
	os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte(content), 0644)

	// Test limit
	grepArgs := map[string]any{
		"regex":       "match",
		"targetpaths": []any{"test.txt"},
		"Limit":       float64(3),
	}
	grepResult := handleGrep(grepArgs)
	matchCount := strings.Count(grepResult, "test.txt:")
	if matchCount != 3 {
		t.Errorf("Limit should restrict results to 3, got %d", matchCount)
	}

	// Test offset
	grepArgs = map[string]any{
		"regex":       "match",
		"targetpaths": []any{"test.txt"},
		"Offset":      float64(5),
		"Limit":       float64(10),
	}
	grepResult = handleGrep(grepArgs)
	lines := strings.Split(strings.TrimSpace(grepResult), "\n")
	if len(lines) > 0 && lines[0] != "" {
		// First result should be line 6 (offset of 5 means skip first 5)
		if !strings.Contains(lines[0], ":6:") {
			t.Errorf("Offset should skip first 5 matches, first result should be line 6, got: %q", lines[0])
		}
	}
}

// Glob filtering
func TestGrepGlobFiltering(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_glob_filter")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir

	os.WriteFile(filepath.Join(tempDir, "test.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(tempDir, "test_test.go"), []byte("package main"), 0644)

	// Test glob filter for .go files
	grepArgs := map[string]any{
		"regex":       "package",
		"targetpaths": []any{"."},
		"Glob":        "*.go",
	}
	grepResult := handleGrep(grepArgs)

	if !strings.Contains(grepResult, "test.go") {
		t.Errorf("Should match test.go with *.go pattern")
	}
	if strings.Contains(grepResult, "test.txt") {
		t.Errorf("Should not match test.txt with *.go pattern")
	}
}

// Context lines (Before/After)
func TestGrepContextLines(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_context")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	currentCWD = tempDir

	content := "line1\nline2\nmatch\nline4\nline5"
	os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte(content), 0644)

	// Test Before context
	grepArgs := map[string]any{
		"regex":       "match",
		"targetpaths": []any{"test.txt"},
		"Before":      float64(1),
	}
	grepResult := handleGrep(grepArgs)

	if !strings.Contains(grepResult, "line2") {
		t.Errorf("Should include line before match")
	}
	if !strings.Contains(grepResult, ":3:") {
		t.Errorf("Should include the match line")
	}

	// Test After context
	grepArgs = map[string]any{
		"regex":       "match",
		"targetpaths": []any{"test.txt"},
		"After":       float64(1),
	}
	grepResult = handleGrep(grepArgs)

	if !strings.Contains(grepResult, "line4") {
		t.Errorf("Should include line after match")
	}

	// Test Context (should set both Before and After)
	grepArgs = map[string]any{
		"regex":       "match",
		"targetpaths": []any{"test.txt"},
		"Context":     float64(1),
	}
	grepResult = handleGrep(grepArgs)

	if !strings.Contains(grepResult, "line2") || !strings.Contains(grepResult, "line4") {
		t.Errorf("Context should set both before and after")
	}
}
