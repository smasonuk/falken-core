package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/smasonuk/falken-core/internal/extensions"
	"github.com/smasonuk/falken-core/internal/host"
	"github.com/smasonuk/falken-core/internal/runtimeapi"
	"github.com/smasonuk/falken-core/internal/tasks"
	"github.com/smasonuk/falken-core/internal/todo"

	"github.com/sashabaranov/go-openai"
)

type RunnerMode string

const (
	ModeDefault RunnerMode = "default"
	ModePlan    RunnerMode = "plan"
	ModeVerify  RunnerMode = "verify"
	ModeExplore RunnerMode = "explore"
)

type PlanInitiator string

const (
	PlanInitiatorAgent PlanInitiator = "agent"
	PlanInitiatorUser  PlanInitiator = "user"
)

type Runner struct {
	client        *openai.Client
	modelName     string
	ToolRegistry  map[string]any // Interface with Definition() and Run()
	ActiveTools   map[string]any
	tools         []openai.Tool // The active slice for the API request
	systemPrompt  string
	logger        *log.Logger
	History       []openai.ChatCompletionMessage
	LogPath       string
	taskStore     *tasks.TaskStore
	todoStore     *todo.TodoStore
	memoryStore   *MemoryStore
	planStore     *PlanStore
	mu            sync.Mutex
	Mode          RunnerMode
	PlanInitiator PlanInitiator
	Shell         *host.StatefulShell
	Paths         runtimeapi.Paths
	Interactions  runtimeapi.InteractionHandler
	ToolDir       string
	PluginDir     string
	SandboxImage  string

	conversation *conversationEngine
	toolExecutor *toolExecutor
	modePolicy   *modePolicy
	history      *historyManager
}

type RunnerOptions struct {
	Client             *openai.Client
	ModelName          string
	Tools              []any
	SystemPrompt       string
	Logger             *log.Logger
	Shell              *host.StatefulShell
	Paths              runtimeapi.Paths
	InteractionHandler runtimeapi.InteractionHandler
	ToolDir            string
	PluginDir          string
	SandboxImage       string
}

