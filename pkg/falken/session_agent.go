package falken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/store"
)

var errSessionAgentModeUnavailable = errors.New("agent mode is unavailable")

// sessionAgentRunner wraps the core agent.Runner with session-specific configuration.
type sessionAgentRunner struct {
	runner       *agent.Runner
	configEvents EventSink
	configDone   CompletionCallback
}

// newSessionAgentRunner initializes a configured sessionAgentRunner.
func newSessionAgentRunner(config SessionConfig, stores conversationStores, toolHub *sessionToolHub, mode *agent.ModeState, runtime *sessionRuntime) (*sessionAgentRunner, error) {
	llm := config.LLM
	if llm == nil {
		return nil, ErrLLMRequired
	}

	baseSystemPrompt := config.BaseSystemPrompt
	if runtime != nil && runtime.executionState != nil {
		baseSystemPrompt = appendWorkspaceContext(baseSystemPrompt, runtime.executionState, config.Execution)
	}
	history := agent.NewHistoryManager(
		stores.history,
		stores.memory,
		agent.WithTodoStore(stores.todos),
		agent.WithModeState(mode),
	)
	conversation := agent.NewConversationState(stores.plan, stores.todos, stores.memory, stores.commandEvidence)
	reviewLLM := config.VerificationReviewerLLM
	if reviewLLM == nil {
		reviewLLM = config.LLM
	}
	reviewer := agent.NewLLMCommandEvidenceReviewer(llmAdapter{llm: reviewLLM})
	var router agent.PlanRouter
	autoPlanMode := false
	switch config.PlanRouting {
	case "", PlanRoutingHeuristic:
		router = agent.NewHeuristicPlanRouter()
	case PlanRoutingLLM:
		autoPlanMode = true
	case PlanRoutingDisabled:
	default:
		return nil, fmt.Errorf("%w: unsupported plan routing mode %q", ErrInvalidConfig, config.PlanRouting)
	}
	runner, err := agent.NewRunner(agent.RunnerConfig{
		LLM:              llmAdapter{llm: llm},
		History:          history,
		State:            conversation,
		Mode:             mode,
		Tools:            sessionToolProvider{hub: toolHub},
		Executor:         sessionToolExecutor{hub: toolHub, runtime: runtime, mode: mode, plan: stores.plan, todos: stores.todos, memory: stores.memory, commandEvidence: stores.commandEvidence, reviewer: reviewer},
		BaseSystemPrompt: baseSystemPrompt,
		PlanRouter:       router,
		AutoPlanMode:     autoPlanMode,
	})
	if err != nil {
		return nil, err
	}

	return &sessionAgentRunner{
		runner:       runner,
		configEvents: config.Events,
		configDone:   config.OnCompleted,
	}, nil
}

func appendWorkspaceContext(base string, state interface {
	WorkspaceRoot() string
	CurrentWorkingDir() string
	ToolPathForHostPath(string) string
	SandboxPathForHostPath(string) string
}, config ExecutionConfig) string {
	root := state.WorkspaceRoot()
	cwd := state.CurrentWorkingDir()
	mode := config.Mode
	if mode == "" {
		mode = ExecutionModeSandbox
	}
	section := "--- CURRENT WORKSPACE ---\n" +
		"workspace_root: " + root + "\n" +
		"working_dir: " + state.ToolPathForHostPath(cwd) + "\n" +
		"execution_mode: " + string(mode)
	if mode == ExecutionModeLocal {
		section += "\nTool path rule: use \".\" or workspace-relative paths for tool arguments."
	} else {
		section += "\n" +
			"sandbox_mount: " + state.SandboxPathForHostPath(root) + "\n" +
			"Sandbox path rule: shell commands run through the configured sandbox runtime with the workspace mounted at sandbox_mount.\n" +
			"Tool path rule: use \".\" or workspace-relative paths for tool arguments. Do not use /workspace as a tool working_dir."
	}
	if base == "" {
		return section
	}
	return base + "\n\n" + section
}

