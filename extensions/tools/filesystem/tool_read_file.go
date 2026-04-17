package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smasonuk/falken-core/pkg/pluginsdk"
)

// isBlockedDevicePath prevents the agent from reading infinite streams or blocking inputs
func isBlockedDevicePath(path string) bool {
	blocked := map[string]bool{
		"/dev/zero": true, "/dev/random": true, "/dev/urandom": true, "/dev/full": true,
		"/dev/stdin": true, "/dev/tty": true, "/dev/console": true,
		"/dev/stdout": true, "/dev/stderr": true,
		"/dev/fd/0": true, "/dev/fd/1": true, "/dev/fd/2": true,
	}
	if blocked[path] {
		return true
	}
	if strings.HasPrefix(path, "/proc/") && (strings.HasSuffix(path, "/fd/0") || strings.HasSuffix(path, "/fd/1") || strings.HasSuffix(path, "/fd/2")) {
		return true
	}
	return false
}

// findSimilarFile does a basic lookup for similarly named files to help the agent recover from typos
func findSimilarFile(targetPath string) string {
	dir := filepath.Dir(targetPath)
	base := strings.ToLower(filepath.Base(targetPath))

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "" // Fail silently, it's just a helper
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		entryName := strings.ToLower(entry.Name())
		// Basic substring match for typos or wrong extensions (e.g., .js vs .ts)
		if strings.Contains(entryName, base) || strings.Contains(base, entryName) {
			return filepath.Join(dir, entry.Name())
		}
	}
	return ""
}

func handleReadFile(args map[string]any) string {
	pathStr, _ := args["Path"].(string)
	if pathStr == "" {
		pathStr, _ = args["path"].(string)
	}
	if pathStr == "" {
		return "error: Path parameter is required"
	}

	var err error
	resolvedPath, err := resolvePath(pathStr)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	// Hang Prevention
	if isBlockedDevicePath(resolvedPath) {
		return fmt.Sprintf("error: Cannot read '%s': this device file would block or produce infinite output.", pathStr)
	}

	// Directory and Existence Guardrails
	info, err := os.Stat(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			suggestion := findSimilarFile(resolvedPath)
			msg := fmt.Sprintf("error: File does not exist at %s.", pathStr)
			if suggestion != "" {
				msg += fmt.Sprintf(" Did you mean %s?", suggestion)
			}
			return msg
		}
		return formatFSError(err, resolvedPath)
	}

	if info.IsDir() {
		return fmt.Sprintf("error: '%s' is a directory. This tool can only read files. Use the 'execute_command' tool with 'ls' or the 'glob' tool to view directory contents.", pathStr)
	}

	var startLine, endLine uint32
	if startVal, ok := args["StartLine"]; ok {
		if parsed, valid := pluginsdk.NumberToUint32(startVal); valid {
			startLine = parsed
		}
	} else if startVal, ok := args["startline"]; ok {
		if parsed, valid := pluginsdk.NumberToUint32(startVal); valid {
			startLine = parsed
		}
	}

	if endVal, ok := args["EndLine"]; ok {
		if parsed, valid := pluginsdk.NumberToUint32(endVal); valid {
			endLine = parsed
		}
	} else if endVal, ok := args["endline"]; ok {
		if parsed, valid := pluginsdk.NumberToUint32(endVal); valid {
			endLine = parsed
		}
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return formatFSError(err, resolvedPath)
	}

	// Mark file as read for the edit_file guardrail
	markFileAsRead(resolvedPath)

	// System Reminder for Empty Files
	if len(content) == 0 {
		return "<system-reminder>Warning: the file exists but the contents are empty.</system-reminder>"
	}

	lines := strings.Split(string(content), "\n")
	totalLines := uint32(len(lines))

	// No line limits provided
	if startLine == 0 && endLine == 0 {
		limit := totalLines
		truncated := false
		if len(content) > 10000 {
			limit = 500
			if totalLines < limit {
				limit = totalLines
			}
			truncated = true
		}
		result := formatNumberedLines(lines, 1, limit)
		if truncated {
			return result + "\n[TRUNCATED: File is too large. Use StartLine/EndLine to see more]"
		}
		return result
	}

	if startLine == 0 {
		startLine = 1
	}
	if endLine == 0 || endLine > totalLines {
		endLine = totalLines
	}

	// System Reminder for Out-of-Bounds
	if startLine > totalLines {
		return fmt.Sprintf("<system-reminder>Warning: the file exists but is shorter than the provided StartLine (%d). The file has %d lines.</system-reminder>", startLine, totalLines)
	}

	if startLine > endLine {
		return "error: StartLine cannot be greater than EndLine"
	}

	return formatNumberedLines(lines, startLine, endLine)
}

func formatNumberedLines(lines []string, startLine, endLine uint32) string {
	var sb strings.Builder
	for i := startLine; i <= endLine; i++ {
		fmt.Fprintf(&sb, "%d | %s\n", i, lines[i-1])
	}
	return strings.TrimRight(sb.String(), "\n")
}
