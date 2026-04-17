package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/smasonuk/falken-core/pkg/pluginsdk"
)

func handleWriteFile(args map[string]any) string {
	pathStr, _ := args["Path"].(string)
	if pathStr == "" {
		pathStr, _ = args["path"].(string)
	}
	contentStr, _ := args["Content"].(string)
	if contentStr == "" {
		contentStr, _ = args["content"].(string)
	}
	if pathStr == "" {
		return "error: Path parameter is required"
	}

	var err error
	pathStr, err = resolvePath(pathStr)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	// Capture old content for backup and diff (if the file already exists)
	var oldContent string
	if oldBytes, err := os.ReadFile(pathStr); err == nil {
		oldContent = string(oldBytes)
		// Trigger pre-write backup
		if err := pluginsdk.BackupFile(pathStr, oldBytes); err != nil {
			return fmt.Sprintf("error creating backup: %v", err)
		}
	}

	err = writeFilePreservingMode(pathStr, []byte(contentStr), defaultNewFileMode)
	if err != nil {
		return formatFSError(err, pathStr)
	}

	// Generate and return diff
	diffStr := generateDiff(oldContent, contentStr, filepath.Base(pathStr))

	if diffStr != "" && oldContent != "" {
		return fmt.Sprintf("Successfully overwrote %s\n\n%s", pathStr, diffStr)
	} else if diffStr != "" {
		return fmt.Sprintf("Successfully wrote new file %s\n\n%s", pathStr, diffStr)
	}

	return fmt.Sprintf("Successfully wrote to %s", pathStr)
}
