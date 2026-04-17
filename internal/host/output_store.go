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

	truncationDir := runtimeapi.Paths{StateDir: s.StateDir}.TruncationDir()
	os.MkdirAll(truncationDir, 0755)

	fileName := fmt.Sprintf("output_%d.txt", time.Now().Unix())
	filePath := filepath.Join(truncationDir, fileName)
	_ = os.WriteFile(filePath, []byte(output), 0644)

	preview := output[:maxChars]
	linesHidden := strings.Count(output[maxChars:], "\n")
	relPath := filepath.Join(s.StateDir, "truncations", fileName)

	return fmt.Sprintf("%s\n\n[TRUNCATED: %d lines hidden. The full output was too large for your context window and has been saved to '%s'. Use the 'grep' or 'read_file' tool on this readable file to extract the specific information you need.]", preview, linesHidden, relPath)
}
