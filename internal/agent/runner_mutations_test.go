package agent

import (
	"encoding/json"
	"testing"

	"github.com/smasonuk/falken-core/internal/extensions/tools"
)

func TestRunnerMutationObservationalCommands(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: "ls", want: true},
		{command: "git status", want: true},
		{command: "ls | cat", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := isObservationalCommand(tt.command); got != tt.want {
				t.Fatalf("isObservationalCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestRunnerMutationObservedForCreatedWriteFile(t *testing.T) {
	result := runnerMutationToolResult(t, "write_file", map[string]any{
		"success": true,
		"status":  "created",
		"created": true,
	})

	if !workspaceMutationObserved(result, tools.Entry{}) {
		t.Fatalf("workspaceMutationObserved(write_file created) = false, want true")
	}
}

func TestRunnerMutationNotObservedForObservationalCommand(t *testing.T) {
	result := runnerMutationToolResult(t, "execute_command", map[string]any{
		"success":  true,
		"status":   "succeeded",
		"executed": true,
		"command":  "git status",
	})

	if workspaceMutationObserved(result, tools.Entry{}) {
		t.Fatalf("workspaceMutationObserved(git status) = true, want false")
	}
}

func runnerMutationToolResult(t *testing.T, name string, payload map[string]any) ToolResult {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return ToolResult{
		CallID:  "call-" + name,
		Name:    name,
		Payload: data,
	}
}