// Run executes a top-level agent loop with combined event sinks and callbacks.
func (r *sessionAgentRunner) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	result, err := r.runner.Run(ctx, agent.RunRequest{
		Prompt:      request.Prompt,
		Events:      combineEventSinks(r.configEvents, request.Events),
		OnCompleted: completionCallback(r.configDone, request.OnCompleted),
	})
	return fromAgentRunResult(result), err
}

// noopLLM implements the LLM interface with an immediate stop response.
type noopLLM struct{}

// Complete implements LLM for noopLLM.
func (noopLLM) Complete(context.Context, CompletionRequest) (CompletionResponse, error) {
	return CompletionResponse{FinishReason: FinishReasonStop}, nil
}

// sessionToolProvider bridges the internal sessionToolHub to the agent tool provider interface.
type sessionToolProvider struct {
	hub *sessionToolHub
}

// ActiveTools returns the list of available tools for the current session.
func (p sessionToolProvider) ActiveTools(ctx context.Context) ([]tools.Entry, error) {
	if p.hub != nil {
		return p.hub.activeTools(ctx)
	}
	return builtinToolEntries(), nil
}

// sessionToolExecutor bridges the internal tool hub and runtime to the agent execution interface.
type sessionToolExecutor struct {
	hub             *sessionToolHub
	runtime         *sessionRuntime
	mode            *agent.ModeState
	plan            store.PlanBackend
	todos           store.TodoBackend
	memory          store.MemoryBackend
	commandEvidence store.CommandEvidenceBackend
	reviewer        agent.CommandEvidenceReviewer
}

// ExecuteTool invokes a tool by name, handling both built-in and external provider tools.
func (e sessionToolExecutor) ExecuteTool(ctx context.Context, request agent.ToolExecutionRequest) (agent.ToolExecutionResult, error) {
	if e.hub != nil {
		return e.hub.execute(ctx, request, e)
	}
	if isBuiltinTool(request.Name) {
		return e.executeBuiltinTool(ctx, request)
	}
	return agent.ToolExecutionResult{}, errUnknownProviderTool(request.Name)
}

// combineEventSinks merges two EventSinks into a single agent.EventSink.
func combineEventSinks(first, second EventSink) agent.EventSink {
	if first == nil {
		return eventSinkAdapter(second)
	}
	if second == nil {
		return eventSinkAdapter(first)
	}
	return func(event agent.Event) {
		publicEvent := fromAgentEvent(event)
		first(publicEvent)
		second(publicEvent)
	}
}

// completionCallback merges config and request completion callbacks in order.
func completionCallback(configCallback, requestCallback CompletionCallback) agent.CompletionCallback {
	if configCallback == nil {
		return completionCallbackAdapter(requestCallback)
	}
	if requestCallback == nil {
		return completionCallbackAdapter(configCallback)
	}
	return func(ctx context.Context, result agent.RunResult) error {
		publicResult := fromAgentRunResult(result)
		if err := configCallback(ctx, publicResult); err != nil {
			return err
		}
		return requestCallback(ctx, publicResult)
	}
}

// llmAdapter bridges the public LLM interface to the internal agent.LLM interface.
type llmAdapter struct {
	llm LLM
}

// Complete delegates the completion request to the underlying public LLM.
func (a llmAdapter) Complete(ctx context.Context, request agent.CompletionRequest) (agent.CompletionResponse, error) {
	response, err := a.llm.Complete(ctx, fromAgentCompletionRequest(request))
	if err != nil {
		return agent.CompletionResponse{}, err
	}
	return toAgentCompletionResponse(response), nil
}

// StreamComplete handles streamed responses if supported, falling back to Complete otherwise.
func (a llmAdapter) StreamComplete(ctx context.Context, request agent.CompletionRequest, sink agent.AssistantTextSink) (agent.CompletionResponse, error) {
	streaming, ok := a.llm.(StreamingLLM)
	if !ok {
		return a.Complete(ctx, request)
	}
	response, err := streaming.StreamComplete(ctx, fromAgentCompletionRequest(request), func(text string) {
		if sink != nil {
			sink(text)
		}
	})
	if err != nil {
		return agent.CompletionResponse{}, err
	}
	return toAgentCompletionResponse(response), nil
}

