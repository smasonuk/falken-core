package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/smasonuk/falken-core/pkg/pluginsdk"
)

func handleGrep(args map[string]any) string {
	regexStr, _ := args["Regex"].(string)
	if regexStr == "" {
		regexStr, _ = args["regex"].(string)
	}
	if regexStr == "" {
		return "error: Regex parameter is required"
	}

	pathsVal, ok := args["TargetPaths"]
	if !ok {
		pathsVal = args["targetpaths"]
	}

	if pathsVal == nil {
		return "error: TargetPaths parameter is required"
	}

	rawPaths, ok := pathsVal.([]any)
	if !ok {
		return "error: TargetPaths must be an array of strings"
	}

	re, err := regexp.Compile(regexStr)
	if err != nil {
		return fmt.Sprintf("error: invalid regex: %v", err)
	}

	// Parse output mode (default: content)
	outputMode, _ := args["OutputMode"].(string)
	if outputMode == "" {
		outputMode = "content"
	}
	if outputMode != "content" && outputMode != "files_with_matches" && outputMode != "count" {
		outputMode = "content"
	}

	// Parse limit and offset (defaults: limit=250, offset=0)
	limit := uint32(250)
	if limitVal, ok := args["Limit"]; ok {
		if parsed, valid := pluginsdk.NumberToUint32(limitVal); valid && parsed > 0 {
			limit = parsed
		}
	}

	offset := uint32(0)
	if offsetVal, ok := args["Offset"]; ok {
		if parsed, valid := pluginsdk.NumberToUint32(offsetVal); valid {
			offset = parsed
		}
	}

	// Parse glob pattern (optional)
	globPattern, _ := args["Glob"].(string)
	if globPattern == "" {
		globPattern, _ = args["glob"].(string)
	}

	// Parse context lines (Before, After, Context)
	contextBefore := uint32(0)
	if beforeVal, ok := args["Before"]; ok {
		if parsed, valid := pluginsdk.NumberToUint32(beforeVal); valid && parsed > 0 {
			if parsed > 10 {
				parsed = 10 // Cap at 10
			}
			contextBefore = parsed
		}
	}

	contextAfter := uint32(0)
	if afterVal, ok := args["After"]; ok {
		if parsed, valid := pluginsdk.NumberToUint32(afterVal); valid && parsed > 0 {
			if parsed > 10 {
				parsed = 10 // Cap at 10
			}
			contextAfter = parsed
		}
	}

	// Context parameter overrides Before and After
	if contextVal, ok := args["Context"]; ok {
		if parsed, valid := pluginsdk.NumberToUint32(contextVal); valid && parsed > 0 {
			if parsed > 10 {
				parsed = 10 // Cap at 10
			}
			contextBefore = parsed
			contextAfter = parsed
		}
	}

	var results []string
	var matchCount uint32
	var filesMatched map[string]bool = make(map[string]bool)

	searchFile := func(path string) error {
		resolvedPath, err := resolvePath(path)
		if err != nil {
			return nil
		}
		f, err := os.Open(resolvedPath)
		if err != nil {
			if os.IsPermission(err) || strings.Contains(err.Error(), "errno 13") {
				results = append(results, fmt.Sprintf("%s: PERMISSION DENIED", path))
			}
			return nil
		}
		defer f.Close()

		// Report path relative to CWD
		relPath, err := filepath.Rel(currentCWD, resolvedPath)
		if err != nil {
			relPath = path
		}

		// For files_with_matches mode, we only need to know if there's at least one match
		if outputMode == "files_with_matches" {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := scanner.Text()
				if re.MatchString(line) {
					// Found a match, add file and skip the rest
					if matchCount >= offset && len(results) < int(limit) {
						if !filesMatched[relPath] {
							filesMatched[relPath] = true
							results = append(results, relPath)
						}
					}
					matchCount++
					return nil // Exit this file early
				}
			}
			return nil
		}

		// Setup context buffers for "content" mode
		var beforeLines []string
		afterCount := uint32(0)

		scanner := bufio.NewScanner(f)
		lineNum := 1
		for scanner.Scan() {
			line := scanner.Text()
			isMatch := re.MatchString(line)

			if isMatch {
				matchCount++

				// Check if this match should be included based on offset
				if matchCount <= offset {
					// Reset context buffers without printing
					beforeLines = beforeLines[:0]
					afterCount = 0
					lineNum++
					continue
				}

				// Check if we've reached the limit
				if len(results) >= int(limit) {
					return fmt.Errorf("limit")
				}

				// Print context before lines
				for i, bline := range beforeLines {
					prevLineNum := lineNum - len(beforeLines) + i
					results = append(results, fmt.Sprintf("%s-%d-%s", relPath, prevLineNum, bline))
				}
				beforeLines = beforeLines[:0]

				// Truncate line if it exceeds 500 characters
				displayLine := line
				if len(displayLine) > 500 {
					displayLine = displayLine[:500] + " ...[TRUNCATED]"
				}

				// Print the actual match
				results = append(results, fmt.Sprintf("%s:%d: %s", relPath, lineNum, displayLine))

				// Start/Reset the after counter
				afterCount = contextAfter

			} else if afterCount > 0 {
				// Print after line
				results = append(results, fmt.Sprintf("%s-%d-%s", relPath, lineNum, line))
				afterCount--
			} else if contextBefore > 0 {
				// Maintain ring buffer of before lines
				if len(beforeLines) >= int(contextBefore) {
					beforeLines = beforeLines[1:] // pop oldest
				}
				beforeLines = append(beforeLines, line) // push newest
			}

			lineNum++

			// Check limit
			if len(results) >= int(limit) && afterCount == 0 {
				return fmt.Errorf("limit")
			}
		}
		return nil
	}

	for _, raw := range rawPaths {
		path, ok := raw.(string)
		if !ok || path == "" {
			continue
		}

		resolvedPath, err := resolvePath(path)
		if err != nil {
			continue
		}

		info, err := os.Stat(resolvedPath)
		if err != nil {
			continue
		}

		if info.IsDir() {
			err = filepath.WalkDir(resolvedPath, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					// Check if directory should be ignored
					ignoreDirs := map[string]bool{
						".git": true, ".svn": true, ".hg": true, ".bzr": true,
						"node_modules": true, "vendor": true, "__pycache__": true,
						".next": true, "dist": true, "build": true,
					}
					if ignoreDirs[d.Name()] {
						return filepath.SkipDir
					}
					return nil
				}

				// Skip non-text files based on extension
				ext := strings.ToLower(filepath.Ext(p))
				if ext == ".wasm" || ext == ".exe" || ext == ".bin" {
					return nil
				}

				// Apply glob filter if specified
				if globPattern != "" {
					relPath, _ := filepath.Rel(resolvedPath, p)
					matched, err := filepath.Match(globPattern, d.Name())
					if err == nil && !matched {
						// Try matching against the relative path
						matched, _ = filepath.Match(globPattern, relPath)
					}
					if !matched {
						return nil // Skip this file
					}
				}

				if searchFile(p) != nil {
					return fmt.Errorf("limit")
				}
				return nil
			})
		} else {
			// Single file case - apply glob filter
			if globPattern != "" {
				matched, err := filepath.Match(globPattern, info.Name())
				if err == nil && !matched {
					continue
				}
			}
			err = searchFile(resolvedPath)
		}

		if err != nil && err.Error() == "limit" {
			break
		}
	}

	// Handle count output mode
	if outputMode == "count" {
		return fmt.Sprintf("Total matches: %d", matchCount)
	}

	if len(results) == 0 {
		return "No matches found."
	}
	return strings.Join(results, "\n")
}
