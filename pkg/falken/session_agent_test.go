package falken

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestSessionAgentRunBasicPersistsHistoryAndEmitsEvents(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{
		AssistantText: "hello from agent",
		FinishReason:  FinishReasonStop,
	}}}
	var events []Event
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM: llm,
		Events: func(event Event) {
			events = append(events, event)
		},
		BaseSystemPrompt: "You are Falken.",
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.Run(context.Background(), RunRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "hello from agent" {
		t.Fatalf("result = %+v, want completed output", result)
	}
	if got := sessionAgentEventTypes(events); !reflect.DeepEqual(got, []EventType{EventPlanRouting, EventAssistantText, EventRunCompleted}) {
		t.Fatalf("events = %v", got)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("LLM requests = %d, want one main call with heuristic routing", len(llm.requests))
	}
	if llm.requests[0].ToolChoice != nil {
		t.Fatalf("main request tool choice = %+v, want no synthetic routing call", llm.requests[0].ToolChoice)
	}
	if sessionAgentHasTool(sessionAgentToolNames(llm.requests[0].Tools), "decide_plan_mode") {
		t.Fatalf("main tools include synthetic routing tool: %+v", llm.requests[0].Tools)
	}

	history := loadSessionAgentHistory(t, session)
	if len(history) != 3 {
		t.Fatalf("history length = %d, want system,user,assistant", len(history))
	}
	if !strings.Contains(history[0].Content, "You are Falken.") || strings.Contains(history[0].Content, "--- CURRENT MODE ---") {
		t.Fatalf("system prompt = %q", history[0].Content)
	}
	if history[1].Content != "hi" || history[2].Content != "hello from agent" {
		t.Fatalf("history = %+v", history)
	}
}

func TestSessionAgentPlanRoutingLLMUsesRoutingCall(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{
		AssistantText: "hello from agent",
		FinishReason:  FinishReasonStop,
	}}}
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM:         llm,
		PlanRouting: PlanRoutingLLM,
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "hi"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("LLM requests = %d, want routing and main call", len(llm.requests))
	}
	if got := sessionAgentToolNames(llm.requests[0].Tools); !reflect.DeepEqual(got, []string{"decide_plan_mode"}) {
		t.Fatalf("routing tools = %v, want decide_plan_mode only", got)
	}
	if llm.requests[0].ToolChoice == nil || llm.requests[0].ToolChoice.Type != "tool" || llm.requests[0].ToolChoice.Name != "decide_plan_mode" {
		t.Fatalf("routing tool choice = %+v, want forced decide_plan_mode", llm.requests[0].ToolChoice)
	}
	if sessionAgentHasTool(sessionAgentToolNames(llm.requests[1].Tools), "decide_plan_mode") {
		t.Fatalf("main tools include synthetic routing tool: %+v", llm.requests[1].Tools)
	}
}

func TestSessionAgentPlanRoutingDisabledSkipsAutomaticPlanMode(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{
		AssistantText: "done",
		FinishReason:  FinishReasonStop,
	}}}
	var events []Event
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM:         llm,
		PlanRouting: PlanRoutingDisabled,
		Events:      func(event Event) { events = append(events, event) },
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{Prompt: "Build a production-ready new application from scratch with SQLite, YAML config, concurrent import, modular packages, and unit tests."}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(llm.requests) != 1 || llm.requests[0].ToolChoice != nil {
		t.Fatalf("LLM requests = %+v, want one main call without routing", llm.requests)
	}
	if got := sessionAgentEventTypes(events); !reflect.DeepEqual(got, []EventType{EventAssistantText, EventRunCompleted}) {
		t.Fatalf("events = %v, want no plan routing event", got)
	}
	mode, err := session.CurrentMode()
	if err != nil {
		t.Fatalf("CurrentMode: %v", err)
	}
	if mode != ModeDefault {
		t.Fatalf("mode = %q, want default", mode)
	}
}