// eventSinkAdapter converts a public EventSink into an internal agent.EventSink.
func eventSinkAdapter(sink EventSink) agent.EventSink {
	if sink == nil {
		return nil
	}
	return func(event agent.Event) {
		sink(fromAgentEvent(event))
	}
}

// completionCallbackAdapter converts a public CompletionCallback into an internal agent.CompletionCallback.
func completionCallbackAdapter(callback CompletionCallback) agent.CompletionCallback {
	if callback == nil {
		return nil
	}
	return func(ctx context.Context, result agent.RunResult) error {
		return callback(ctx, fromAgentRunResult(result))
	}
}

// fromAgentCompletionRequest translates an internal agent.CompletionRequest to a public CompletionRequest.
func fromAgentCompletionRequest(request agent.CompletionRequest) CompletionRequest {
	messages := make([]Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		messages = append(messages, fromAgentMessage(message))
	}
	tools := make([]ToolDefinition, 0, len(request.Tools))
	for _, tool := range request.Tools {
		tools = append(tools, ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  append(json.RawMessage(nil), tool.Parameters...),
		})
	}
	var toolChoice *ToolChoice
	if request.ToolChoice != nil {
		toolChoice = &ToolChoice{
			Type: request.ToolChoice.Type,
			Name: request.ToolChoice.Name,
		}
	}
	return CompletionRequest{Messages: messages, Tools: tools, ToolChoice: toolChoice}
}

// toAgentCompletionResponse translates a public CompletionResponse to an internal agent.CompletionResponse.
func toAgentCompletionResponse(response CompletionResponse) agent.CompletionResponse {
	calls := make([]agent.ToolCall, 0, len(response.ToolCalls))
	for _, call := range response.ToolCalls {
		calls = append(calls, toAgentToolCall(call))
	}
	return agent.CompletionResponse{
		AssistantText: response.AssistantText,
		ToolCalls:     calls,
		FinishReason:  agent.FinishReason(response.FinishReason),
	}
}

// fromAgentMessage translates an internal agent.Message to a public Message.
func fromAgentMessage(message agent.Message) Message {
	toolCalls := make([]ToolCall, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		toolCalls = append(toolCalls, fromAgentToolCall(call))
	}
	var toolResult *ToolResult
	if message.ToolResult != nil {
		converted := fromAgentToolResult(*message.ToolResult)
		toolResult = &converted
	}
	return Message{
		Role:       Role(message.Role),
		Content:    message.Content,
		ToolCalls:  toolCalls,
		ToolResult: toolResult,
	}
}

// fromAgentEvent translates an internal agent.Event to a public Event.
func fromAgentEvent(event agent.Event) Event {
	var toolCall *ToolCall
	if event.ToolCall != nil {
		converted := fromAgentToolCall(*event.ToolCall)
		toolCall = &converted
	}
	var toolResult *ToolResult
	if event.ToolResult != nil {
		converted := fromAgentToolResult(*event.ToolResult)
		toolResult = &converted
	}
	var commandChunk *CommandChunk
	if event.CommandChunk != nil {
		commandChunk = &CommandChunk{
			Stream: string(event.CommandChunk.Stream),
			Data:   append([]byte(nil), event.CommandChunk.Data...),
		}
	}
	var planRouting *PlanRoutingDecisionEvent
	if event.PlanRouting != nil {
		planRouting = &PlanRoutingDecisionEvent{
			RequiresPlan:  event.PlanRouting.RequiresPlan,
			RequiresTodos: event.PlanRouting.RequiresTodos,
			Reason:        event.PlanRouting.Reason,
			Confidence:    event.PlanRouting.Confidence,
			Signals:       append([]string(nil), event.PlanRouting.Signals...),
			Source:        event.PlanRouting.Source,
		}
	}
	var runResult *RunResult
	if event.RunResult != nil {
		converted := fromAgentRunResult(*event.RunResult)
		runResult = &converted
	}
	return Event{
		Type:         EventType(event.Type),
		Text:         event.Text,
		ToolCall:     toolCall,
		ToolResult:   toolResult,
		CommandChunk: commandChunk,
		PlanRouting:  planRouting,
		RunResult:    runResult,
		Error:        event.Error,
	}
}

