package agent

import (
	"encoding/json"
	"strings"

	"github.com/smasonuk/falken-core/internal/extensions/tools"
)

type toolMutationPayload struct {
	Success                 *bool  `json:"success"`
	Status                  string `json:"status"`
	Executed                bool   `json:"executed"`
	Changed                 bool   `json:"changed"`
	Created                 bool   `json:"created"`
	Overwritten             bool   `json:"overwritten"`
	Deleted                 bool   `json:"deleted"`
	FilesChanged            int    `json:"files_changed"`
	MutationMayHaveOccurred bool   `json:"mutation_may_have_occurred"`
	RollbackAttempted       bool   `json:"rollback_attempted"`
	RollbackSucceeded       bool   `json:"rollback_succeeded"`
	WorkspaceMutation       struct {
		Observed          bool `json:"observed"`
		MayHaveOccurred   bool `json:"may_have_occurred"`
		FilesChanged      int  `json:"files_changed"`
		RollbackAttempted bool `json:"rollback_attempted"`
		RollbackSucceeded bool `json:"rollback_succeeded"`
	} `json:"workspace_mutation"`
}

// observationalShellCommands names a small set of commands treated as
// read-only for dirty-tracking efficiency. This is a heuristic only.
// Commands outside this list are conservatively treated as potentially
// mutating when they succeed.
var observationalShellCommands = map[string]struct{}{
	"ls":       {},
	"pwd":      {},
	"echo":     {},
	"cat":      {},
	"grep":     {},
	"head":     {},
	"tail":     {},
	"wc":       {},
	"stat":     {},
	"file":     {},
	"which":    {},
	"whereis":  {},
	"type":     {},
	"basename": {},
	"dirname":  {},
	"true":     {},
	"false":    {},
}

func mutationPayload(result ToolResult) toolMutationPayload {
	if len(result.Payload) == 0 {
		return toolMutationPayload{}
	}
	var payload toolMutationPayload
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		return toolMutationPayload{}
	}
	return payload
}

func workspaceMutationObserved(result ToolResult, entry tools.Entry) bool {
	payload := mutationPayload(result)

	if result.Name == "execute_command" {
		if !payload.Executed {
			return false
		}
		if !toolResultSucceeded(result) {
			// Failed commands may have partial side effects, but historically
			// Falken only marked dirty on success. Preserve that behavior.
			return false
		}
		return !isObservationalCommand(payloadCommand(result))
	}

	if payload.WorkspaceMutation.Observed ||
		payload.WorkspaceMutation.MayHaveOccurred ||
		payload.WorkspaceMutation.FilesChanged > 0 ||
		(payload.WorkspaceMutation.RollbackAttempted && !payload.WorkspaceMutation.RollbackSucceeded) {
		return true
	}
	if payload.MutationMayHaveOccurred ||
		payload.FilesChanged > 0 ||
		(payload.RollbackAttempted && !payload.RollbackSucceeded) {
		return true
	}
	switch result.Name {
	case "write_file":
		return toolResultSucceeded(result) && (payload.Created || payload.Overwritten || payload.Status == "created" || payload.Status == "overwritten")
	case "delete_file":
		return toolResultSucceeded(result) && (payload.Deleted || payload.Status == "deleted")
	case "edit_file":
		return toolResultSucceeded(result) && payload.Changed && payload.Status != "unchanged"
	case "multi_edit":
		return payload.Status != "no_changes" && payload.FilesChanged > 0
	case "apply_patch":
		return toolResultSucceeded(result) && !(payload.RollbackAttempted && payload.RollbackSucceeded)
	default:
		return entry.Safety.MutatesWorkspace && toolResultSucceeded(result)
	}
}

func isManagedWorkspaceMutationTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "multi_edit", "apply_patch", "delete_file":
		return true
	default:
		return false
	}
}

func isObservationalCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}

	// Compound commands, pipelines, substitutions, redirections, and newlines
	// fall back to conservative dirty tracking.
	if strings.ContainsAny(command, "|;&$`(){}<>\n") {
		return false
	}

	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	first := fields[0]
	if first == "git" && len(fields) >= 2 && fields[1] == "status" {
		return true
	}

	_, ok := observationalShellCommands[first]
	return ok
}

func payloadCommand(result ToolResult) string {
	if len(result.Payload) == 0 {
		return ""
	}
	var p struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(result.Payload, &p); err != nil {
		return ""
	}
	return p.Command
}
