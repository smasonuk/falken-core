package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smasonuk/falken-core/internal/runtimeapi"
)

type outputStore struct{}

func (o *outputStore) formatOutput(s *StatefulShell, output string) string {
	output = strings.TrimLeft(output, "\n\r")
	const maxChars = 10000

	if len(output) <= maxChars {
		return output
	}

	paths := runtimeapi.Paths{
		WorkspaceDir: s.WorkspaceDir,
		StateDir:     s.StateDir,
	}
	truncationDir := paths.TruncationDir()
	os.MkdirAll(truncationDir, 0755)

	fileName := fmt.Sprintf("output_%d.txt", time.Now().Unix())
	filePath := filepath.Join(truncationDir, fileName)
	_ = os.WriteFile(filePath, []byte(output), 0644)

	preview := output[:maxChars]
	linesHidden := strings.Count(output[maxChars:], "\n")

	displayPath := filePath
	if rel, err := filepath.Rel(paths.WorkspaceDir, filePath); err == nil && !strings.HasPrefix(rel, "..") {
		displayPath = rel
	}

	return fmt.Sprintf("%s\n\n[TRUNCATED: %d lines hidden. Full output was saved to internal session state at '%s'. Ask the host/user to inspect this file, or rerun a narrower command.]", preview, linesHidden, displayPath)
}
