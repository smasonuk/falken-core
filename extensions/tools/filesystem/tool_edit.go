package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/smasonuk/falken-core/pkg/pluginsdk"
)

// matchIndentation ensures NewString gets the same base indentation as the original block
func matchIndentation(actualOld, newString string) string {
	// Find the leading whitespace of the first line of the actual matched string
	lines := strings.Split(actualOld, "\n")
	if len(lines) == 0 {
		return newString
	}

	firstLine := lines[0]
	leadingSpace := ""
	for _, r := range firstLine {
		if r == ' ' || r == '\t' {
			leadingSpace += string(r)
		} else {
			break
		}
	}

	// If the LLM forgot to indent NewString, prepend the leading space to its lines
	newLines := strings.Split(newString, "\n")
	for i, line := range newLines {
		// Don't indent completely empty lines
		if len(strings.TrimSpace(line)) > 0 {
			// Basic check: if the LLM didn't already include the indentation, add it
			if !strings.HasPrefix(line, leadingSpace) {
				newLines[i] = leadingSpace + line
			}
		}
	}

	return strings.Join(newLines, "\n")
}

// findFuzzyMatch ignores all whitespace differences between the search string and the file content.
func findFuzzyMatch(fileContent, searchString string) string {
	// Split by any whitespace (spaces, tabs, newlines)
	words := strings.Fields(searchString)
	if len(words) == 0 {
		return ""
	}

	// Escape regex meta-characters in the actual words
	for i, w := range words {
		words[i] = regexp.QuoteMeta(w)
	}

	// Rejoin with a pattern that matches ANY amount of whitespace
	pattern := strings.Join(words, `\s+`)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}

	// Find matches in the original file
	matches := re.FindAllString(fileContent, -1)

	// Only return a success if it uniquely identifies a single block
	if len(matches) == 1 {
		return matches[0]
	}

	return ""
}

func handleEditFile(args map[string]any) string {
	req, errMsg := parseEditRequest(args)
	if errMsg != "" {
		return errMsg
	}

	var err error
	req.Path, err = resolvePath(req.Path)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	// Enforce Read-Before-Write
	if err := verifyFileRead(req.Path); err != nil {
		return err.Error()
	}

	file, errMsg := loadEditableFile(req.Path)
	if errMsg != "" {
		return errMsg
	}

	result, errMsg := applyEditToContent(req, file.Content)
	if errMsg != "" {
		return errMsg
	}

	if err := pluginsdk.BackupFile(req.Path, file.ContentBytes); err != nil {
		return fmt.Sprintf("error creating backup: %v", err)
	}
	if err := os.WriteFile(req.Path, []byte(result.NewContent), 0644); err != nil {
		return formatFSError(err, req.Path)
	}

	if req.ReplaceAll {
		msg := "Successfully replaced %d occurrences in %s\n\n%s"
		if result.UsedFuzzy {
			msg = "Successfully replaced %d occurrences in %s (fuzzy match succeeded)\n\n%s"
		}
		return fmt.Sprintf(msg, result.Count, req.Path, result.Diff)
	}

	msg := "Successfully edited %s\n\n%s"
	if result.UsedFuzzy {
		msg = "Successfully edited %s (fuzzy match succeeded)\n\n%s"
	}
	return fmt.Sprintf(msg, req.Path, result.Diff)
}

func normalizeTrailingWhitespace(filename, content string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	// Markdown syntax relies on two trailing spaces for hard line breaks.
	// Stripping whitespace here would break document formatting.
	if ext == ".md" || ext == ".mdx" {
		return content
	}

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		// Preserve carriage return if it exists (CRLF handling)
		hasCR := strings.HasSuffix(line, "\r")
		if hasCR {
			line = line[:len(line)-1]
		}

		// Strip trailing spaces and tabs
		line = strings.TrimRight(line, " \t")

		if hasCR {
			line += "\r"
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}
