package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultMaxToolRounds = 100

var (
	// ErrRunnerDependency indicates a required runner dependency was not provided.
	ErrRunnerDependency = errors.New("agent runner dependency is required")
	// ErrMaxToolRoundsExceeded indicates the agent loop exceeded its configured tool-round guard.
	ErrMaxToolRoundsExceeded = errors.New("maximum tool rounds exceeded")
)

// RunnerConfig configures the core agent loop.
type RunnerConfig struct {
	LLM              LLM
	History          *HistoryManager
	State            *ConversationState
	Mode             *ModeState
	Tools            ActiveToolProvider
	Executor         ToolExecutor
	BaseSystemPrompt string
	MaxToolRounds    int
	PlanRouter       PlanRouter
	AutoPlanMode     bool
}

// Runner orchestrates LLM turns, mode-filtered tool exposure, tool execution, events, and history.
type Runner struct {
	llm              LLM
	history          *HistoryManager
	state            *ConversationState
	mode             *ModeState
	tools            ActiveToolProvider
	executor         ToolExecutor
	baseSystemPrompt string
	maxToolRounds    int
	planRouter       PlanRouter
	autoPlanMode     bool
}

// NewRunner creates a core agent loop runner.
func NewRunner(config RunnerConfig) (*Runner, error) {
	if config.LLM == nil {
		return nil, fmt.Errorf("%w: llm", ErrRunnerDependency)
	}
	if config.History == nil {
		return nil, fmt.Errorf("%w: history", ErrRunnerDependency)
	}
	if config.Tools == nil {
		return nil, fmt.Errorf("%w: tools", ErrRunnerDependency)
	}
	mode := config.Mode
	if mode == nil {
		mode = &ModeState{mode: ModeDefault}
	}
	config.History.mode = mode
	maxToolRounds := config.MaxToolRounds
	if maxToolRounds <= 0 {
		maxToolRounds = defaultMaxToolRounds
	}

	return &Runner{
		llm:              config.LLM,
		history:          config.History,
		state:            config.State,
		mode:             mode,
		tools:            config.Tools,
		executor:         config.Executor,
		baseSystemPrompt: config.BaseSystemPrompt,
		maxToolRounds:    maxToolRounds,
		planRouter:       config.PlanRouter,
		autoPlanMode:     config.AutoPlanMode,
	}, nil
}

