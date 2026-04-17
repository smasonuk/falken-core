package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smasonuk/falken-core/internal/extensions"
	"github.com/smasonuk/falken-core/internal/host"
	"github.com/smasonuk/falken-core/internal/runtimeapi"
	"github.com/smasonuk/falken-core/internal/tasks"

	"github.com/sashabaranov/go-openai"
)

const verificationSystemPrompt = `You are an adversarial QA Verification Specialist. Your job is NOT to confirm the implementation works—it is to try to BREAK it.

You will be given a task description and the output from the developer agent.
You must:
1. Use the 'execute_command' tool to run the code, run the tests, or curl the endpoints.
2. Test edge cases (empty inputs, bad data, boundary limits).
3. If it is a bug fix, verify the bug is actually gone.

You CANNOT edit files. You can only read and execute.
When you are finished, your FINAL output must end with exactly one of these two lines:
VERDICT: PASS
VERDICT: FAIL

If FAIL, explain exactly what broke so the developer agent can fix it.`

type delegatedTaskRequest struct {
	Instructions   string
	RequiredOutput string
	Profile        string
}

type delegatedTaskService struct {
	runner *Runner
}

type delegatedTaskLaunch struct {
	request     delegatedTaskRequest
	taskID      string
	subPaths    runtimeapi.Paths
	subShell    *host.StatefulShell
	subToolRT   extensions.ResourceSet
	allSubTools []any
	subRunner   *Runner
}

func newDelegatedTaskService(runner *Runner) *delegatedTaskService {
	return &delegatedTaskService{runner: runner}
}

func (s *delegatedTaskService) Launch(ctx context.Context, req delegatedTaskRequest) (map[string]any, error) {
	subRunID := fmt.Sprintf("subagent_%d", time.Now().UnixNano())
	subPaths := s.runner.Paths.SubRunPaths(subRunID)
	if err := subPaths.EnsureStateDirs(); err != nil {
		return nil, fmt.Errorf("failed to initialize delegated task state: %v", err)
	}

	taskID, err := s.createTask(req.Instructions)
	if err != nil {
		return nil, err
	}

	launch, launchFailed, err := s.bootstrap(ctx, req, taskID, subPaths)
	if err != nil {
		return nil, err
	}
	if launchFailed != nil {
		return launchFailed, nil
	}

	s.startAsync(launch)
	return map[string]any{
		"status":  "async_launched",
		"task_id": launch.taskID,
		"message": fmt.Sprintf("Successfully launched background sub-agent. Track its progress via TaskList using Task ID: %s. Do not wait for it; move on to other tasks.", launch.taskID),
	}, nil
}

func (s *delegatedTaskService) createTask(instructions string) (string, error) {
	subject := delegatedTaskSubject(instructions)
	taskID, err := s.runner.taskStore.CreateTask("subagent", subject, "Background task delegated by parent agent.", nil, "")
	if err != nil {
		return "", fmt.Errorf("failed to register sub-agent task: %v", err)
	}

	inProgress := tasks.StatusInProgress
	if err := s.runner.taskStore.UpdateTask(taskID, tasks.TaskPatch{Status: &inProgress}); err != nil {
		return "", fmt.Errorf("failed to mark sub-agent task in progress: %v", err)
	}
	return taskID, nil
}