func NewRunner(opts RunnerOptions) (*Runner, error) {
	client := opts.Client
	modelName := opts.ModelName
	tools := opts.Tools
	systemPrompt := opts.SystemPrompt
	logger := opts.Logger
	shell := opts.Shell
	paths := opts.Paths
	if paths.WorkspaceDir == "" || paths.StateDir == "" {
		derived, err := runtimeapi.NewPaths("", "")
		if err == nil {
			paths = derived
		}
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	r := &Runner{
		client:       client,
		modelName:    modelName,
		ToolRegistry: make(map[string]any),
		ActiveTools:  make(map[string]any),
		systemPrompt: systemPrompt,
		logger:       logger,
		LogPath:      paths.HistoryPath(),
		taskStore:    tasks.NewTaskStore(paths.TasksPath()),
		todoStore:    todo.NewTodoStore(paths.TodosPath()),
		memoryStore:  NewMemoryStore(paths.MemoryPath()),
		planStore:    NewPlanStore(paths.PlanPath()),
		Mode:         ModeDefault,
		Shell:        shell,
		Paths:        paths,
		Interactions: opts.InteractionHandler,
		ToolDir:      opts.ToolDir,
		PluginDir:    opts.PluginDir,
		SandboxImage: opts.SandboxImage,
		conversation: &conversationEngine{},
		toolExecutor: &toolExecutor{},
		modePolicy:   &modePolicy{},
		history:      &historyManager{},
	}

	_ = os.MkdirAll(paths.StateDir, 0755)
	_ = paths.EnsureStateDirs(false)
	if r.ToolDir == "" && paths.WorkspaceDir != "" {
		r.ToolDir = filepath.Join(paths.WorkspaceDir, "tools")
	}
	if r.PluginDir == "" && paths.WorkspaceDir != "" {
		r.PluginDir = filepath.Join(paths.WorkspaceDir, "plugins")
	}
	if r.SandboxImage == "" && shell != nil && shell.PermManager != nil && shell.PermManager.Config != nil {
		r.SandboxImage = shell.PermManager.Config.SandboxImage
	}
	if r.SandboxImage == "" {
		r.SandboxImage = "falken/sandbox:latest"
	}

	// Load existing history if log file exists
	if _, err := os.Stat(r.LogPath); err == nil {
		f, err := os.Open(r.LogPath)
		if err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				var msg openai.ChatCompletionMessage
				if err := json.Unmarshal(scanner.Bytes(), &msg); err == nil {
					r.History = append(r.History, msg)
				}
			}
		}
	}

	for _, t := range tools {
		runnableTool, ok := t.(interface {
			Definition() openai.FunctionDefinition
		})
		if !ok {
			return nil, fmt.Errorf("tool %T does not expose a definition", t)
		}
		def := runnableTool.Definition()
		r.ToolRegistry[def.Name] = t

		// Filter based on AlwaysLoad
		alwaysLoad := false
		if wasmTool, ok := t.(*extensions.WasmTool); ok {
			alwaysLoad = wasmTool.ToolDef.AlwaysLoad
		} else {
			// Host tools should probably always be loaded by default
			alwaysLoad = true
		}

		if alwaysLoad {
			r.ActiveTools[def.Name] = t
			r.tools = append(r.tools, openai.Tool{
				Type:     openai.ToolTypeFunction,
				Function: &def,
			})
		}
	}

	// Inject agent host tool if not already in tools
	if _, exists := r.ToolRegistry["agent"]; !exists {
		dtTool := &delegateTaskTool{runner: r}
		def := dtTool.Definition()
		r.ToolRegistry[def.Name] = dtTool
		r.ActiveTools[def.Name] = dtTool
		r.tools = append(r.tools, openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &def,
		})
	}

	// Inject search_tools host tool
	if _, exists := r.ToolRegistry["search_tools"]; !exists {
		stTool := &searchToolsTool{runner: r}
		def := stTool.Definition()
		r.ToolRegistry[def.Name] = stTool
		r.ActiveTools[def.Name] = stTool
		r.tools = append(r.tools, openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &def,
		})
	}

	// Inject execute_command host tool
	if _, exists := r.ToolRegistry["execute_command"]; !exists {
		execTool := &ExecuteCommandTool{runner: r}
		def := execTool.Definition()
		r.ToolRegistry[def.Name] = execTool
		r.ActiveTools[def.Name] = execTool
		r.tools = append(r.tools, openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &def,
		})
	}

	// Inject Task tools
	taskTools := []any{
		&TaskCreateTool{store: r.taskStore},
		&TaskListTool{store: r.taskStore},
		&TaskGetTool{store: r.taskStore},
		&TaskUpdateTool{store: r.taskStore},
	}
	for _, t := range taskTools {
		def := t.(interface {
			Definition() openai.FunctionDefinition
		}).Definition()
		r.ToolRegistry[def.Name] = t
		r.ActiveTools[def.Name] = t
		r.tools = append(r.tools, openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &def,
		})
	}

	// Inject Todo tools
	todoTools := []any{
		&TodoWriteTool{store: r.todoStore},
	}
	for _, t := range todoTools {
		def := t.(interface {
			Definition() openai.FunctionDefinition
		}).Definition()
		r.ToolRegistry[def.Name] = t
		r.ActiveTools[def.Name] = t
		r.tools = append(r.tools, openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &def,
		})
	}

	// Inject Plan Mode tools
	planTools := []any{
		&EnterPlanModeTool{runner: r},
		&ExitPlanModeTool{runner: r},
		&WritePlanTool{runner: r},
		&ReadPlanTool{runner: r},
		&UpdateMemoryTool{runner: r},
		&SubmitTaskTool{runner: r},
	}
	for _, t := range planTools {
		def := t.(interface {
			Definition() openai.FunctionDefinition
		}).Definition()
		r.ToolRegistry[def.Name] = t
		r.ActiveTools[def.Name] = t
		r.tools = append(r.tools, openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &def,
		})
	}

	return r, nil
}

// ActivateTool dynamically activates a tool during a loop iteration
func (r *Runner) ActivateTool(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tool, exists := r.ToolRegistry[name]
	if !exists {
		return fmt.Errorf("tool %q not found in registry", name)
	}
	if _, active := r.ActiveTools[name]; active {
		return nil // Already active
	}

	r.ActiveTools[name] = tool

	runnableTool, ok := tool.(interface {
		Definition() openai.FunctionDefinition
	})
	if !ok {
		return fmt.Errorf("tool %T does not expose a definition", tool)
	}
	def := runnableTool.Definition()

	r.tools = append(r.tools, openai.Tool{
		Type:     openai.ToolTypeFunction,
		Function: &def,
	})
	r.logger.Printf("Dynamically activated tool: %s", name)
	return nil
}

