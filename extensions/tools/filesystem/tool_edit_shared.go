package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/smasonuk/falken-core/pkg/pluginsdk"

	"github.com/agnivade/levenshtein"
)

type editRequest struct {
	Path       string
	OldString  string
	NewString  string
	ReplaceAll bool
}

type editApplicationResult struct {
	NewContent string
	Diff       string
	UsedFuzzy  bool
	Count      int
}

type editableFile struct {
	ContentBytes []byte
	Content      string
	ModTime      time.Time
}

func parseEditRequest(args map[string]any) (editRequest, string) {
	pathStr, _ := args["Path"].(string)
	if pathStr == "" {
		pathStr, _ = args["path"].(string)
	}
	oldString, _ := args["OldString"].(string)
	if oldString == "" {
		oldString, _ = args["oldstring"].(string)
	}
	newString, _ := args["NewString"].(string)
	if newString == "" {
		newString, _ = args["newstring"].(string)
	}
	replaceAll, _ := args["ReplaceAll"].(bool)
	if !replaceAll {
		replaceAll, _ = args["replaceall"].(bool)
	}

	if pathStr == "" || oldString == "" {
		return editRequest{}, "error: Path and OldString are required"
	}

	return editRequest{
		Path:       pathStr,
		OldString:  oldString,
		NewString:  newString,
		ReplaceAll: replaceAll,
	}, ""
}

func loadEditableFile(path string) (editableFile, string) {
	info, err := os.Stat(path)
	if err != nil {
		return editableFile{}, formatFSError(err, path)
	}

	const maxFileSize = 5 * 1024 * 1024
	if info.Size() > maxFileSize {
		return editableFile{}, fmt.Sprintf("error: file is too large to edit (%d bytes). Maximum allowed size is 5MB.", info.Size())
	}

	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return editableFile{}, formatFSError(err, path)
	}

	return editableFile{
		ContentBytes: contentBytes,
		Content:      string(contentBytes),
		ModTime:      info.ModTime(),
	}, ""
}

func applyEditToContent(req editRequest, content string) (editApplicationResult, string) {
	newString := normalizeTrailingWhitespace(req.Path, req.NewString)

	for _, re := range pluginsdk.SecretPatterns {
		if re.MatchString(newString) {
			return editApplicationResult{}, "ERROR: PERMISSION_DENIED. Attempted to write a blocked secret (e.g., API key or private key) into the file. Do not hardcode secrets."
		}
	}

	var matches []string
	var appliedDesanitization map[string]string
	var usedOldString string

	strategies := []func(string, string) []string{
		func(c, s string) []string {
			match := findActualString(c, s)
			if match != "" {
				cnt := strings.Count(c, match)
				var res []string
				for i := 0; i < cnt; i++ {
					res = append(res, match)
				}
				return res
			}
			return nil
		},
		func(c, s string) []string {
			desanitizedOld, applied := desanitizeMatchString(s)
			match := findActualString(c, desanitizedOld)
			if match != "" {
				appliedDesanitization = applied
				cnt := strings.Count(c, match)
				var res []string
				for i := 0; i < cnt; i++ {
					res = append(res, match)
				}
				usedOldString = desanitizedOld
				return res
			}
			return nil
		},
		func(c, s string) []string {
			words := strings.Fields(s)
			if len(words) == 0 {
				return nil
			}
			for i, w := range words {
				words[i] = regexp.QuoteMeta(w)
			}
			pattern := strings.Join(words, `\s+`)
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil
			}
			return re.FindAllString(c, -1)
		},
		func(c, s string) []string {
			contentLines := strings.Split(c, "\n")
			searchLines := strings.Split(s, "\n")
			if len(searchLines) < 3 {
				return nil
			}

			firstLine := strings.TrimSpace(searchLines[0])
			lastLine := strings.TrimSpace(searchLines[len(searchLines)-1])

			var blockMatches []string
			for i := 0; i < len(contentLines); i++ {
				if strings.TrimSpace(contentLines[i]) != firstLine {
					continue
				}
				for j := i + 2; j < len(contentLines); j++ {
					if strings.TrimSpace(contentLines[j]) == lastLine {
						block := strings.Join(contentLines[i:j+1], "\n")
						dist := levenshtein.ComputeDistance(s, block)
						maxLen := len(s)
						if len(block) > maxLen {
							maxLen = len(block)
						}
						similarity := 1.0 - (float64(dist) / float64(maxLen))

						if similarity > 0.85 {
							blockMatches = append(blockMatches, block)
						}
						break
					}
				}
			}
			return blockMatches
		},
	}

	actualOldString := ""
	usedFuzzyMatch := false
	for i, strategy := range strategies {
		res := strategy(content, req.OldString)
		if len(res) == 1 {
			actualOldString = res[0]
			matches = res
			if i >= 2 {
				usedFuzzyMatch = true
				if i == 2 {
					newString = matchIndentation(actualOldString, newString)
				}
			}
			break
		} else if len(res) > 1 {
			matches = res
			actualOldString = res[0]
			if i >= 2 {
				usedFuzzyMatch = true
			}
			break
		}
	}

	if len(matches) == 0 {
		normalizedContent := normalizeWhitespace(content)
		normalizedOld := normalizeWhitespace(req.OldString)
		if strings.Count(normalizedContent, normalizedOld) > 1 {
			return editApplicationResult{}, "error: Your OldString matches multiple locations in the file when ignoring whitespace. Please provide more surrounding context lines to make it unique."
		}
		return editApplicationResult{}, "error: OldString not found in file. Check for typos or verify you are using the correct file content."
	}

	if len(matches) > 1 && !req.ReplaceAll {
		return editApplicationResult{}, "error: OldString matches multiple locations. Set ReplaceAll to true, or provide more context in OldString to make it unique."
	}

	oldString := req.OldString
	if appliedDesanitization != nil {
		for from, to := range appliedDesanitization {
			newString = strings.ReplaceAll(newString, from, to)
		}
		oldString = usedOldString
	}

	actualNewString := preserveQuoteStyle(oldString, actualOldString, newString)
	count := len(matches)

	if req.ReplaceAll {
		newContent := strings.ReplaceAll(content, actualOldString, actualNewString)
		return editApplicationResult{
			NewContent: newContent,
			Diff:       generateDiff(content, newContent, filepath.Base(req.Path)),
			UsedFuzzy:  usedFuzzyMatch,
			Count:      count,
		}, ""
	}

	if count == 1 {
		newContent := strings.Replace(content, actualOldString, actualNewString, 1)
		return editApplicationResult{
			NewContent: newContent,
			Diff:       generateDiff(content, newContent, filepath.Base(req.Path)),
			UsedFuzzy:  usedFuzzyMatch,
			Count:      count,
		}, ""
	}

	return editApplicationResult{}, "error: Unexpected error during replacement."
}