func (s *delegatedTaskService) bootstrap(ctx context.Context, req delegatedTaskRequest, taskID string, subPaths runtimeapi.Paths) (*delegatedTaskLaunch, map[string]any, error) {
	subShell := host.NewStatefulShell(subPaths.WorkspaceDir, subPaths.StateDir, s.runner.Shell.PermManager, s.runner.logger)
	if err := subShell.StartSandbox(ctx, s.runner.SandboxImage); err != nil {
		_ = subShell.Close(context.Background())
		return nil, s.failBeforeLaunch(taskID, fmt.Sprintf("sub-agent sandbox startup failed: %v", err)), nil
	}

	allSubTools, subToolRT, err := extensions.LoadTools(s.runner.ToolDir, subShell)
	if err != nil {
		_ = subShell.Close(context.Background())
		return nil, s.failBeforeLaunch(taskID, fmt.Sprintf("failed to load tools for sub-agent: %v", err)), nil
	}

	subTools := filterDelegatedTools(allSubTools, req.Profile, s.runner.logger)
	subRunner, err := NewRunner(RunnerOptions{
		Client:       s.runner.client,
		ModelName:    s.runner.modelName,
		Tools:        subTools,
		SystemPrompt: s.runner.systemPrompt,
		Logger:       s.runner.logger,
		Shell:        subShell,
		Paths:        subPaths,
		ToolDir:      s.runner.ToolDir,
		PluginDir:    s.runner.PluginDir,
		SandboxImage: s.runner.SandboxImage,
	})
	if err != nil {
		_ = subToolRT.Close(context.Background())
		_ = subShell.Close(context.Background())
		return nil, s.failBeforeLaunch(taskID, fmt.Sprintf("failed to create sub-agent runner: %v", err)), nil
	}
	if req.Profile == "explore" {
		subRunner.Mode = ModeExplore
	}
	subRunner.History = delegatedTaskHistory(s.runner.History, req.Instructions, req.RequiredOutput)

	return &delegatedTaskLaunch{
		request:     req,
		taskID:      taskID,
		subPaths:    subPaths,
		subShell:    subShell,
		subToolRT:   subToolRT,
		allSubTools: allSubTools,
		subRunner:   subRunner,
	}, nil, nil
}

func (s *delegatedTaskService) startAsync(launch *delegatedTaskLaunch) {
	go func() {
		defer launch.subShell.Close(context.Background())
		defer launch.subToolRT.Close(context.Background())

		bgEventChan := make(chan any, 100)
		go launch.subRunner.Run(context.Background(), "", bgEventChan)

		var finalOutput strings.Builder
		for msg := range bgEventChan {
			switch m := msg.(type) {
			case AgentTextMsg:
				finalOutput.WriteString(m.Text)
			case AgentDoneMsg:
				s.handleSubAgentCompletion(launch, finalOutput.String(), m.Error)
				return
			}
		}
	}()
}

func (s *delegatedTaskService) handleSubAgentCompletion(launch *delegatedTaskLaunch, output string, runErr error) {
	taskDir := filepath.Join(s.runner.Paths.TasksDir(), launch.taskID)
	_ = os.MkdirAll(taskDir, 0755)
	resultPath := filepath.Join(taskDir, "result.md")
	_ = os.WriteFile(resultPath, []byte(output), 0644)

	if runErr != nil {
		s.runner.logger.Printf("Sub-agent %s failed: %v", launch.taskID, runErr)
		failed := tasks.StatusFailed
		lastError := "Failed fatally: " + runErr.Error()
		_ = s.runner.taskStore.UpdateTask(launch.taskID, tasks.TaskPatch{
			Status:     &failed,
			LastError:  &lastError,
			ResultPath: &resultPath,
		})
		return
	}

	s.runner.logger.Printf("Sub-agent %s finished coding. Starting Verification...", launch.taskID)
	verifying := tasks.StatusVerifying
	_ = s.runner.taskStore.UpdateTask(launch.taskID, tasks.TaskPatch{Status: &verifying})

	verificationText, verifyErr := s.runVerification(launch, output)
	if verifyErr != nil {
		s.runner.logger.Printf("Failed to create verifier for %s: %v", launch.taskID, verifyErr)
		completed := tasks.StatusCompleted
		summary := "Verification skipped due to error."
		_ = s.runner.taskStore.UpdateTask(launch.taskID, tasks.TaskPatch{
			Status:     &completed,
			Summary:    &summary,
			ResultPath: &resultPath,
		})
		return
	}

	verifyPath := filepath.Join(taskDir, "verify.md")
	_ = os.WriteFile(verifyPath, []byte(verificationText), 0644)

	if strings.Contains(verificationText, "VERDICT: PASS") {
		s.runner.logger.Printf("Sub-agent %s PASSED verification.", launch.taskID)
		completed := tasks.StatusCompleted
		summary := "VERIFIED ✅"
		_ = s.runner.taskStore.UpdateTask(launch.taskID, tasks.TaskPatch{
			Status:     &completed,
			Summary:    &summary,
			ResultPath: &resultPath,
		})
		return
	}

	s.runner.logger.Printf("Sub-agent %s FAILED verification. Kicking back.", launch.taskID)
	failed := tasks.StatusFailed
	lastError := "FAILED QA ❌ See verify.md for details."
	_ = s.runner.taskStore.UpdateTask(launch.taskID, tasks.TaskPatch{
		Status:     &failed,
		LastError:  &lastError,
		ResultPath: &resultPath,
	})
}

