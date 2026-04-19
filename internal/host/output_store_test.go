package host

import (
	"strings"
	"testing"
)

func TestFormatOutputSanitization(t *testing.T) {
	tmp := t.TempDir()
	
	s := &StatefulShell{
		WorkspaceDir: tmp,
		StateDir:     tmp,
	}
	
	o := &outputStore{}
	
	// Create large output to trigger truncation
	largeOutput := strings.Repeat("Line\n", 3000) // Much more than 10000 chars
	
	formatted := o.formatOutput(s, largeOutput)
	
	// Check that it's truncated
	if !strings.Contains(formatted, "[TRUNCATED:") {
		t.Fatal("expected output to be truncated")
	}
	
	// Check for path leaks
	forbiddenMarkers := []string{
		".falken/state",
		"/state/current/",
		"truncations",
		tmp,
	}
	
	for _, forbidden := range forbiddenMarkers {
		if strings.Contains(formatted, forbidden) {
			t.Fatalf("output leaked forbidden marker %q:\n%s", forbidden, formatted)
		}
	}
	
	// Check that it contains the artifact name
	// fileName format: "output_%d.txt"
	if !strings.Contains(formatted, "output_") || !strings.Contains(formatted, ".txt") {
		t.Errorf("expected artifact name in truncated output, got %s", formatted)
	}
}