// Run executes the v1 agent loop until the model stops requesting tools or a loop guard trips.
func (r *Runner) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	events := request.Events
	messages, err := r.history.PrepareRun(PrepareRunRequest{
		BaseSystemPrompt: r.baseSystemPrompt,
		UserPrompt:       request.Prompt,
	})
	if err != nil {
		return r.finishFailure(events, "", err)
	}
	if err := r.routePreparedMessages(ctx, messages, events); err != nil {
		return r.finishFailure(events, "", err)
	}

	toolRounds := 0
	finalOutput := ""
	failures := newRepeatedFailureTracker()
	implementationAccepted := false
	workspaceMutatedThisRun := false
	todoProgressNudgeAppended := false
	submitRequiredNudgeAppended := false
	for {
		available, err := r.tools.ActiveTools(ctx)
		if err != nil {
			return r.finishFailure(events, finalOutput, err)
		}
		allowed, err := FilterTools(r.currentMode(), available)
		if err != nil {
			return r.finishFailure(events, finalOutput, err)
		}

		response, streamedAssistant, streamedText, err := r.complete(ctx, CompletionRequest{
			Messages: r.messagesWithRuntimeMode(messages),
			Tools:    ToolDefinitionsFromEntries(allowed),
		}, events)
		if err != nil {
			return r.finishFailure(events, finalOutput, err)
		}
		if todoProgressNudgeAppended {
			messages = removeTrailingTodoProgressNudge(messages)
			todoProgressNudgeAppended = false
		}
		assistantText := response.AssistantText
		if assistantText == "" && streamedAssistant {
			assistantText = streamedText
		}

		if assistantText != "" {
			finalOutput = assistantText
			if !streamedAssistant {
				emit(events, AssistantTextEvent(assistantText))
			}
		}
		if len(response.ToolCalls) != 0 && toolRounds >= r.maxToolRounds {
			err := fmt.Errorf("%w: limit=%d", ErrMaxToolRoundsExceeded, r.maxToolRounds)
			return r.finishFailure(events, finalOutput, err)
		}
		if assistantText != "" || len(response.ToolCalls) != 0 {
			assistant := AssistantMessage(assistantText, sanitizeToolCallsForHistory(response.ToolCalls)...)
			if err := r.history.Append(assistant); err != nil {
				return r.finishFailure(events, finalOutput, err)
			}
			messages = append(messages, assistant)
		}

		if len(response.ToolCalls) == 0 {
			requiresSubmission, err := r.requiresImplementationSubmission()
			if err != nil {
				return r.finishFailure(events, finalOutput, err)
			}
			if requiresSubmission && !implementationAccepted {
				if submitRequiredNudgeAppended {
					return r.finishFailure(events, finalOutput, ErrImplementationSubmissionRequired)
				}
				if toolRounds >= r.maxToolRounds {
					err := fmt.Errorf("%w: implementation submission required", ErrMaxToolRoundsExceeded)
					return r.finishFailure(events, finalOutput, err)
				}
				nudge := SystemMessage(renderSubmitPlanImplementationRequired())
				if err := r.history.Append(nudge); err != nil {
					return r.finishFailure(events, finalOutput, err)
				}
				messages = append(messages, nudge)
				submitRequiredNudgeAppended = true
				toolRounds++
				continue
			}
			return r.finishSuccess(ctx, events, request.OnCompleted, finalOutput)
		}
		toolRounds++

		todoProgressNudgeNeeded := false
		todoProgressUpdatedThisRound := false
		for _, call := range response.ToolCalls {
			result := r.handleToolCall(ctx, call, available, events)
			repeated := failures.Record(call, result)
			if repeated.Count >= 2 {
				result = withRepeatedFailureWarning(result)
			}
			emit(events, ToolResultEvent(result))
			message := ToolResultMessage(result)
			if err := r.history.Append(message); err != nil {
				return r.finishFailure(events, finalOutput, err)
			}
			messages = append(messages, message)
			if r.currentMode() == ModePlan && call.Name == "write_plan" && readyForImplementation(result) {
				if err := r.mode.CompletePlanAfterWrite(); err != nil {
					return r.finishFailure(events, finalOutput, err)
				}
			}
			if call.Name == "submit_plan_implementation" && submissionAccepted(result) {
				implementationAccepted = true
			}
			if todoOrPlanMutationAccepted(result) {
				implementationAccepted = false
			}
			if call.Name == "write_todos" && toolResultSucceeded(result) {
				todoProgressUpdatedThisRound = true
			}
			entry, _ := findTool(call.Name, available)
			mutated := workspaceMutationObserved(result, entry)
			if mutated && isManagedWorkspaceMutationTool(result.Name) && r.state != nil {
				if _, err := r.state.RecordWorkspaceMutation(result.Name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
					return r.finishFailure(events, finalOutput, err)
				}
			}
			if shouldNudgeTodoProgress(result, mutated, workspaceMutatedThisRun) {
				todoProgressNudgeNeeded = true
			}
			if mutated {
				workspaceMutatedThisRun = true
			}
			if repeated.Count >= 3 {
				err := fmt.Errorf("%w: tool=%s status=%s error=%s", ErrRepeatedToolFailure, result.Name, repeated.Status, repeated.Error)
				return r.finishFailure(events, finalOutput, err)
			}
		}
		if todoProgressNudgeNeeded && !todoProgressUpdatedThisRound {
			nudge, err := r.todoProgressNudge()
			if err != nil {
				return r.finishFailure(events, finalOutput, err)
			}
			if strings.TrimSpace(nudge.Content) != "" {
				messages = append(messages, nudge)
				todoProgressNudgeAppended = true
			}
		}
	}
}

func (r *Runner) currentMode() Mode {
	if r == nil || r.mode == nil {
		return ModeDefault
	}
	return r.mode.Current()
}

func emit(sink EventSink, event Event) {
	if sink != nil {
		sink(event)
	}
}
