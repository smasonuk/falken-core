package falken

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/store"
)

func TestToolHostExecuteCommandUsesPolicy(t *testing.T) {
	setStateHomeEnv(t)

	var commandResult ToolCommandResult
	session := newSessionWithHostProvider(t, hostStartProvider{
		start: func(ctx context.Context, host ToolHost) error {
			var err error
			commandResult, err = host.ExecuteCommand(ctx, ToolCommandRequest{Command: "printf blocked"})
			return err
		},
	}, SessionConfig{
		Policy: PolicyConfig{StrictCommandAllowlist: true},
	})
	defer session.Close()

	if commandResult.Success || commandResult.Status != "blocked" || commandResult.PolicyExplanation == "" {
		t.Fatalf("command result = %+v, want policy-blocked command", commandResult)
	}
}

func TestToolHostExecuteCommandBlocksShellWriteBypass(t *testing.T) {
	setStateHomeEnv(t)

	var commandResult ToolCommandResult
	provider := &recordingToolProvider{
		descriptors: []ToolDescriptor{{
			Name:        "shell_writer",
			Description: "attempts a shell write",
			Parameters:  []byte(`{"type":"object","properties":{}}`),
			Safety:      ToolSafety{ExecutesShell: true},
			DefaultLoad: true,
		}},
		executeWithHost: func(ctx context.Context, _ ToolInvocation, host ToolHost) (ToolExecutionResult, error) {
			var err error
			commandResult, err = host.ExecuteCommand(ctx, ToolCommandRequest{Command: "echo hi > marker.txt"})
			return ToolExecutionResult{Success: true, Status: "done"}, err
		},
	}
	session := newSessionWithHostProvider(t, provider, SessionConfig{LLM: toolCallingLLM("shell_writer")})
	defer session.Close()
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "try shell write"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if commandResult.Success || commandResult.Status != "blocked" || commandResult.PolicyOutcome != "blocked_shell_write_bypass" {
		t.Fatalf("command result = %+v, want shell-write bypass blocked", commandResult)
	}
	if _, err := os.Stat(filepath.Join(session.layout.WorkspaceRoot, "marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("marker stat = %v, want missing file", err)
	}
}

func TestToolHostExecuteCommandRecordsCommandEvidenceForProviderTool(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingToolProvider{
		descriptors: []ToolDescriptor{{
			Name:        "provider_verify",
			Description: "runs provider verification",
			Parameters:  []byte(`{"type":"object","properties":{}}`),
			Safety:      ToolSafety{ExecutesShell: true},
			DefaultLoad: true,
		}},
		executeWithHost: func(ctx context.Context, _ ToolInvocation, host ToolHost) (ToolExecutionResult, error) {
			result, err := host.ExecuteCommand(ctx, ToolCommandRequest{Command: "true"})
			if err != nil {
				return ToolExecutionResult{}, err
			}
			return ToolExecutionResult{
				Success: result.Success,
				Status:  result.Status,
				Content: "provider verification complete",
			}, nil
		},
	}
	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{ToolCalls: []ToolCall{{ID: "call-provider", Name: "provider_verify", Arguments: []byte(`{}`)}}, FinishReason: FinishReasonToolCalls},
		{ToolCalls: []ToolCall{{ID: "call-evidence", Name: "read_command_evidence", Arguments: []byte(`{}`)}}, FinishReason: FinishReasonToolCalls},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}
	session := newSessionWithHostProvider(t, provider, SessionConfig{LLM: llm})
	defer session.Close()

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "verify"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	evidence, err := store.NewCommandEvidenceStore(session.layout).Read()
	if err != nil {
		t.Fatalf("Read command evidence: %v", err)
	}
	if len(evidence.Records) != 1 {
		t.Fatalf("records = %+v, want one provider command evidence record", evidence.Records)
	}
	record := evidence.Records[0]
	if record.Command != "true" || record.Status != "succeeded" || !record.Executed || !record.Succeeded {
		t.Fatalf("record = %+v, want successful provider command evidence", record)
	}
	if record.RecordedAt == "" {
		t.Fatalf("recorded_at is empty: %+v", record)
	}

	readResult := toolResultByName(t, session, "read_command_evidence")
	var payload struct {
		Records []store.CommandEvidenceRecord `json:"records"`
	}
	if err := json.Unmarshal(readResult.Payload, &payload); err != nil {
		t.Fatalf("decode read_command_evidence payload: %v", err)
	}
	if len(payload.Records) != 1 || payload.Records[0].Command != "true" {
		t.Fatalf("read_command_evidence payload = %+v, want provider command record", payload.Records)
	}
}

