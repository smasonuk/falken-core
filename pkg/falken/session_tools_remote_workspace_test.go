package falken

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteWorkspaceModeRejectsProviderStartupMutatesWorkspace(t *testing.T) {
	provider := &recordingToolProvider{startupSafety: ToolSafety{MutatesWorkspace: true}}
	session := newRemoteWorkspaceToolSafetySession(t, provider, false, noopLLM{})
	err := session.Start()
	if err == nil || !strings.Contains(err.Error(), "cannot run in remote workspace mode") {
		t.Fatalf("Start error = %v, want startup mutation safety rejection", err)
	}
	if provider.starts != 0 {
		t.Fatalf("provider starts = %d, want rejection before provider startup", provider.starts)
	}
}

func TestRemoteWorkspaceModeRejectsDescriptorMutatesWorkspace(t *testing.T) {
	descriptor := testNonWorkspaceToolDescriptor("external_writer")
	descriptor.Safety.MutatesWorkspace = true
	provider := &recordingToolProvider{descriptors: []ToolDescriptor{descriptor}}
	session := newRemoteWorkspaceToolSafetySession(t, provider, false, noopLLM{})
	err := session.Start()
	if err == nil || !strings.Contains(err.Error(), "cannot run in remote workspace mode") {
		t.Fatalf("Start error = %v, want descriptor mutation safety rejection", err)
	}
	if provider.closes != 1 {
		t.Fatalf("provider closes = %d, want cleanup after descriptor rejection", provider.closes)
	}
}

func TestLocalWorkspaceModeAllowsProviderMutatesWorkspaceTool(t *testing.T) {
	setStateHomeEnv(t)

	descriptor := testNonWorkspaceToolDescriptor("external_writer")
	descriptor.Safety.MutatesWorkspace = true
	provider := &recordingToolProvider{descriptors: []ToolDescriptor{descriptor}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              noopLLM{},
		ToolProviders:    []ToolProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()
}

func TestRemoteWorkspaceModeKeepsBuiltinManagedFileToolsAvailable(t *testing.T) {
	llm := &sessionFakeLLM{}
	session := newRemoteWorkspaceToolSafetySession(t, nil, false, llm)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "list builtins"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(llm.requests) == 0 {
		t.Fatal("LLM received no request")
	}
	tools := toolDefinitionNames(llm.requests[0].Tools)
	for _, want := range []string{"read_file", "write_file", "edit_file", "delete_file"} {
		if !tools[want] {
			t.Fatalf("tools = %+v, want built-in %s in remote workspace mode", tools, want)
		}
	}
}

func newRemoteWorkspaceToolSafetySession(t *testing.T, provider ToolProvider, allowWorkspaceTools bool, llm LLM) *Session {
	t.Helper()
	setStateHomeEnv(t)
	providers := []ToolProvider(nil)
	if provider != nil {
		providers = []ToolProvider{provider}
	}
	session, err := New(Config{
		WorkspaceDir:     filepath.Join(t.TempDir(), "missing-workspace"),
		StateDir:         filepath.Join(t.TempDir(), "agent-state"),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeSandbox},
		Runtime: runtimeAdaptersProvider{adapters: RuntimeAdapters{
			SandboxRuntime: &countingSandboxRuntime{},
			WorkspaceFiles: stubWorkspaceOperations{},
		}},
		LLM:                             llm,
		ToolProviders:                   providers,
		AllowWorkspaceToolsInRemoteMode: allowWorkspaceTools,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return session
}
