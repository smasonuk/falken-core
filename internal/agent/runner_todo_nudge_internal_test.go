package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestShouldNudgeTodoProgress(t *testing.T) {
	tests := []struct {
		name                    string
		result                  ToolResult
		workspaceMutated        bool
		workspaceMutatedThisRun bool
		want                    bool
	}{
		{
			name:             "successful edit with observed mutation",
			result:           successToolResult("edit_file", `{"success":true}`),
			workspaceMutated: true,
			want:             true,
		},
		{
			name:             "failed edit",
			result:           failedToolResult("edit_file"),
			workspaceMutated: true,
			want:             false,
		},
		{
			name:   "successful read file",
			result: successToolResult("read_file", `{"success":true}`),
			want:   false,
		},
		{
			name:   "successful write todos",
			result: successToolResult("write_todos", `{"success":true}`),
			want:   false,
		},
		{
			name:   "successful non observational command",
			result: successToolResult("execute_command", `{"success":true,"executed":true,"command":"python script.py"}`),
			want:   true,
		},
		{
			name:   "successful observational command before mutation",
			result: successToolResult("execute_command", `{"success":true,"executed":true,"command":"ls"}`),
			want:   false,
		},
		{
			name:                    "successful observational command after mutation",
			result:                  successToolResult("execute_command", `{"success":true,"executed":true,"command":"ls"}`),
			workspaceMutatedThisRun: true,
			want:                    true,
		},
		{
			name:   "successful compound command",
			result: successToolResult("execute_command", `{"success":true,"executed":true,"command":"ls foo && echo bar"}`),
			want:   true,
		},
		{
			name:   "successful unknown command",
			result: successToolResult("execute_command", `{"success":true,"executed":true,"command":"custom-tool --flag"}`),
			want:   true,
		},
		{
			name:   "missing command payload conservatively nudges",
			result: successToolResult("execute_command", `{"success":true,"executed":true}`),
			want:   true,
		},
		{
			name:             "missing command payload with observed workspace mutation",
			result:           successToolResult("execute_command", `{"success":true,"executed":true}`),
			workspaceMutated: true,
			want:             true,
		},
		{
			name:             "glob remains read only even if mutation flag is passed",
			result:           successToolResult("glob", `{"success":true}`),
			workspaceMutated: true,
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldNudgeTodoProgress(tt.result, tt.workspaceMutated, tt.workspaceMutatedThisRun)
			if got != tt.want {
				t.Fatalf("shouldNudgeTodoProgress = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkspaceMutationObserved(t *testing.T) {
	tests := []struct {
		name   string
		result ToolResult
		entry  tools.Entry
		want   bool
	}{
		{
			name:   "observed mutation metadata",
			result: successToolResult("write_file", `{"success":true,"workspace_mutation":{"observed":true}}`),
			want:   true,
		},
		{
			name:   "files changed metadata",
			result: successToolResult("write_file", `{"success":true,"workspace_mutation":{"files_changed":1}}`),
			want:   true,
		},
		{
			name:   "unchanged metadata",
			result: successToolResult("edit_file", `{"success":true,"workspace_mutation":{"observed":false,"files_changed":0}}`),
			entry:  tools.Entry{Name: "edit_file", Safety: tools.Safety{MutatesWorkspace: true}},
			want:   false,
		},
		{
			name:   "metadata absent falls back to declared workspace mutation",
			result: successToolResult("custom_write", `{"success":true}`),
			entry:  tools.Entry{Name: "custom_write", Safety: tools.Safety{MutatesWorkspace: true}},
			want:   true,
		},
		{
			name:   "successful ls does not mark dirty",
			result: successToolResult("execute_command", `{"success":true,"executed":true,"command":"ls"}`),
			want:   false,
		},
		{
			name:   "successful cat file does not mark dirty",
			result: successToolResult("execute_command", `{"success":true,"executed":true,"command":"cat README.md"}`),
			want:   false,
		},
		{
			name:   "successful compound ls marks dirty",
			result: successToolResult("execute_command", `{"success":true,"executed":true,"command":"ls foo && echo bar"}`),
			want:   true,
		},
		{
			name:   "successful python marks dirty",
			result: successToolResult("execute_command", `{"success":true,"executed":true,"command":"python script.py"}`),
			want:   true,
		},
		{
			name:   "successful git status does not mark dirty",
			result: successToolResult("execute_command", `{"success":true,"executed":true,"command":"git status"}`),
			want:   false,
		},
		{
			name:   "successful absolute ls marks dirty",
			result: successToolResult("execute_command", `{"success":true,"executed":true,"command":"/bin/ls"}`),
			want:   true,
		},
		{
			name:   "failed command does not mark dirty",
			result: failedToolResult("execute_command"),
			want:   false,
		},
		{
			name:   "blocked command does not mark dirty",
			result: successToolResult("execute_command", `{"success":false,"status":"blocked","executed":false,"command":"rm -rf foo"}`),
			want:   false,
		},
		{
			name:   "failed mutation tool",
			result: failedToolResult("write_file"),
			entry:  tools.Entry{Name: "write_file", Safety: tools.Safety{MutatesWorkspace: true}},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workspaceMutationObserved(tt.result, tt.entry)
			if got != tt.want {
				t.Fatalf("workspaceMutationObserved = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTodoProgressNudge(t *testing.T) {
	tests := []struct {
		name        string
		todos       []Todo
		wantContent bool
	}{
		{name: "no todos", wantContent: false},
		{
			name:        "all todos completed",
			todos:       []Todo{{ID: "impl", Content: "Implement", Status: TodoStatusCompleted}},
			wantContent: false,
		},
		{
			name:        "in progress todo",
			todos:       []Todo{{ID: "impl", Content: "Implement change", Status: TodoStatusInProgress}},
			wantContent: true,
		},
		{
			name:        "pending todos only",
			todos:       []Todo{{ID: "impl", Content: "Implement change", Status: TodoStatusPending}},
			wantContent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := testTodoNudgeLayout(t)
			todoStore := store.NewTodoStore(layout)
			if err := NewTodoManager(todoStore).Replace(tt.todos); err != nil {
				t.Fatalf("Replace todos: %v", err)
			}
			runner := &Runner{history: NewHistoryManager(
				store.NewHistoryStore(layout),
				store.NewMemoryStore(layout),
				WithTodoStore(todoStore),
			)}

			message, err := runner.todoProgressNudge()
			if err != nil {
				t.Fatalf("todoProgressNudge: %v", err)
			}
			if !tt.wantContent {
				if message.Content != "" {
					t.Fatalf("message = %+v, want empty", message)
				}
				return
			}
			if message.Role != RoleSystem || !strings.HasPrefix(message.Content, "TODO progress checkpoint:") {
				t.Fatalf("message = %+v, want system TODO progress checkpoint", message)
			}
			if !strings.Contains(message.Content, RenderTodos(tt.todos)) {
				t.Fatalf("message = %q, want rendered todos", message.Content)
			}
			if !strings.Contains(message.Content, "do not defer TODO completion") {
				t.Fatalf("message = %q, want defer warning", message.Content)
			}
		})
	}
}

func successToolResult(name, payload string) ToolResult {
	return ToolResult{
		CallID:  "call-1",
		Name:    name,
		Payload: json.RawMessage(payload),
	}
}

func failedToolResult(name string) ToolResult {
	return ToolResult{
		CallID:  "call-1",
		Name:    name,
		Payload: json.RawMessage(`{"success":false,"status":"failed","error":"failed"}`),
		Error:   "failed",
	}
}

func testTodoNudgeLayout(t *testing.T) state.Layout {
	t.Helper()

	layout, err := state.ResolveLayout(
		filepath.Join(t.TempDir(), "workspace"),
		filepath.Join(t.TempDir(), "state"),
	)
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	return layout
}
