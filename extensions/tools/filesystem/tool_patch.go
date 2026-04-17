package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/smasonuk/falken-core/pkg/pluginsdk"
)

func handleApplyPatch(args map[string]any) string {
	patchText, _ := args["PatchText"].(string)
	files, _, err := gitdiff.Parse(strings.NewReader(patchText))
	if err != nil {
		return fmt.Sprintf("error: Invalid patch format: %v", err)
	}

	var output strings.Builder
	for _, file := range files {
		oldPath, err := resolvePatchPath(file.OldName)
		if err != nil {
			output.WriteString(fmt.Sprintf("Failed to resolve %s: %v\n", file.OldName, err))
			continue
		}
		newPath, err := resolvePatchPath(file.NewName)
		if err != nil {
			output.WriteString(fmt.Sprintf("Failed to resolve %s: %v\n", file.NewName, err))
			continue
		}

		switch {
		case file.IsDelete:
			oldContent, err := os.ReadFile(oldPath)
			if err != nil {
				output.WriteString(fmt.Sprintf("Failed to read %s: %v\n", file.OldName, err))
				continue
			}
			if err := pluginsdk.BackupFile(oldPath, oldContent); err != nil {
				output.WriteString(fmt.Sprintf("Failed to back up %s: %v\n", file.OldName, err))
				continue
			}
			if err := os.Remove(oldPath); err != nil {
				output.WriteString(fmt.Sprintf("Failed to delete %s: %v\n", file.OldName, err))
				continue
			}
			output.WriteString(fmt.Sprintf("Successfully deleted %s\n", file.OldName))
			continue

		default:
			oldContent := []byte{}
			if !file.IsNew {
				oldContent, err = os.ReadFile(oldPath)
				if err != nil {
					output.WriteString(fmt.Sprintf("Failed to read %s: %v\n", file.OldName, err))
					continue
				}
				if err := pluginsdk.BackupFile(oldPath, oldContent); err != nil {
					output.WriteString(fmt.Sprintf("Failed to back up %s: %v\n", file.OldName, err))
					continue
				}
			}

			newContent := oldContent
			if len(file.TextFragments) > 0 || file.IsNew {
				var out bytes.Buffer
				err = gitdiff.Apply(&out, bytes.NewReader(oldContent), file)
				if err != nil {
					output.WriteString(fmt.Sprintf("Failed to patch %s: %v\n", file.NewName, err))
					continue
				}
				newContent = out.Bytes()
			}

			if newPath == "" {
				output.WriteString("Failed to determine destination path for patch\n")
				continue
			}
			if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
				output.WriteString(fmt.Sprintf("Failed to prepare %s: %v\n", file.NewName, err))
				continue
			}
			targetMode := file.NewMode
			if file.IsRename && oldPath != "" {
				targetMode = determineWriteMode(oldPath, targetMode)
			}
			if err := writeFilePreservingMode(newPath, newContent, targetMode); err != nil {
				output.WriteString(fmt.Sprintf("Failed to write %s: %v\n", file.NewName, err))
				continue
			}
			if file.IsRename && oldPath != "" && oldPath != newPath {
				if err := os.Remove(oldPath); err != nil {
					output.WriteString(fmt.Sprintf("Failed to remove renamed source %s: %v\n", file.OldName, err))
					continue
				}
				output.WriteString(fmt.Sprintf("Successfully renamed %s to %s\n", file.OldName, file.NewName))
				continue
			}
			output.WriteString(fmt.Sprintf("Successfully patched %s\n", file.NewName))
		}
	}
	return output.String()
}

func resolvePatchPath(name string) (string, error) {
	if name == "" || name == "/dev/null" {
		return "", nil
	}
	return resolvePath(name)
}