func (s *delegatedTaskService) runVerification(launch *delegatedTaskLaunch, output string) (string, error) {
	verifyRunner, err := NewRunner(RunnerOptions{
		Client:       s.runner.client,
		ModelName:    s.runner.modelName,
		Tools:        verificationTools(launch.allSubTools),
		SystemPrompt: verificationSystemPrompt,
		Logger:       s.runner.logger,
		Shell:        launch.subShell,
		Paths:        launch.subPaths.SubRunPaths("verify_" + launch.taskID),
		ToolDir:      s.runner.ToolDir,
		PluginDir:    s.runner.PluginDir,
		SandboxImage: s.runner.SandboxImage,
	})
	if err != nil {
		return "", err
	}
	verifyRunner.Mode = ModeVerify

	verifyPrompt := fmt.Sprintf("Verify this task:\n%s\n\nDeveloper Output:\n%s", launch.request.Instructions, output)
	verifyChan := make(chan any, 100)
	go verifyRunner.Run(context.Background(), verifyPrompt, verifyChan)

	var verifyOutput strings.Builder
	for vMsg := range verifyChan {
		if text, ok := vMsg.(AgentTextMsg); ok {
			verifyOutput.WriteString(text.Text)
		}
		if _, ok := vMsg.(AgentDoneMsg); ok {
			break
		}
	}
	return verifyOutput.String(), nil
}

func (s *delegatedTaskService) failBeforeLaunch(taskID, failure string) map[string]any {
	s.runner.logger.Printf("Sub-agent %s failed before launch: %s", taskID, failure)
	failed := tasks.StatusFailed
	lastError := failure
	_ = s.runner.taskStore.UpdateTask(taskID, tasks.TaskPatch{
		Status:    &failed,
		LastError: &lastError,
	})

	return map[string]any{
		"status":  "launch_failed",
		"task_id": taskID,
		"message": fmt.Sprintf("Sub-agent launch failed. Task %s has been marked failed: %s", taskID, failure),
	}
}

func delegatedTaskSubject(instructions string) string {
	subject := fmt.Sprintf("Sub-agent: %s", instructions)
	if len(subject) > 50 {
		return subject[:47] + "..."
	}
	return subject
}

func delegatedTaskHistory(parent []openai.ChatCompletionMessage, instructions, requiredOutput string) []openai.ChatCompletionMessage {
	history := make([]openai.ChatCompletionMessage, len(parent))
	copy(history, parent)
	if len(history) > 0 && history[len(history)-1].Role == openai.ChatMessageRoleAssistant {
		history = history[:len(history)-1]
	}

	directivePrompt := fmt.Sprintf("[SUB-AGENT DIRECTIVE]\nYou are a forked sub-agent. Inherit the context above, but focus EXCLUSIVELY on this task:\n%s\n\nRequired Output:\n%s", instructions, requiredOutput)
	return append(history, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: directivePrompt,
	})
}

func filterDelegatedTools(allTools []any, profile string, logger loggerLike) []any {
	var filtered []any
	for _, tool := range allTools {
		runnableTool, ok := tool.(interface {
			Definition() openai.FunctionDefinition
		})
		if !ok {
			continue
		}
		name := runnableTool.Definition().Name
		if profile == "explore" && !isDelegatedReadOnlyTool(name) {
			if logger != nil {
				logger.Printf("Skipping tool %s for explore profile", name)
			}
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func verificationTools(allTools []any) []any {
	var filtered []any
	for _, tool := range allTools {
		runnableTool, ok := tool.(interface {
			Definition() openai.FunctionDefinition
		})
		if !ok {
			continue
		}
		if isVerificationReadTool(runnableTool.Definition().Name) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func isDelegatedReadOnlyTool(name string) bool {
	return name == "read_file" || name == "read_files" || name == "glob" || name == "grep" || name == "search_tools"
}

func isVerificationReadTool(name string) bool {
	return name == "read_file" || name == "read_files" || name == "glob" || name == "grep"
}

type loggerLike interface {
	Printf(string, ...any)
}