func TestSessionAgentRejectsUnsupportedPlanRoutingMode(t *testing.T) {
	setStateHomeEnv(t)

	session := newTestSessionWithConfig(t, SessionConfig{
		LLM:         &sessionFakeLLM{},
		PlanRouting: PlanRoutingMode("surprise"),
	})
	err := session.Start()
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "unsupported plan routing mode") {
		t.Fatalf("Start error = %v, want invalid plan routing config", err)
	}
}

func TestSessionAgentRunUsesPublicStreamingLLM(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionStreamingLLM{
		chunks: []string{"hello ", "stream"},
		response: CompletionResponse{
			AssistantText: "hello stream",
			FinishReason:  FinishReasonStop,
		},
	}
	var events []Event
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM:    llm,
		Events: func(event Event) { events = append(events, event) },
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.Run(context.Background(), RunRequest{Prompt: "stream"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "hello stream" {
		t.Fatalf("result = %+v, want streamed completion", result)
	}
	if got := sessionAgentEventTypes(events); !reflect.DeepEqual(got, []EventType{
		EventPlanRouting,
		EventAssistantText,
		EventAssistantText,
		EventRunCompleted,
	}) {
		t.Fatalf("events = %v, want streamed chunks and completion", got)
	}
	history := loadSessionAgentHistory(t, session)
	if history[len(history)-1].Content != "hello stream" {
		t.Fatalf("persisted assistant = %+v, want final streamed response once", history[len(history)-1])
	}
}

func TestSessionAgentPlanModeBlocksMutatingTool(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "write_file",
				Arguments: json.RawMessage(`{"path":"a","content":"x"}`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "planned", FinishReason: FinishReasonStop},
		{
			ToolCalls: []ToolCall{{
				ID:        "call-submit",
				Name:      "submit_plan_implementation",
				Arguments: json.RawMessage(`{"summary":"no implementation changes","verification_summary":"not needed"}`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "planned", FinishReason: FinishReasonStop},
	}}
	reviewer := &sessionFakeLLM{responses: []CompletionResponse{{
		ToolCalls: []ToolCall{{
			ID:        "review-1",
			Name:      "record_command_evidence_review",
			Arguments: json.RawMessage(`{"verdict":"not_applicable","verification_performed":false,"confidence":"high","reason":"no workspace changes were made"}`),
		}},
		FinishReason: FinishReasonToolCalls,
	}}}
	session := newTestSessionWithConfig(t, SessionConfig{LLM: llm, VerificationReviewerLLM: reviewer})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}
	if err := session.WritePlan("# Goal\nInspect planning behavior before blocking mutating tools.\n\n# Files\nNo workspace files are modified by this setup step.\n\n# Changes\n1. Store a valid implementation plan through the public API.\n2. Run the agent while it is still in plan mode.\n\n# Verification\nConfirm the mutating tool call is blocked by the plan-mode policy."); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	if plan, err := session.ReadPlan(); err != nil || !strings.Contains(plan, "Inspect planning behavior") {
		t.Fatalf("ReadPlan = %q/%v", plan, err)
	}

	result, err := session.Run(context.Background(), RunRequest{Prompt: "plan"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "planned" {
		t.Fatalf("result = %+v", result)
	}
	if exposed := sessionAgentToolNames(llm.requests[0].Tools); !sessionAgentHasTool(exposed, "read_files") || !sessionAgentHasTool(exposed, "read_plan") || !sessionAgentHasTool(exposed, "write_plan") {
		t.Fatalf("plan-mode exposed tools = %v, want read/planning-safe built-ins", exposed)
	}
	history := loadSessionAgentHistory(t, session)
	foundBlocked := false
	for _, message := range history {
		if message.ToolResult != nil && strings.Contains(message.ToolResult.Error, "plan mode") {
			foundBlocked = true
			break
		}
	}
	if !foundBlocked {
		t.Fatalf("history = %+v, want blocked plan-mode tool result", history)
	}
}

func TestSessionAgentUsesConfiguredVerificationReviewerLLM(t *testing.T) {
	setStateHomeEnv(t)

	main := &sessionFakeLLM{responses: []CompletionResponse{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-submit",
				Name:      "submit_plan_implementation",
				Arguments: json.RawMessage(`{"summary":"implemented","verification_summary":"not applicable"}`),
			}},
			FinishReason: FinishReasonToolCalls,
		},
		{AssistantText: "done", FinishReason: FinishReasonStop},
	}}
	reviewer := &sessionFakeLLM{responses: []CompletionResponse{{
		ToolCalls: []ToolCall{{
			ID:        "review-1",
			Name:      "record_command_evidence_review",
			Arguments: json.RawMessage(`{"verdict":"not_applicable","verification_performed":false,"confidence":"high","reason":"no workspace changes were made"}`),
		}},
		FinishReason: FinishReasonToolCalls,
	}}}
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM:                     main,
		VerificationReviewerLLM: reviewer,
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.stores.plan.Write(validImplementationPlanForSessionTest()); err != nil {
		t.Fatalf("plan.Write: %v", err)
	}
	if err := session.stores.todos.Write(store.TodoState{Items: []store.TodoItem{{
		ID:      "task-1",
		Content: "Implement",
		Status:  "completed",
	}}}); err != nil {
		t.Fatalf("todos.Write: %v", err)
	}

	result, err := session.Run(context.Background(), RunRequest{Prompt: "submit"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "done" {
		t.Fatalf("result = %+v, want completed final output", result)
	}
	if len(reviewer.requests) != 1 || reviewer.requests[0].ToolChoice == nil || reviewer.requests[0].ToolChoice.Name != "record_command_evidence_review" {
		t.Fatalf("reviewer requests = %+v, want forced command evidence review", reviewer.requests)
	}
	for _, request := range main.requests {
		if request.ToolChoice != nil && request.ToolChoice.Name == "record_command_evidence_review" {
			t.Fatalf("main LLM received reviewer request: %+v", request)
		}
	}
}

func validImplementationPlanForSessionTest() string {
	return "# Goal\nComplete the active session test plan with enough detail for validation.\n\n# Files\nNo workspace files are modified by this test setup.\n\n# Changes\n1. Prepare completed todos.\n2. Submit the implementation through the runtime tool.\n\n# Verification\nThe reviewer reports that verification is not applicable."
}

func TestSessionWritePlanRejectsInvalidPlan(t *testing.T) {
	setStateHomeEnv(t)

	session := newTestSessionWithConfig(t, SessionConfig{LLM: noopLLM{}})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.WritePlan("   "); !errors.Is(err, agent.ErrInvalidPlan) {
		t.Fatalf("WritePlan invalid error = %v, want ErrInvalidPlan", err)
	}
}

func TestSessionWritePlanAndTodosWritesAtomicallyAndExitsPlanMode(t *testing.T) {
	setStateHomeEnv(t)

	session := newTestSessionWithConfig(t, SessionConfig{LLM: noopLLM{}})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}
	todos := []Todo{{ID: "t1", Content: "Do the work", Status: string(agent.TodoStatusPending)}}
	if err := session.WritePlanAndTodos(validImplementationPlanForSessionTest(), todos); err != nil {
		t.Fatalf("WritePlanAndTodos: %v", err)
	}
	plan, err := session.ReadPlan()
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if !strings.Contains(plan, "Complete the active session test plan") {
		t.Fatalf("plan = %q, want written implementation plan", plan)
	}
	gotTodos, err := session.ReadTodos()
	if err != nil {
		t.Fatalf("ReadTodos: %v", err)
	}
	if !reflect.DeepEqual(gotTodos, todos) {
		t.Fatalf("todos = %+v, want %+v", gotTodos, todos)
	}
	mode, err := session.CurrentMode()
	if err != nil {
		t.Fatalf("CurrentMode: %v", err)
	}
	if mode != ModeDefault {
		t.Fatalf("mode = %q, want default after plan write", mode)
	}
}

func TestSessionWritePlanAndTodosRejectsInvalidInputsAtomically(t *testing.T) {
	tests := []struct {
		name  string
		plan  string
		todos []Todo
	}{
		{
			name:  "empty todos",
			plan:  validImplementationPlanForSessionTest(),
			todos: []Todo{},
		},
		{
			name:  "invalid todo status",
			plan:  validImplementationPlanForSessionTest(),
			todos: []Todo{{ID: "t1", Content: "Do the work", Status: "done"}},
		},
		{
			name:  "invalid plan",
			plan:  "# Goal\nToo short",
			todos: []Todo{{ID: "t1", Content: "Do the work", Status: string(agent.TodoStatusPending)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setStateHomeEnv(t)
			session := newTestSessionWithConfig(t, SessionConfig{LLM: noopLLM{}})
			if err := session.Start(); err != nil {
				t.Fatalf("Start: %v", err)
			}

			if err := session.WritePlanAndTodos(tt.plan, tt.todos); err == nil {
				t.Fatal("WritePlanAndTodos succeeded, want error")
			}
			plan, err := session.ReadPlan()
			if err != nil {
				t.Fatalf("ReadPlan: %v", err)
			}
			if strings.TrimSpace(plan) != "" {
				t.Fatalf("plan = %q, want unchanged empty plan", plan)
			}
			todos, err := session.ReadTodos()
			if err != nil {
				t.Fatalf("ReadTodos: %v", err)
			}
			if len(todos) != 0 {
				t.Fatalf("todos = %+v, want unchanged empty todos", todos)
			}
		})
	}
}

func TestSessionWritePlanAndTodosResetsCommandEvidenceReviewAttempts(t *testing.T) {
	setStateHomeEnv(t)

	session := newTestSessionWithConfig(t, SessionConfig{LLM: noopLLM{}})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	conversation := agent.NewConversationState(
		session.stores.plan,
		session.stores.todos,
		session.stores.memory,
		session.stores.commandEvidence,
	)
	if err := conversation.AppendCommandEvidence(agent.CommandEvidenceRecord{
		Command: "go test ./...", Status: "succeeded", Executed: true, Succeeded: true,
	}); err != nil {
		t.Fatalf("AppendCommandEvidence: %v", err)
	}
	if _, err := conversation.RecordCommandEvidenceReview(agent.CommandEvidenceReview{Verdict: "unclear", Confidence: "low", Reason: "no verification"}); err != nil {
		t.Fatalf("RecordCommandEvidenceReview: %v", err)
	}

	err := session.WritePlanAndTodos(validImplementationPlanForSessionTest(), []Todo{
		{ID: "t1", Content: "Do the work", Status: string(agent.TodoStatusPending)},
	})
	if err != nil {
		t.Fatalf("WritePlanAndTodos: %v", err)
	}
	evidence, err := conversation.ReadCommandEvidence()
	if err != nil {
		t.Fatalf("ReadCommandEvidence: %v", err)
	}
	if len(evidence.Records) != 1 || evidence.ReviewAttempts != 0 || evidence.LastReview != nil || evidence.PlanBaselineRevision != 1 {
		t.Fatalf("evidence = %+v, want records retained, baseline recorded, and review attempts reset", evidence)
	}
}

func TestSessionAgentCompletionCallbackThroughConfigAndRequest(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	var calls []string
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM: llm,
		OnCompleted: func(context.Context, RunResult) error {
			calls = append(calls, "config")
			return nil
		},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.Run(context.Background(), RunRequest{
		Prompt: "finish",
		OnCompleted: func(_ context.Context, result RunResult) error {
			calls = append(calls, "request")
			if !result.Completed || result.FinalOutput != "done" {
				t.Fatalf("callback result = %+v", result)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed {
		t.Fatalf("result = %+v, want completed", result)
	}
	if !reflect.DeepEqual(calls, []string{"config", "request"}) {
		t.Fatalf("completion callbacks = %v, want config then request", calls)
	}
}

func TestSessionAgentCompletionCallbackConfigErrorSkipsRequest(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	callbackErr := errors.New("config callback failed")
	var calls []string
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM: llm,
		OnCompleted: func(context.Context, RunResult) error {
			calls = append(calls, "config")
			return callbackErr
		},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.Run(context.Background(), RunRequest{
		Prompt: "finish",
		OnCompleted: func(context.Context, RunResult) error {
			calls = append(calls, "request")
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), callbackErr.Error()) {
		t.Fatalf("Run error = %v, want config callback failure", err)
	}
	if result.Completed || !strings.Contains(result.Error, "completion callback") {
		t.Fatalf("result = %+v, want callback failure", result)
	}
	if !reflect.DeepEqual(calls, []string{"config"}) {
		t.Fatalf("completion callbacks = %v, want only config", calls)
	}
}

func TestAppendWorkspaceContextDescribesLocalExecution(t *testing.T) {
	state := fakeWorkspacePromptState{
		root:  "/host/work",
		cwd:   "/host/work/subdir",
		tool:  "subdir",
		mount: "/workspace",
	}

	prompt := appendWorkspaceContext("Base", state, ExecutionConfig{Mode: ExecutionModeLocal})
	for _, want := range []string{
		"Base",
		"workspace_root: /host/work",
		"working_dir: subdir",
		"execution_mode: local",
		"Tool path rule: use \".\" or workspace-relative paths",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("local prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, stale := range []string{"sandbox_mount:", "Docker container", "Do not use /workspace"} {
		if strings.Contains(prompt, stale) {
			t.Fatalf("local prompt contains %q:\n%s", stale, prompt)
		}
	}
}

func TestAppendWorkspaceContextDescribesSandboxExecution(t *testing.T) {
	state := fakeWorkspacePromptState{
		root:  "/host/work",
		cwd:   "/host/work",
		tool:  ".",
		mount: "/workspace",
	}

	prompt := appendWorkspaceContext("", state, ExecutionConfig{Mode: ExecutionModeSandbox})
	for _, want := range []string{
		"workspace_root: /host/work",
		"working_dir: .",
		"execution_mode: sandbox",
		"sandbox_mount: /workspace",
		"configured sandbox runtime",
		"Do not use /workspace as a tool working_dir",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("sandbox prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSessionAgentCompletionCallbackFailureAndFailedRunCallbackBehavior(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	var events []Event
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM: llm,
		Events: func(event Event) {
			events = append(events, event)
		},
		OnCompleted: func(context.Context, RunResult) error {
			return errors.New("host callback failed")
		},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.Run(context.Background(), RunRequest{Prompt: "finish"})
	if err == nil || !strings.Contains(err.Error(), "completion callback: host callback failed") {
		t.Fatalf("Run error = %v, want callback failure", err)
	}
	if result.Completed || !strings.Contains(result.Error, "completion callback") {
		t.Fatalf("result = %+v, want callback failure", result)
	}
	if got := sessionAgentEventTypes(events); !reflect.DeepEqual(got, []EventType{EventPlanRouting, EventAssistantText, EventRunFailed}) {
		t.Fatalf("callback failure events = %v, want assistant_text/run_failed only", got)
	}

	failingLLM := &sessionFakeLLM{err: errors.New("llm failed")}
	called := false
	failedSession := newTestSessionWithConfig(t, SessionConfig{
		LLM: failingLLM,
		OnCompleted: func(context.Context, RunResult) error {
			called = true
			return nil
		},
	})
	if err := failedSession.Start(); err != nil {
		t.Fatalf("Start failed session: %v", err)
	}
	if _, err := failedSession.Run(context.Background(), RunRequest{Prompt: "fail"}); err == nil {
		t.Fatal("Run succeeded, want LLM failure")
	}
	if called {
		t.Fatal("completion callback should not run on failed session run")
	}
}

func TestSessionAgentLifecycleAndResetIntegration(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{AssistantText: "first", FinishReason: FinishReasonStop},
		{AssistantText: "second", FinishReason: FinishReasonStop},
	}}
	session := newTestSessionWithConfig(t, SessionConfig{LLM: llm})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result, err := session.Run(context.Background(), RunRequest{Prompt: "first"}); err != nil || result.FinalOutput != "first" {
		t.Fatalf("first Run = %+v/%v", result, err)
	}
	if history := loadSessionAgentHistory(t, session); len(history) == 0 {
		t.Fatal("expected persisted history after run")
	}
	if err := session.ResetConversationState(); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}
	if history, err := session.stores.history.Read(); err != nil || len(history) != 0 {
		t.Fatalf("history after reset = %+v/%v, want empty", history, err)
	}
	if result, err := session.Run(context.Background(), RunRequest{Prompt: "second"}); err != nil || result.FinalOutput != "second" {
		t.Fatalf("second Run after reset = %+v/%v", result, err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "after close"}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Run after close error = %v, want ErrSessionClosed", err)
	}
	if err := session.Start(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Start after close error = %v, want ErrSessionClosed", err)
	}
}

type sessionFakeLLM struct {
	requests  []CompletionRequest
	responses []CompletionResponse
	err       error
}

type sessionStreamingLLM struct {
	requests []CompletionRequest
	chunks   []string
	response CompletionResponse
	err      error
}

func (l *sessionStreamingLLM) Complete(_ context.Context, request CompletionRequest) (CompletionResponse, error) {
	l.requests = append(l.requests, request)
	if isSessionRoutingRequest(request) {
		return sessionRoutingResponse(false, "test default", "high", nil), nil
	}
	return CompletionResponse{}, errors.New("Complete should only be used for routing on sessionStreamingLLM")
}

func (l *sessionStreamingLLM) StreamComplete(_ context.Context, request CompletionRequest, sink AssistantTextSink) (CompletionResponse, error) {
	l.requests = append(l.requests, request)
	for _, chunk := range l.chunks {
		if sink != nil {
			sink(chunk)
		}
	}
	if l.err != nil {
		return CompletionResponse{}, l.err
	}
	return l.response, nil
}

func (l *sessionFakeLLM) Complete(_ context.Context, request CompletionRequest) (CompletionResponse, error) {
	l.requests = append(l.requests, request)
	if isSessionRoutingRequest(request) {
		return sessionRoutingResponse(false, "test default", "high", nil), nil
	}
	if l.err != nil {
		return CompletionResponse{}, l.err
	}
	if len(l.responses) == 0 {
		return CompletionResponse{AssistantText: "default", FinishReason: FinishReasonStop}, nil
	}
	response := l.responses[0]
	l.responses = l.responses[1:]
	return response, nil
}

func isSessionRoutingRequest(request CompletionRequest) bool {
	return request.ToolChoice != nil &&
		request.ToolChoice.Type == "tool" &&
		request.ToolChoice.Name == "decide_plan_mode"
}

func sessionRoutingResponse(requiresPlan bool, reason, confidence string, signals []string) CompletionResponse {
	if signals == nil {
		signals = []string{}
	}
	arguments, err := json.Marshal(map[string]any{
		"requires_plan": requiresPlan,
		"reason":        reason,
		"confidence":    confidence,
		"signals":       signals,
	})
	if err != nil {
		panic(err)
	}
	return CompletionResponse{
		ToolCalls: []ToolCall{{
			ID:        "route-1",
			Name:      "decide_plan_mode",
			Arguments: arguments,
		}},
		FinishReason: FinishReasonToolCalls,
	}
}

func sessionAgentEventTypes(events []Event) []EventType {
	types := make([]EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func sessionAgentToolNames(definitions []ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func sessionAgentHasTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

type fakeWorkspacePromptState struct {
	root  string
	cwd   string
	tool  string
	mount string
}

func (s fakeWorkspacePromptState) WorkspaceRoot() string {
	return s.root
}

func (s fakeWorkspacePromptState) CurrentWorkingDir() string {
	return s.cwd
}

func (s fakeWorkspacePromptState) ToolPathForHostPath(string) string {
	return s.tool
}

func (s fakeWorkspacePromptState) SandboxPathForHostPath(string) string {
	return s.mount
}

func loadSessionAgentHistory(t *testing.T, session *Session) []Message {
	t.Helper()

	entries, err := session.stores.history.Read()
	if err != nil {
		t.Fatalf("history.Read: %v", err)
	}
	history := make([]Message, 0, len(entries))
	for _, entry := range entries {
		var message Message
		if err := json.Unmarshal([]byte(entry), &message); err != nil {
			t.Fatalf("decode history entry: %v", err)
		}
		history = append(history, message)
	}
	return history
}

func TestSessionAgentHardening_HistoryPersistsAcrossRunsUntilReset(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{
		{AssistantText: "first answer", FinishReason: FinishReasonStop},
		{AssistantText: "second answer", FinishReason: FinishReasonStop},
	}}
	session := newTestSessionWithConfig(t, SessionConfig{LLM: llm, BaseSystemPrompt: "Base"})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if result, err := session.Run(context.Background(), RunRequest{Prompt: "first"}); err != nil || result.FinalOutput != "first answer" {
		t.Fatalf("first Run = %+v/%v", result, err)
	}
	if result, err := session.Run(context.Background(), RunRequest{Prompt: "second"}); err != nil || result.FinalOutput != "second answer" {
		t.Fatalf("second Run = %+v/%v", result, err)
	}

	history := loadSessionAgentHistory(t, session)
	gotRoles := sessionAgentRoles(history)
	wantRoles := []Role{RoleSystem, RoleUser, RoleAssistant, RoleUser, RoleAssistant}
	if !reflect.DeepEqual(gotRoles, wantRoles) {
		t.Fatalf("history roles = %v, want %v", gotRoles, wantRoles)
	}
	if history[1].Content != "first" || history[2].Content != "first answer" || history[3].Content != "second" || history[4].Content != "second answer" {
		t.Fatalf("history = %+v", history)
	}
	if strings.Count(history[0].Content, "--- CURRENT MODE ---") != 0 || strings.Count(history[0].Content, "--- CURRENT AGENT MEMORY ---") != 1 {
		t.Fatalf("system prompt duplicated runtime sections: %q", history[0].Content)
	}

	if err := session.ResetConversationState(); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}
	if raw, err := session.stores.history.Read(); err != nil || len(raw) != 0 {
		t.Fatalf("raw history after reset = %+v/%v, want empty", raw, err)
	}
}

func TestSessionAgentHardening_ConfigAndRequestEventSinksBothReceiveEvents(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "done", FinishReason: FinishReasonStop}}}
	var configEvents []Event
	var requestEvents []Event
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM: llm,
		Events: func(event Event) {
			configEvents = append(configEvents, event)
		},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := session.Run(context.Background(), RunRequest{
		Prompt: "events",
		Events: func(event Event) {
			requestEvents = append(requestEvents, event)
		},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []EventType{EventPlanRouting, EventAssistantText, EventRunCompleted}
	if got := sessionAgentEventTypes(configEvents); !reflect.DeepEqual(got, want) {
		t.Fatalf("config events = %v, want %v", got, want)
	}
	if got := sessionAgentEventTypes(requestEvents); !reflect.DeepEqual(got, want) {
		t.Fatalf("request events = %v, want %v", got, want)
	}
}

func TestSessionAgentHardening_RunFailedEventFlowsThroughSession(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{err: errors.New("llm hard failure")}
	var events []Event
	session := newTestSessionWithConfig(t, SessionConfig{
		LLM: llm,
		Events: func(event Event) {
			events = append(events, event)
		},
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.Run(context.Background(), RunRequest{Prompt: "fail"})
	if err == nil || !strings.Contains(err.Error(), "llm hard failure") {
		t.Fatalf("Run error = %v, want llm hard failure", err)
	}
	if result.Completed || result.Error != "llm hard failure" {
		t.Fatalf("result = %+v, want failed result", result)
	}
	if got := sessionAgentEventTypes(events); !reflect.DeepEqual(got, []EventType{EventPlanRouting, EventRunFailed}) {
		t.Fatalf("events = %v, want run_failed", got)
	}
}

func TestSessionAgentHardening_OverlappingRealAgentRunsAreRejected(t *testing.T) {
	setStateHomeEnv(t)

	llm := newBlockingSessionLLM()
	session := newTestSessionWithConfig(t, SessionConfig{LLM: llm})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := session.Run(context.Background(), RunRequest{Prompt: "first"})
		done <- err
	}()

	<-llm.started
	if _, err := session.Run(context.Background(), RunRequest{Prompt: "second"}); !errors.Is(err, ErrTopLevelRunActive) {
		t.Fatalf("overlapping Run error = %v, want ErrTopLevelRunActive", err)
	}
	close(llm.release)
	if err := <-done; err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
}

func TestSessionAgentHardening_ModeMutationWhileRunActiveIsRejected(t *testing.T) {
	setStateHomeEnv(t)

	llm := newBlockingSessionLLM()
	session := newTestSessionWithConfig(t, SessionConfig{LLM: llm})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := session.Run(context.Background(), RunRequest{Prompt: "first"})
		done <- err
	}()

	<-llm.started
	if err := session.EnterPlanMode(); !errors.Is(err, ErrTopLevelRunActive) {
		t.Fatalf("EnterPlanMode during run error = %v, want ErrTopLevelRunActive", err)
	}
	if err := session.WritePlan("Goal: no race"); !errors.Is(err, ErrTopLevelRunActive) {
		t.Fatalf("WritePlan during run error = %v, want ErrTopLevelRunActive", err)
	}
	if err := session.ExitPlanMode(); !errors.Is(err, ErrTopLevelRunActive) {
		t.Fatalf("ExitPlanMode during run error = %v, want ErrTopLevelRunActive", err)
	}
	close(llm.release)
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestSessionAgentHardening_TodosRequireImplementationSubmission(t *testing.T) {
	setStateHomeEnv(t)

	llm := &sessionFakeLLM{responses: []CompletionResponse{{AssistantText: "completed without ceremony", FinishReason: FinishReasonStop}}}
	session := newTestSessionWithConfig(t, SessionConfig{LLM: llm})
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.stores.todos.Write(store.TodoState{Items: []store.TodoItem{{
		ID:      "task-1",
		Content: "Still pending",
		Status:  "pending",
	}}}); err != nil {
		t.Fatalf("todos.Write: %v", err)
	}
	mode, err := session.CurrentMode()
	if err != nil {
		t.Fatalf("CurrentMode: %v", err)
	}
	if mode != ModeDefault {
		t.Fatalf("mode = %q, want default", mode)
	}

	result, err := session.Run(context.Background(), RunRequest{Prompt: "finish normally"})
	if !errors.Is(err, agent.ErrImplementationSubmissionRequired) {
		t.Fatalf("Run error = %v, want implementation submission required failure", err)
	}
	if result.Completed {
		t.Fatalf("result = %+v, want completion blocked", result)
	}
}

type blockingSessionLLM struct {
	started chan struct{}
	release chan struct{}
}

func newBlockingSessionLLM() *blockingSessionLLM {
	return &blockingSessionLLM{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (l *blockingSessionLLM) Complete(_ context.Context, request CompletionRequest) (CompletionResponse, error) {
	if isSessionRoutingRequest(request) {
		return sessionRoutingResponse(false, "test default", "high", nil), nil
	}
	close(l.started)
	<-l.release
	return CompletionResponse{AssistantText: "released", FinishReason: FinishReasonStop}, nil
}

func sessionAgentRoles(messages []Message) []Role {
	roles := make([]Role, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, message.Role)
	}
	return roles
}
