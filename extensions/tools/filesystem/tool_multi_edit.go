package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smasonuk/falken-core/pkg/pluginsdk"
)

func handleMultiEdit(args map[string]any) string {
	editsRaw, ok := args["Edits"].([]any)
	if !ok {
		return "error: Edits array required"
	}

	grouped := make(map[string][]editRequest)
	var orderedPaths []string

	for _, raw := range editsRaw {
		editMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		req, errMsg := parseEditRequest(editMap)
		if errMsg != "" {
			return errMsg
		}

		resolvedPath, err := resolvePath(req.Path)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		req.Path = resolvedPath

		if _, exists := grouped[req.Path]; !exists {
			orderedPaths = append(orderedPaths, req.Path)
		}
		grouped[req.Path] = append(grouped[req.Path], req)
	}

	var results []string
	for _, path := range orderedPaths {
		if err := verifyFileRead(path); err != nil {
			results = append(results, fmt.Sprintf("--- Edit %s ---\n%s", path, err.Error()))
			continue
		}

		file, errMsg := loadEditableFile(path)
		if errMsg != "" {
			results = append(results, fmt.Sprintf("--- Edit %s ---\n%s", path, errMsg))
			continue
		}

		workingContent := file.Content
		usedFuzzy := false
		editCount := 0
		failed := false
		for _, req := range grouped[path] {
			result, errMsg := applyEditToContent(req, workingContent)
			if errMsg != "" {
				results = append(results, fmt.Sprintf("--- Edit %s ---\n%s", path, errMsg))
				failed = true
				break
			}
			workingContent = result.NewContent
			usedFuzzy = usedFuzzy || result.UsedFuzzy
			editCount++
		}
		if failed {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			results = append(results, fmt.Sprintf("--- Edit %s ---\n%s", path, formatFSError(err, path)))
			continue
		}
		if !info.ModTime().Equal(file.ModTime) {
			results = append(results, fmt.Sprintf("--- Edit %s ---\nRACE CONDITION GUARD: File has been modified by an external process since you last read it. Please read it again before attempting an edit.", path))
			continue
		}

		if err := pluginsdk.BackupFile(path, file.ContentBytes); err != nil {
			results = append(results, fmt.Sprintf("--- Edit %s ---\nerror creating backup: %v", path, err))
			continue
		}
		if err := os.WriteFile(path, []byte(workingContent), 0644); err != nil {
			results = append(results, fmt.Sprintf("--- Edit %s ---\n%s", path, formatFSError(err, path)))
			continue
		}

		diffStr := generateDiff(file.Content, workingContent, filepath.Base(path))
		msg := "Successfully edited %s (%d staged edits)\n\n%s"
		if usedFuzzy {
			msg = "Successfully edited %s (%d staged edits, fuzzy match succeeded)\n\n%s"
		}
		results = append(results, fmt.Sprintf(msg, path, editCount, diffStr))
	}
	return strings.Join(results, "\n\n")
}
