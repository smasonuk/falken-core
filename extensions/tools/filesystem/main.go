package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/smasonuk/falken-core/pkg/workspacepath"
)

type PluginPayload struct {
	Command       string         `json:"command"`
	Args          map[string]any `json:"args"`
	RealCWD       string         `json:"cwd"`
	WorkspaceRoot string         `json:"workspace_root"`
}

var currentCWD string
var workspaceRoot string

func main() {
	inputBytes, _ := io.ReadAll(os.Stdin)

	var payload PluginPayload
	if err := json.Unmarshal(inputBytes, &payload); err != nil {
		fmt.Printf(`{"result": "error: failed to parse JSON payload"}` + "\n")
		return
	}

	currentCWD = payload.RealCWD
	workspaceRoot = payload.WorkspaceRoot
	if workspaceRoot == "" {
		workspaceRoot = currentCWD
	}

	var result string
	switch payload.Command {
	case "read_file":
		result = handleReadFile(payload.Args)
	case "write_file":
		result = handleWriteFile(payload.Args)

	case "edit_file":
		result = handleEditFile(payload.Args)
	case "multi_edit":
		result = handleMultiEdit(payload.Args)
	case "apply_patch":
		result = handleApplyPatch(payload.Args)

	case "glob":
		result = handleGlob(payload.Args)
	case "grep":
		result = handleGrep(payload.Args)
	default:
		result = fmt.Sprintf("error: unknown tool command '%s'", payload.Command)
	}

	outBytes, _ := json.Marshal(map[string]string{"result": result})
	fmt.Print(string(outBytes))
}

func resolvePath(path string) (string, error) {
	// SECURITY: Block Windows UNC paths to prevent NTLM credential leaks
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, `//`) {
		return "", fmt.Errorf("UNC paths are not permitted for security reasons")
	}
	if currentCWD == "" {
		return path, nil
	}
	effectiveWorkspaceRoot := workspaceRoot
	if effectiveWorkspaceRoot == "" {
		effectiveWorkspaceRoot = currentCWD
	}
	if rel, err := filepath.Rel(effectiveWorkspaceRoot, currentCWD); err != nil ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		effectiveWorkspaceRoot = currentCWD
	}
	return workspacepath.Resolve(effectiveWorkspaceRoot, currentCWD, path)
}

func normalizeWhitespace(s string) string {
	re := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(re.ReplaceAllString(s, " "))
}

func formatFSError(err error, path string) string {
	if os.IsPermission(err) || strings.Contains(err.Error(), "errno 13") {
		return "ERROR: PERMISSION_DENIED. The user has explicitly rejected your request to access this file for security reasons. DO NOT retry."
	}
	if os.IsNotExist(err) || strings.Contains(err.Error(), "errno 2") {
		return fmt.Sprintf("ERROR: File not found at %s. Verify the path exists before retrying.", path)
	}
	return fmt.Sprintf("error: %v", err)
}