// fromAgentToolCall translates an internal agent.ToolCall to a public ToolCall.
func fromAgentToolCall(call agent.ToolCall) ToolCall {
	return ToolCall{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: append(json.RawMessage(nil), call.Arguments...),
	}
}

// toAgentToolCall translates a public ToolCall to an internal agent.ToolCall.
func toAgentToolCall(call ToolCall) agent.ToolCall {
	return agent.ToolCall{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: append(json.RawMessage(nil), call.Arguments...),
	}
}

// fromAgentToolResult translates an internal agent.ToolResult to a public ToolResult.
func fromAgentToolResult(result agent.ToolResult) ToolResult {
	return ToolResult{
		CallID:  result.CallID,
		Name:    result.Name,
		Content: result.Content,
		Payload: append(json.RawMessage(nil), result.Payload...),
		Error:   result.Error,
	}
}

// fromAgentRunResult translates an internal agent.RunResult to a public RunResult.
func fromAgentRunResult(result agent.RunResult) RunResult {
	return RunResult{
		FinalOutput: result.FinalOutput,
		Completed:   result.Completed,
		Error:       result.Error,
	}
}

// CurrentMode returns the session's current v1 agent runtime mode.
func (s *Session) CurrentMode() (Mode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	mode, err := s.currentAgentModeLocked()
	if err != nil {
		return "", err
	}
	return Mode(mode.Current()), nil
}

// EnterPlanMode switches the session into Plan mode and initializes plan state if needed.
func (s *Session) EnterPlanMode() error {
	s.mu.Lock()
	if s.runActive {
		s.mu.Unlock()
		return ErrTopLevelRunActive
	}
	mode, err := s.currentAgentModeLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return mode.EnterPlan()
}

// ExitPlanMode validates the current plan and returns the session to Default mode.
func (s *Session) ExitPlanMode() error {
	s.mu.Lock()
	if s.runActive {
		s.mu.Unlock()
		return ErrTopLevelRunActive
	}
	mode, err := s.currentAgentModeLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return mode.ExitPlan()
}

// ReadPlan returns the current conversation-scoped runtime plan text.
func (s *Session) ReadPlan() (string, error) {
	s.mu.Lock()
	mode, err := s.currentAgentModeLocked()
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	return mode.Plan().Read()
}

// WritePlan replaces the current conversation-scoped runtime plan text.
func (s *Session) WritePlan(plan string) error {
	s.mu.Lock()
	if s.runActive {
		s.mu.Unlock()
		return ErrTopLevelRunActive
	}
	mode, err := s.currentAgentModeLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := agent.ValidatePlan(plan); err != nil {
		return err
	}
	return mode.Plan().Write(plan)
}

// WritePlanAndTodos atomically writes an implementation plan and its initial todos.
func (s *Session) WritePlanAndTodos(plan string, todos []Todo) error {
	if s == nil {
		return ErrSessionClosed
	}
	s.mu.Lock()
	if s.runActive {
		s.mu.Unlock()
		return ErrTopLevelRunActive
	}
	mode, err := s.currentAgentModeLocked()
	stores := s.stores
	s.mu.Unlock()
	if err != nil {
		return err
	}

	conversation := agent.NewConversationState(
		stores.plan,
		stores.todos,
		stores.memory,
		stores.commandEvidence,
	)
	if err := conversation.WritePlanAndTodos(plan, toAgentTodos(todos)); err != nil {
		return err
	}
	return mode.CompletePlanAfterWrite()
}

// currentAgentModeLocked retrieves the active mode state under a session lock.
func (s *Session) currentAgentModeLocked() (*agent.ModeState, error) {
	switch s.state {
	case lifecycleClosed:
		return nil, ErrSessionClosed
	case lifecycleNew:
		return nil, ErrSessionNotStarted
	case lifecycleStarting:
		return nil, ErrSessionStarting
	case lifecycleClosing:
		return nil, ErrSessionClosing
	}
	if s.resources.agentMode == nil {
		return nil, errSessionAgentModeUnavailable
	}
	return s.resources.agentMode, nil
}
