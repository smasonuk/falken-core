package main

import (
	"fmt"
	"os"
	"strings"
)

func handleReadFiles(args map[string]any) string {
	pathsVal, ok := args["Paths"]
	if !ok {
		pathsVal = args["paths"]
	}

	rawPaths, ok := pathsVal.([]any)
	if !ok || len(rawPaths) == 0 {
		return "error: Paths must be a non-empty array of strings"
	}

	var sb strings.Builder
	for _, raw := range rawPaths {
		path, ok := raw.(string)
		if !ok || path == "" {
			continue
		}

		resolvedPath, err := resolvePath(path)
		if err != nil {
			fmt.Fprintf(&sb, "--- FILE: %s ---\nError: %v\n\n", path, err)
			continue
		}
		content, err := os.ReadFile(resolvedPath)
		if err != nil {
			fmt.Fprintf(&sb, "--- FILE: %s ---\n%s\n\n", path, formatFSError(err, resolvedPath))
			continue
		}

		// Mark file as read for the edit_file guardrail
		markFileAsRead(resolvedPath)

		lines := strings.Split(string(content), "\n")
		fmt.Fprintf(&sb, "--- FILE: %s ---\n%s\n\n", path, formatNumberedLines(lines, 1, uint32(len(lines))))
	}

	return sb.String()
}
