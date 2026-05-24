package agent

import (
	"encoding/json"
	"testing"

	"github.com/smasonuk/falken-core/internal/extensions/tools"
)

func TestIsObservationalCommandHeuristic(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: "ls", want: true},
		{command: "pwd", want: true},
		{command: "cat README.md", want: true},
		{command: "grep foo README.md", want: true},
		{command: "git status", want: true},
		{command: "git diff"},
		{command: "go test ./..."},
		{command: "npm test"},
		{command: "make check"},
		{command: "touch tmp.txt"},
		{command: "rm tmp.txt"},
		{command: "echo hello", want: true},
		{command: "echo hello && echo done"},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := isObservationalCommand(tt.command); got != tt.want {
				t.Fatalf("isObservationalCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestWorkspaceMutationObservedCommandHeuristic(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: "ls"},
		{command: "grep foo README.md"},
		{command: "git status"},
		{command: "go test ./...", want: true},
		{command: "npm test", want: true},
		{command: "make check", want: true},
		{command: "touch tmp.txt", want: true},
		{command: "rm tmp.txt", want: true},
		{command: "echo hello && echo done", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := workspaceMutationObserved(successfulCommandResult(t, tt.command), tools.Entry{})
			if got != tt.want {
				t.Fatalf("workspaceMutationObserved(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestShouldNudgeTodoProgressHeuristic(t *testing.T) {
	tests := []struct {
		name                    string
		result                  ToolResult
		workspaceMutated        bool
		workspaceMutatedThisRun bool
		want                    bool
	}{
		{name: "read tool", result: successfulToolResult(t, "read_file", map[string]any{"success": true})},
		{name: "workspace mutation", result: successfulToolResult(t, "write_file", map[string]any{"success": true}), workspaceMutated: true, want: true},
		{name: "verification command", result: successfulCommandResult(t, "go test ./..."), workspaceMutated: true, want: true},
		{name: "observational command", result: successfulCommandResult(t, "ls")},
		{name: "observational command after mutation", result: successfulCommandResult(t, "ls"), workspaceMutatedThisRun: true, want: true},
		{name: "compound command", result: successfulCommandResult(t, "echo hello && echo done"), workspaceMutated: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldNudgeTodoProgress(tt.result, tt.workspaceMutated, tt.workspaceMutatedThisRun)
			if got != tt.want {
				t.Fatalf("shouldNudgeTodoProgress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func successfulCommandResult(t *testing.T, command string) ToolResult {
	t.Helper()
	return successfulToolResult(t, "execute_command", map[string]any{
		"success":  true,
		"executed": true,
		"command":  command,
	})
}

func successfulToolResult(t *testing.T, name string, payload map[string]any) ToolResult {
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