func (r *Runner) ClearHistory() {
	r.History = nil
}

// ForcePlanMode puts the runner into plan mode and initializes the plan.
func (r *Runner) ForcePlanMode(userInitiated bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.Mode = ModePlan
	if userInitiated {
		r.PlanInitiator = PlanInitiatorUser
	} else {
		r.PlanInitiator = PlanInitiatorAgent
	}

	// Initialize the plan
	_ = r.planStore.Write("# Implementation Plan\n\n")
}

// ResetConversationState reinitializes the runner's stores with new paths.
func (r *Runner) ResetConversationState(paths runtimeapi.Paths) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.History = nil
	r.LogPath = paths.HistoryPath()
	r.taskStore = tasks.NewTaskStore(paths.TasksPath())
	r.todoStore = todo.NewTodoStore(paths.TodosPath())
	r.memoryStore = NewMemoryStore(paths.MemoryPath())
	r.planStore = NewPlanStore(paths.PlanPath())
	r.Paths = paths

	return nil
}

func (r *Runner) appendToLog(msg openai.ChatCompletionMessage) error {
	return r.history.appendToLog(r, msg)
}

func (r *Runner) prepareHistory(prompt string) {
	r.history.prepareHistory(r, prompt)
}

func (r *Runner) executeToolCall(ctx context.Context, tc openai.ToolCall, eventChan chan<- any) openai.ChatCompletionMessage {
	return r.toolExecutor.executeToolCall(r, ctx, tc, eventChan)
}

func (r *Runner) summarizeDroppedHistory(dropped []openai.ChatCompletionMessage) {
	r.history.summarizeDroppedHistory(r, dropped)
}

func (r *Runner) streamCompletion(ctx context.Context, req openai.ChatCompletionRequest, eventChan chan<- any) (string, []openai.ToolCall, openai.FinishReason, error) {
	return r.conversation.streamCompletion(r, ctx, req, eventChan)
}

func (r *Runner) toolsForCurrentMode() []openai.Tool {
	return r.modePolicy.toolsForCurrentMode(r)
}

func (r *Runner) Run(ctx context.Context, prompt string, eventChan chan<- any) (err error) {
	defer func() {
		eventChan <- AgentDoneMsg{Error: err}
		close(eventChan)
	}()

	r.prepareHistory(prompt)

	for {
		r.history.compactHistory(r)
		r.history.refreshMemoryPrompt(r)

		req := openai.ChatCompletionRequest{
			Model:    r.modelName,
			Messages: r.History,
			Tools:    r.toolsForCurrentMode(),
			Stream:   true,
		}

		fullContent, toolCalls, finishReason, err := r.streamCompletion(ctx, req, eventChan)
		if err != nil {
			return err
		}

		// Append assistant response to history
		assistantMsg := openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleAssistant,
			Content: fullContent,
		}
		if len(toolCalls) > 0 {
			assistantMsg.ToolCalls = toolCalls
		}
		r.History = append(r.History, assistantMsg)
		r.appendToLog(assistantMsg)

		if finishReason != openai.FinishReasonToolCalls && len(toolCalls) == 0 {
			r.logger.Println("=== AGENT TURN COMPLETE ===")
			break
		}

		// Execute tools
		for _, tc := range toolCalls {
			toolMsg := r.executeToolCall(ctx, tc, eventChan)
			r.History = append(r.History, toolMsg)
			r.appendToLog(toolMsg)
		}
	}

	return nil
}

func mergeToolCallChunks(existing []openai.ToolCall, delta []openai.ToolCall) []openai.ToolCall {
	for _, d := range delta {
		if d.Index == nil {
			continue
		}
		idx := *d.Index
		for len(existing) <= idx {
			existing = append(existing, openai.ToolCall{})
		}
		if d.ID != "" {
			existing[idx].ID = d.ID
		}
		if d.Type != "" {
			existing[idx].Type = d.Type
		}
		if d.Function.Name != "" {
			existing[idx].Function.Name += d.Function.Name
		}
		if d.Function.Arguments != "" {
			existing[idx].Function.Arguments += d.Function.Arguments
		}
	}
	return existing
}

func parseArgs(args string) map[string]any {
	var m map[string]any
	json.Unmarshal([]byte(args), &m)
	return m
}