func TestToolHostCheckFileAccessUsesPolicy(t *testing.T) {
	setStateHomeEnv(t)

	var accessResult ToolFileAccessResult
	session := newSessionWithHostProvider(t, hostStartProvider{
		start: func(ctx context.Context, host ToolHost) error {
			var err error
			accessResult, err = host.CheckFileAccess(ctx, ToolFileAccessRequest{
				Path: "secret.txt",
				Mode: FileAccessRead,
			})
			return err
		},
	}, SessionConfig{
		Policy: PolicyConfig{StrictFileAllowlist: true},
	})
	defer session.Close()

	if accessResult.Allowed || accessResult.Explanation == "" {
		t.Fatalf("file access result = %+v, want policy denial", accessResult)
	}
}

func TestToolHostCheckFileAccessDeniesWorkspaceEscapeBeforePolicy(t *testing.T) {
	setStateHomeEnv(t)

	workspace := tempWorkspace(t)
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(workspace, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	var results []ToolFileAccessResult
	session, err := New(Config{
		WorkspaceDir:     workspace,
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		ToolProviders: []ToolProvider{hostStartProvider{
			start: func(ctx context.Context, host ToolHost) error {
				for _, path := range []string{
					filepath.Join("..", "outside.txt"),
					outsideFile,
					filepath.Join("escape", "secret.txt"),
				} {
					result, err := host.CheckFileAccess(ctx, ToolFileAccessRequest{Path: path, Mode: FileAccessRead})
					if err != nil {
						return err
					}
					results = append(results, result)
				}
				return nil
			},
		}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()

	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for _, result := range results {
		if result.Allowed || result.Explanation == "" {
			t.Fatalf("result = %+v, want structured workspace denial", result)
		}
	}
}

func TestToolHostCheckFileAccessCreateInsideWorkspaceReachesPolicy(t *testing.T) {
	setStateHomeEnv(t)

	var accessResult ToolFileAccessResult
	provider := &recordingToolProvider{
		descriptors: []ToolDescriptor{{
			Name:        "creator",
			Description: "checks create access",
			Parameters:  []byte(`{"type":"object","properties":{}}`),
			Safety:      ToolSafety{MutatesWorkspace: true},
			DefaultLoad: true,
		}},
		executeWithHost: func(ctx context.Context, _ ToolInvocation, host ToolHost) (ToolExecutionResult, error) {
			var err error
			accessResult, err = host.CheckFileAccess(ctx, ToolFileAccessRequest{
				Path: filepath.Join("new", "file.txt"),
				Mode: FileAccessCreate,
			})
			return ToolExecutionResult{Success: true, Status: "succeeded"}, err
		},
	}
	session := newSessionWithHostProvider(t, provider, SessionConfig{
		LLM:             toolCallingLLM("creator"),
		ApprovalHandler: projectApprovalHandler{fileScope: ApprovalScopeOnce},
	})
	defer session.Close()
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "check create"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !accessResult.Allowed {
		t.Fatalf("file access result = %+v, want create path to reach allowing policy", accessResult)
	}
}

func TestToolHostCheckFileAccessUsesVirtualWorkspaceWithoutLocalStat(t *testing.T) {
	setStateHomeEnv(t)

	virtualWorkspace := filepath.Join(t.TempDir(), "missing-workspace")
	var accessResult ToolFileAccessResult
	provider := startupFileAccessProvider{
		safety: ToolSafety{ReadsWorkspace: true},
		start: func(ctx context.Context, host ToolHost) error {
			var err error
			accessResult, err = host.CheckFileAccess(ctx, ToolFileAccessRequest{
				Path: "/workspace/src/main.go",
				Mode: FileAccessRead,
			})
			return err
		},
	}
	session, err := New(Config{
		WorkspaceDir: virtualWorkspace,
		StateDir:     filepath.Join(t.TempDir(), "state"),
		Runtime: runtimeAdaptersProvider{adapters: RuntimeAdapters{
			SandboxRuntime: &countingSandboxRuntime{},
			WorkspaceFiles: stubWorkspaceOperations{},
		}},
		LLM:                             &sessionFakeLLM{},
		ToolProviders:                   []ToolProvider{provider},
		AllowWorkspaceToolsInRemoteMode: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer session.Close()

	if !accessResult.Allowed {
		t.Fatalf("file access result = %+v, want virtual workspace path to reach allowing policy", accessResult)
	}
	if _, err := os.Stat(virtualWorkspace); !os.IsNotExist(err) {
		t.Fatalf("virtual workspace stat = %v, want no local workspace directory", err)
	}
}

func TestToolHostStateIsScopedUnderPluginStateRoot(t *testing.T) {
	setStateHomeEnv(t)

	var setResult ToolStateSetResult
	var getResult ToolStateGetResult
	provider := &recordingToolProvider{
		descriptors: []ToolDescriptor{{
			Name:        "state_tool",
			Description: "uses state",
			Parameters:  []byte(`{"type":"object","properties":{}}`),
			Safety:      ToolSafety{UsesHostState: true},
			DefaultLoad: true,
		}},
		executeWithHost: func(ctx context.Context, _ ToolInvocation, host ToolHost) (ToolExecutionResult, error) {
			var err error
			setResult, err = host.SetState(ctx, ToolStateSetRequest{
				Key:   "../outside",
				Value: "value",
			})
			if err != nil {
				return ToolExecutionResult{}, err
			}
			getResult, err = host.GetState(ctx, ToolStateGetRequest{
				Key: "../outside",
			})
			return ToolExecutionResult{Success: true, Status: "succeeded"}, err
		},
	}
	session := newSessionWithHostProvider(t, provider, SessionConfig{LLM: toolCallingLLM("state_tool")})
	defer session.Close()
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "state"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	root := session.Paths.PluginStateRoot
	if !strings.HasPrefix(setResult.Path, root+string(filepath.Separator)) {
		t.Fatalf("state path = %q, want under plugin state root %q", setResult.Path, root)
	}
	if strings.Contains(setResult.Path, "..") || strings.Contains(setResult.Path, "outside") {
		t.Fatalf("state path = %q, want sanitized hashed path", setResult.Path)
	}
	if getResult.Value != "value" || !getResult.Found {
		t.Fatalf("state get = %+v, want stored value", getResult)
	}
	if _, err := os.Stat(filepath.Join(root, "..", "outside.state")); !os.IsNotExist(err) {
		t.Fatalf("malicious state path stat = %v, want not exist", err)
	}
}

func TestToolHostStateReadWriteCapabilitiesAreSeparate(t *testing.T) {
	setStateHomeEnv(t)

	var setErr error
	provider := &recordingToolProvider{
		descriptors: []ToolDescriptor{{
			Name:        "state_reader",
			Description: "reads state",
			Parameters:  []byte(`{"type":"object","properties":{}}`),
			Safety:      ToolSafety{ReadsHostState: true},
			DefaultLoad: true,
		}},
		executeWithHost: func(ctx context.Context, _ ToolInvocation, host ToolHost) (ToolExecutionResult, error) {
			if _, err := host.GetState(ctx, ToolStateGetRequest{Key: "shared"}); err != nil {
				return ToolExecutionResult{}, err
			}
			_, setErr = host.SetState(ctx, ToolStateSetRequest{Key: "shared", Value: "blocked"})
			return ToolExecutionResult{Success: true, Status: "succeeded"}, nil
		},
	}
	session := newSessionWithHostProvider(t, provider, SessionConfig{LLM: toolCallingLLM("state_reader")})
	defer session.Close()
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "state"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !errors.Is(setErr, errToolHostCapabilityDenied) {
		t.Fatalf("SetState error = %v, want capability denial", setErr)
	}
}

func TestToolHostStateNamespacesAreProviderScoped(t *testing.T) {
	setStateHomeEnv(t)

	values := make([]string, 2)
	providers := []ToolProvider{
		&recordingToolProvider{
			descriptors: []ToolDescriptor{stateToolDescriptor("state_one")},
			executeWithHost: func(ctx context.Context, _ ToolInvocation, host ToolHost) (ToolExecutionResult, error) {
				if _, err := host.SetState(ctx, ToolStateSetRequest{Key: "shared", Value: "first"}); err != nil {
					return ToolExecutionResult{}, err
				}
				result, err := host.GetState(ctx, ToolStateGetRequest{Key: "shared"})
				if err != nil {
					return ToolExecutionResult{}, err
				}
				values[0] = result.Value
				return ToolExecutionResult{Success: true, Status: "succeeded"}, nil
			},
		},
		&recordingToolProvider{
			descriptors: []ToolDescriptor{stateToolDescriptor("state_two")},
			executeWithHost: func(ctx context.Context, _ ToolInvocation, host ToolHost) (ToolExecutionResult, error) {
				if _, err := host.SetState(ctx, ToolStateSetRequest{Key: "shared", Value: "second"}); err != nil {
					return ToolExecutionResult{}, err
				}
				result, err := host.GetState(ctx, ToolStateGetRequest{Key: "shared"})
				if err != nil {
					return ToolExecutionResult{}, err
				}
				values[1] = result.Value
				return ToolExecutionResult{Success: true, Status: "succeeded"}, nil
			},
		},
	}
	session := newSessionWithHostProviders(t, providers, SessionConfig{LLM: &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{
				{ID: "call-one", Name: "state_one", Arguments: []byte(`{}`)},
				{ID: "call-two", Name: "state_two", Arguments: []byte(`{}`)},
			},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}})
	defer session.Close()
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "state"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if values[0] != "first" || values[1] != "second" {
		t.Fatalf("state values = %+v, want isolated provider state", values)
	}
}

func TestToolHostEmitUsesSessionEventSink(t *testing.T) {
	setStateHomeEnv(t)

	var events []Event
	session := newSessionWithHostProvider(t, hostStartProvider{
		start: func(_ context.Context, host ToolHost) error {
			host.Emit(Event{Type: EventThought, Text: "host event"})
			return nil
		},
	}, SessionConfig{
		Events: func(event Event) { events = append(events, event) },
	})
	defer session.Close()

	if len(events) != 1 || events[0].Type != EventThought || events[0].Text != "host event" {
		t.Fatalf("events = %+v, want host-emitted thought event", events)
	}
}

func newSessionWithHostProvider(t *testing.T, provider ToolProvider, config SessionConfig) *Session {
	t.Helper()
	return newSessionWithHostProviders(t, []ToolProvider{provider}, config)
}

func toolCallingLLM(name string) *sessionFakeLLM {
	return &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls:    []ToolCall{{ID: "call-" + name, Name: name, Arguments: []byte(`{}`)}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}
}

func stateToolDescriptor(name string) ToolDescriptor {
	return ToolDescriptor{
		Name:        name,
		Description: "uses state",
		Parameters:  []byte(`{"type":"object","properties":{}}`),
		Safety:      ToolSafety{UsesHostState: true},
		DefaultLoad: true,
	}
}

func toolResultByName(t *testing.T, session *Session, name string) ToolResult {
	t.Helper()
	history := loadSessionAgentHistory(t, session)
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].ToolResult != nil && history[i].ToolResult.Name == name {
			return *history[i].ToolResult
		}
	}
	t.Fatalf("history = %+v, want tool result %q", history, name)
	return ToolResult{}
}

func newSessionWithHostProviders(t *testing.T, providers []ToolProvider, config SessionConfig) *Session {
	t.Helper()
	if config.LLM == nil {
		config.LLM = &sessionFakeLLM{}
	}
	config.Execution.Mode = ExecutionModeLocal
	config.ToolProviders = append(config.ToolProviders, providers...)
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: config.Execution,
		Runtime:          config.Runtime,
		LLM:              config.LLM,
		ToolProviders:    config.ToolProviders,
		Policy:           config.Policy,
		Events:           config.Events,
		ApprovalHandler:  config.ApprovalHandler,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return session
}

type hostStartProvider struct {
	start func(context.Context, ToolHost) error
}

func (p hostStartProvider) Start(ctx context.Context, host ToolHost) error {
	if p.start != nil {
		return p.start(ctx, host)
	}
	return nil
}

func (p hostStartProvider) Tools(context.Context) ([]ToolDescriptor, error) {
	return nil, nil
}

func (p hostStartProvider) ExecuteTool(context.Context, ToolInvocation) (ToolExecutionResult, error) {
	return ToolExecutionResult{}, nil
}

func (p hostStartProvider) Close(context.Context) error {
	return nil
}

type startupFileAccessProvider struct {
	start  func(context.Context, ToolHost) error
	safety ToolSafety
}

func (p startupFileAccessProvider) Start(ctx context.Context, host ToolHost) error {
	if p.start != nil {
		return p.start(ctx, host)
	}
	return nil
}

func (p startupFileAccessProvider) Tools(context.Context) ([]ToolDescriptor, error) {
	return nil, nil
}

func (p startupFileAccessProvider) ExecuteTool(context.Context, ToolInvocation) (ToolExecutionResult, error) {
	return ToolExecutionResult{}, nil
}

func (p startupFileAccessProvider) Close(context.Context) error {
	return nil
}

func (p startupFileAccessProvider) StartupSafety() ToolSafety {
	return p.safety
}
