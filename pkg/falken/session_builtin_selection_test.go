package falken

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
)

func TestBuiltinToolsDefaultModePreservesAllBuiltins(t *testing.T) {
	got, err := builtinToolEntriesFor(BuiltinToolsConfig{})
	if err != nil {
		t.Fatalf("builtinToolEntriesFor default: %v", err)
	}
	if !reflect.DeepEqual(entryNames(got), entryNames(builtinToolEntries())) {
		t.Fatalf("default built-ins = %v, want all built-ins", entryNames(got))
	}
}

func TestBuiltinToolsNoneModeExposesNone(t *testing.T) {
	got, err := builtinToolEntriesFor(BuiltinToolsConfig{Mode: BuiltinToolsNone})
	if err != nil {
		t.Fatalf("builtinToolEntriesFor none: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("none built-ins = %v, want none", entryNames(got))
	}
}

func TestBuiltinToolsSelectedModeUsesRegistryOrder(t *testing.T) {
	got, err := builtinToolEntriesFor(BuiltinToolsConfig{
		Mode:  BuiltinToolsSelected,
		Names: []string{"grep", "read_file"},
	})
	if err != nil {
		t.Fatalf("builtinToolEntriesFor selected: %v", err)
	}
	if want := []string{"read_file", "grep"}; !reflect.DeepEqual(entryNames(got), want) {
		t.Fatalf("selected built-ins = %v, want %v", entryNames(got), want)
	}
}

func TestBuiltinToolsSelectedModeRejectsUnknownNames(t *testing.T) {
	_, err := builtinToolEntriesFor(BuiltinToolsConfig{
		Mode:  BuiltinToolsSelected,
		Names: []string{"not_a_builtin"},
	})
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "unknown built-in tool name") {
		t.Fatalf("builtinToolEntriesFor error = %v, want unknown built-in config error", err)
	}
}

func TestBuiltinToolsSelectedModeRejectsDuplicateNames(t *testing.T) {
	_, err := builtinToolEntriesFor(BuiltinToolsConfig{
		Mode:  BuiltinToolsSelected,
		Names: []string{"read_file", "read_file"},
	})
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "duplicate built-in tool name") {
		t.Fatalf("builtinToolEntriesFor error = %v, want duplicate built-in config error", err)
	}
}

func TestDisabledBuiltinCannotBeExecutedDirectly(t *testing.T) {
	setStateHomeEnv(t)

	session := newTestSessionWithConfig(t, SessionConfig{
		LLM: noopLLM{},
		BuiltinTools: BuiltinToolsConfig{
			Mode:  BuiltinToolsSelected,
			Names: []string{"read_file"},
		},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	executor := sessionToolExecutor{hub: session.resources.toolHub, runtime: session.resources.runtime}
	_, err := executor.ExecuteTool(context.Background(), agent.ToolExecutionRequest{Name: "write_file"})
	if err == nil || !strings.Contains(err.Error(), "tool is not active or unknown: write_file") {
		t.Fatalf("ExecuteTool error = %v, want inactive built-in error", err)
	}
}

func TestProviderToolsWorkAlongsideSelectedBuiltins(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM: llm,
		BuiltinTools: BuiltinToolsConfig{
			Mode:  BuiltinToolsSelected,
			Names: []string{"read_file"},
		},
		ToolProviders: []ToolProvider{&recordingToolProvider{descriptors: []ToolDescriptor{testNonWorkspaceToolDescriptor("external_reader")}}},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "tools"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := sessionAgentToolNames(llm.requests[0].Tools), []string{"read_file", "external_reader"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func TestSelectedBuiltinDuplicateWithProviderIsRejected(t *testing.T) {
	setStateHomeEnv(t)

	session := newTestSessionWithConfig(t, SessionConfig{
		LLM: noopLLM{},
		BuiltinTools: BuiltinToolsConfig{
			Mode:  BuiltinToolsSelected,
			Names: []string{"read_file"},
		},
		ToolProviders: []ToolProvider{&recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("read_file")}}},
	})
	err := session.Start()
	if err == nil || !strings.Contains(err.Error(), "duplicate tool name") {
		t.Fatalf("Start error = %v, want duplicate provider/built-in name", err)
	}
}

func entryNames(entries []tools.Entry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}
