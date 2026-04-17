package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func handleGlob(args map[string]any) string {
	pattern, _ := args["Pattern"].(string)
	if pattern == "" {
		pattern, _ = args["pattern"].(string)
	}
	if pattern == "" {
		return "error: Pattern parameter is required"
	}

	root := currentCWD
	if pathVal, ok := args["Path"].(string); ok && pathVal != "" {
		root = pathVal
	} else if pathVal, ok := args["path"].(string); ok && pathVal != "" {
		root = pathVal
	}
	if root == "" {
		root = "."
	}

	resolvedRoot, err := resolvePath(root)
	if err != nil {
		return fmt.Sprintf("error resolving path: %v", err)
	}

	var matches []string
	// Simple implementation for ** support
	if strings.Contains(pattern, "**") {
		// Convert glob to regex
		regexPattern := strings.ReplaceAll(pattern, ".", "\\.")
		regexPattern = strings.ReplaceAll(regexPattern, "**/", "(.+/)?")
		regexPattern = strings.ReplaceAll(regexPattern, "**", ".*")
		regexPattern = strings.ReplaceAll(regexPattern, "*", "[^/]*")
		regexPattern = "^" + regexPattern + "$"
		re, err := regexp.Compile(regexPattern)
		if err != nil {
			return fmt.Sprintf("error: invalid pattern: %v", err)
		}

		filepath.WalkDir(resolvedRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "node_modules" || name == "vendor" || name == "__pycache__" || name == ".next" {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(resolvedRoot, path)
			if rel == "." {
				return nil
			}
			if re.MatchString(rel) {
				matches = append(matches, rel)
			}
			return nil
		})
	} else {
		var err error
		fullPattern := pattern
		if !filepath.IsAbs(pattern) && resolvedRoot != "" {
			fullPattern = filepath.Join(resolvedRoot, pattern)
		}
		rawMatches, err := filepath.Glob(fullPattern)
		if err != nil {
			return fmt.Sprintf("error during glob: %v", err)
		}
		for _, m := range rawMatches {
			rel, err := filepath.Rel(resolvedRoot, m)
			if err == nil {
				// Filter out .git manually for non-recursive glob if needed,
				// but Glob usually doesn't cross boundaries unless specified.
				if !strings.HasPrefix(rel, ".git") {
					matches = append(matches, rel)
				}
			} else {
				if !strings.HasPrefix(m, ".git") {
					matches = append(matches, m)
				}
			}
		}
	}

	if len(matches) == 0 {
		return "No matches found."
	}

	// Struct to hold path and mod time for sorting
	type fileMatch struct {
		path    string
		modTime int64
	}

	var sortedMatches []fileMatch
	for _, m := range matches {
		absPath := filepath.Join(resolvedRoot, m)
		info, err := os.Stat(absPath)
		var modTime int64
		if err == nil {
			modTime = info.ModTime().UnixNano()
		}
		sortedMatches = append(sortedMatches, fileMatch{path: m, modTime: modTime})
	}

	// Sort newest first (descending)
	sort.Slice(sortedMatches, func(i, j int) bool {
		return sortedMatches[i].modTime > sortedMatches[j].modTime
	})

	limit := 100
	truncated := false
	if len(sortedMatches) > limit {
		sortedMatches = sortedMatches[:limit]
		truncated = true
	}

	var finalPaths []string
	for _, sm := range sortedMatches {
		finalPaths = append(finalPaths, sm.path)
	}

	resultStr := strings.Join(finalPaths, "\n")
	if truncated {
		resultStr += "\n(Results are truncated. Consider using a more specific path or pattern.)"
	}

	return resultStr
}
