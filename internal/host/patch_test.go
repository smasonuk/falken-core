package host

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

func TestApplyChanges_SkipsBlockedModification(t *testing.T) {
	realDir := t.TempDir()
	sandboxDir := t.TempDir()
	rel := "secret.txt"

	writeTestFile(t, filepath.Join(realDir, rel), "host-secret\n")
	writeTestFile(t, filepath.Join(sandboxDir, rel), "sandbox-secret\n")

	diff := `diff --git a/secret.txt b/secret.txt
index 1111111..2222222 100644
--- a/secret.txt
+++ b/secret.txt
@@ -1 +1 @@
-host-secret
+sandbox-secret
`

	result, err := ApplyChanges(realDir, sandboxDir, diff, []string{"secret.txt"})
	if err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}
	if !result.GuardrailTriggered {
		t.Fatalf("expected guardrail to trigger")
	}
	if len(result.SkippedFiles) != 1 || result.SkippedFiles[0] != "secret.txt" {
		t.Fatalf("expected skipped file list to include secret.txt, got %#v", result.SkippedFiles)
	}
	if got := readTestFile(t, filepath.Join(realDir, rel)); got != "host-secret\n" {
		t.Fatalf("expected blocked host file to remain unchanged, got %q", got)
	}
}

func TestApplyChanges_SkipsBlockedDeletion(t *testing.T) {
	realDir := t.TempDir()
	sandboxDir := t.TempDir()
	rel := "secret.txt"

	writeTestFile(t, filepath.Join(realDir, rel), "host-secret\n")

	diff := `diff --git a/secret.txt b/secret.txt
deleted file mode 100644
index 1111111..0000000
--- a/secret.txt
+++ /dev/null
@@ -1 +0,0 @@
-host-secret
`

	result, err := ApplyChanges(realDir, sandboxDir, diff, []string{"secret.txt"})
	if err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}
	if !result.GuardrailTriggered {
		t.Fatalf("expected guardrail to trigger")
	}
	if _, err := os.Stat(filepath.Join(realDir, rel)); err != nil {
		t.Fatalf("expected blocked file deletion to be skipped, stat err=%v", err)
	}
}

func TestApplyChanges_SkipsBlockedRename(t *testing.T) {
	realDir := t.TempDir()
	sandboxDir := t.TempDir()
	oldRel := "secret.txt"
	newRel := "secret-renamed.txt"

	writeTestFile(t, filepath.Join(realDir, oldRel), "host-secret\n")
	writeTestFile(t, filepath.Join(sandboxDir, newRel), "sandbox-secret\n")

	diff := `diff --git a/secret.txt b/secret-renamed.txt
similarity index 100%
rename from secret.txt
rename to secret-renamed.txt
`

	result, err := ApplyChanges(realDir, sandboxDir, diff, []string{"secret.txt", "secret-renamed.txt"})
	if err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}
	if !result.GuardrailTriggered {
		t.Fatalf("expected guardrail to trigger")
	}
	if len(result.SkippedFiles) != 2 || result.SkippedFiles[0] != "secret.txt" || result.SkippedFiles[1] != "secret-renamed.txt" {
		t.Fatalf("expected skipped rename files, got %#v", result.SkippedFiles)
	}
	if _, err := os.Stat(filepath.Join(realDir, oldRel)); err != nil {
		t.Fatalf("expected blocked old path to remain, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(realDir, newRel)); !os.IsNotExist(err) {
		t.Fatalf("expected blocked rename target to be skipped, stat err=%v", err)
	}
}

func TestApplyChanges_AllowsUnblockedModification(t *testing.T) {
	realDir := t.TempDir()
	sandboxDir := t.TempDir()
	rel := "allowed.txt"

	writeTestFile(t, filepath.Join(realDir, rel), "host-value\n")
	writeTestFile(t, filepath.Join(sandboxDir, rel), "sandbox-value\n")

	diff := `diff --git a/allowed.txt b/allowed.txt
index 1111111..2222222 100644
--- a/allowed.txt
+++ b/allowed.txt
@@ -1 +1 @@
-host-value
+sandbox-value
`

	result, err := ApplyChanges(realDir, sandboxDir, diff, []string{"secret.txt"})
	if err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}
	if result.GuardrailTriggered {
		t.Fatalf("did not expect guardrail for allowed file")
	}
	if got := readTestFile(t, filepath.Join(realDir, rel)); got != "sandbox-value\n" {
		t.Fatalf("expected allowed file to update, got %q", got)
	}
}

func TestParseDiffFiles_SingleFile(t *testing.T) {
	diff := `diff --git a/foo.txt b/foo.txt
index 1111111..2222222 100644
--- a/foo.txt
+++ b/foo.txt
`

	files := ParseDiffFiles(diff)
	if len(files) != 1 || files[0] != "foo.txt" {
		t.Fatalf("expected [foo.txt], got %#v", files)
	}
}

func TestParseDiffFiles_MultiFile(t *testing.T) {
	diff := `diff --git a/foo.txt b/foo.txt
index 1111111..2222222 100644
--- a/foo.txt
+++ b/foo.txt
diff --git a/bar.txt b/bar.txt
index 3333333..4444444 100644
--- a/bar.txt
+++ b/bar.txt
`

	files := ParseDiffFiles(diff)
	if len(files) != 2 || files[0] != "foo.txt" || files[1] != "bar.txt" {
		t.Fatalf("expected [foo.txt bar.txt], got %#v", files)
	}
}

func TestParseDiffFiles_Rename(t *testing.T) {
	diff := `diff --git a/old.txt b/new.txt
similarity index 100%
rename from old.txt
rename to new.txt
`

	files := ParseDiffFiles(diff)
	if len(files) != 1 || files[0] != "new.txt" {
		t.Fatalf("expected [new.txt], got %#v", files)
	}
}
