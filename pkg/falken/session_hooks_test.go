package falken

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSessionHookProviderStartsAndCloses(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingHookProvider{
		descriptors: []HookDescriptor{{Name: "start", Event: HookSessionStart, Description: "start hook"}},
	}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		HookProviders:    []HookProvider{provider},
		ToolProviders:    []ToolProvider{&recordingToolProvider{descriptors: []ToolDescriptor{testToolDescriptor("external_tool")}}},
		BaseSystemPrompt: "",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if provider.starts != 1 || provider.host == nil {
		t.Fatalf("provider starts = %d host nil = %t, want started with host", provider.starts, provider.host == nil)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if provider.closes != 1 {
		t.Fatalf("provider closes = %d, want 1", provider.closes)
	}
	if provider.runs != 1 {
		t.Fatalf("provider runs = %d, want session_start hook", provider.runs)
	}
}

func TestSessionHookProviderRunsCloseHook(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingHookProvider{
		descriptors: []HookDescriptor{{Name: "close", Event: HookSessionClose, Description: "close hook"}},
	}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		HookProviders:    []HookProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if provider.runs != 0 {
		t.Fatalf("provider runs after start = %d, want no close hook yet", provider.runs)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if provider.runs != 1 {
		t.Fatalf("provider runs = %d, want session_close hook", provider.runs)
	}
	if provider.closes != 1 {
		t.Fatalf("provider closes = %d, want provider closed after hook", provider.closes)
	}
}

func TestSessionCloseHookCanReenterSessionWithoutDeadlock(t *testing.T) {
	setStateHomeEnv(t)

	var session *Session
	provider := &recordingHookProvider{
		descriptors: []HookDescriptor{{Name: "close", Event: HookSessionClose, Description: "close hook"}},
		runFunc: func() error {
			_, err := session.CurrentMode()
			if !errors.Is(err, ErrSessionClosing) {
				return fmt.Errorf("CurrentMode during Close error = %w, want ErrSessionClosing", err)
			}
			return nil
		},
	}
	var err error
	session, err = New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		HookProviders:    []HookProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSessionStartHookFailureFailsStart(t *testing.T) {
	setStateHomeEnv(t)

	provider := &recordingHookProvider{
		descriptors: []HookDescriptor{{Name: "start", Event: HookSessionStart}},
		result:      HookResult{Success: false, Status: "blocked", Error: "nope"},
	}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		HookProviders:    []HookProvider{provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if err == nil || !strings.Contains(err.Error(), "run session start hooks") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("Start error = %v, want hook failure", err)
	}
	if provider.closes != 1 {
		t.Fatalf("provider closes = %d, want cleanup close", provider.closes)
	}
}

func TestSessionStartContextReachesHookProvider(t *testing.T) {
	setStateHomeEnv(t)

	ctx, cancel := context.WithCancel(context.Background())
	provider := &recordingHookProvider{
		startWithContext: func(ctx context.Context) error {
			cancel()
			return ctx.Err()
		},
	}
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM:           noopLLM{},
		HookProviders: []HookProvider{provider},
	})

	if err := session.StartContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartContext error = %v, want context.Canceled", err)
	}
	if provider.starts != 1 {
		t.Fatalf("provider starts = %d, want 1", provider.starts)
	}
}

func TestSessionHookProviderHooksAreNotListedToLLM(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              llm,
		HookProviders:    []HookProvider{&recordingHookProvider{descriptors: []HookDescriptor{{Name: "audit", Event: HookSessionStart}}}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "list tools"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if toolDefinitionNames(llm.requests[0].Tools)["audit"] {
		t.Fatalf("tools = %+v, hook should not be exposed as LLM tool", llm.requests[0].Tools)
	}
}

func TestSessionHookProviderRejectsDuplicateHooks(t *testing.T) {
	setStateHomeEnv(t)

	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		HookProviders: []HookProvider{
			&recordingHookProvider{descriptors: []HookDescriptor{{Name: "audit", Event: HookSessionStart}}},
			&recordingHookProvider{descriptors: []HookDescriptor{{Name: "audit", Event: HookSessionStart}}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = session.Start()
	if err == nil || !strings.Contains(err.Error(), "duplicate hook") {
		t.Fatalf("Start error = %v, want duplicate hook", err)
	}
}

func TestSessionHookProviderStartupHostIsCapabilityScoped(t *testing.T) {
	setStateHomeEnv(t)

	unscoped := &recordingHookProvider{}
	scoped := &recordingHookProvider{startupSafety: ToolSafety{ReadsHostState: true}}
	session, err := New(Config{
		WorkspaceDir:     tempWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              &sessionFakeLLM{},
		HookProviders:    []HookProvider{unscoped, scoped},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if _, err := unscoped.host.GetState(context.Background(), ToolStateGetRequest{Key: "missing"}); !errors.Is(err, errToolHostCapabilityDenied) {
		t.Fatalf("unscoped hook GetState error = %v, want capability denied", err)
	}
	if _, err := scoped.host.GetState(context.Background(), ToolStateGetRequest{Key: "missing"}); err != nil {
		t.Fatalf("scoped hook GetState: %v", err)
	}
}

func TestCloneSessionConfigClonesHookProvidersSlice(t *testing.T) {
	first := &recordingHookProvider{}
	second := &recordingHookProvider{}
	config := SessionConfig{HookProviders: []HookProvider{first}}

	cloned := cloneSessionConfig(config)
	if len(cloned.HookProviders) != 1 {
		t.Fatalf("cloned providers = %d, want 1", len(cloned.HookProviders))
	}
	config.HookProviders[0] = second
	if cloned.HookProviders[0] != first {
		t.Fatal("clone shares hook provider slice backing array")
	}
}

type recordingHookProvider struct {
	starts           int
	closes           int
	runs             int
	host             ToolHost
	descriptors      []HookDescriptor
	result           HookResult
	err              error
	runFunc          func() error
	startWithContext func(context.Context) error
	startupSafety    ToolSafety
}

func (p *recordingHookProvider) Start(ctx context.Context, host ToolHost) error {
	p.starts++
	p.host = host
	if p.startWithContext != nil {
		if err := p.startWithContext(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (p *recordingHookProvider) Hooks(context.Context) ([]HookDescriptor, error) {
	return append([]HookDescriptor(nil), p.descriptors...), nil
}

func (p *recordingHookProvider) RunHook(context.Context, HookInvocation) (HookResult, error) {
	p.runs++
	if p.runFunc != nil {
		if err := p.runFunc(); err != nil {
			return HookResult{}, err
		}
	}
	if p.err != nil {
		return HookResult{}, p.err
	}
	if p.result.Status != "" || p.result.Error != "" || len(p.result.Payload) != 0 {
		return p.result, nil
	}
	return HookResult{Success: true, Status: "succeeded"}, nil
}

func (p *recordingHookProvider) Close(context.Context) error {
	p.closes++
	return nil
}

func (p *recordingHookProvider) StartupSafety() ToolSafety {
	return p.startupSafety
}
