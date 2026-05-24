package falken_test

import (
	"context"
	"os"

	"github.com/smasonuk/falken-core/pkg/falken"
)

type exampleLLM struct{}

func (exampleLLM) Complete(context.Context, falken.CompletionRequest) (falken.CompletionResponse, error) {
	return falken.CompletionResponse{
		AssistantText: "ready",
		FinishReason:  falken.FinishReasonStop,
	}, nil
}

func ExampleNew() {
	workspace, err := os.MkdirTemp("", "falken-workspace-*")
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = os.RemoveAll(workspace)
	}()

	session, err := falken.New(falken.Config{
		WorkspaceDir:     workspace,
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              exampleLLM{},
		Events: func(event falken.Event) {
			// Hosts can route events to logs, UI streams, or telemetry.
			_ = event
		},
	})
	if err != nil {
		panic(err)
	}
	if err := session.Start(); err != nil {
		panic(err)
	}
	defer func() {
		_ = session.Close()
	}()

	_, _ = session.Run(context.Background(), falken.RunRequest{Prompt: "Hello, Falken"})
}

func ExampleSession_Run() {
	workspace, err := os.MkdirTemp("", "falken-workspace-*")
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = os.RemoveAll(workspace)
	}()

	session, err := falken.New(falken.Config{
		WorkspaceDir:     workspace,
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              exampleLLM{},
		Events: func(event falken.Event) {
			switch event.Type {
			case falken.EventAssistantText, falken.EventRunCompleted, falken.EventRunFailed:
				// Forward stable host-facing events to the embedding app.
				_ = event
			}
		},
	})
	if err != nil {
		panic(err)
	}
	if err := session.Start(); err != nil {
		panic(err)
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.Run(context.Background(), falken.RunRequest{Prompt: "Summarize the workspace"})
	if err != nil {
		panic(err)
	}
	_ = result.FinalOutput
}

func ExampleSession_ResetConversationState() {
	workspace, err := os.MkdirTemp("", "falken-workspace-*")
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = os.RemoveAll(workspace)
	}()

	session, err := falken.New(falken.Config{
		WorkspaceDir:     workspace,
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              exampleLLM{},
	})
	if err != nil {
		panic(err)
	}
	if err := session.Start(); err != nil {
		panic(err)
	}
	defer func() {
		_ = session.Close()
	}()

	if _, err := session.Run(context.Background(), falken.RunRequest{Prompt: "First run"}); err != nil {
		panic(err)
	}
	if err := session.ResetConversationState(); err != nil {
		panic(err)
	}
	_, _ = session.Run(context.Background(), falken.RunRequest{Prompt: "Fresh conversation"})
}

func ExampleNewAgent_toolOnly() {
	ctx := context.Background()

	type lookupUserArgs struct {
		Email string `json:"email" falken:"required,description=User email address,format=email"`
	}
	type userRecord struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	lookupUserTool := falken.Func(
		"lookup_user",
		"Look up a user by email.",
		func(context.Context, lookupUserArgs) (userRecord, error) {
			return userRecord{Email: "user@example.com", Name: "Example User"}, nil
		},
	)

	agent, err := falken.NewAgent(ctx, falken.AgentConfig{
		LLM:          exampleLLM{},
		SystemPrompt: "Help with user account questions.",
		Tools:        []falken.Tool{lookupUserTool},
	})
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = agent.Close(ctx)
	}()

	_, _ = agent.Run(ctx, "Help with this account")
}

func ExampleNewAgent_readOnlyDocs() {
	ctx := context.Background()
	docs, err := os.MkdirTemp("", "falken-docs-*")
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = os.RemoveAll(docs)
	}()

	agent, err := falken.NewAgent(ctx, falken.AgentConfig{
		LLM:           exampleLLM{},
		SystemPrompt:  "Answer only using the files in the docs directory.",
		ReadDirectory: docs,
	})
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = agent.Close(ctx)
	}()

	_, _ = agent.Run(ctx, "Summarize the auth docs")
}

func ExampleFunc() {
	type weatherArgs struct {
		City string `json:"city" falken:"required,description=City name"`
	}
	type weatherResult struct {
		Summary string `json:"summary"`
	}

	weather := falken.Func(
		"get_weather",
		"Get the weather for a city.",
		func(context.Context, weatherArgs) (weatherResult, error) {
			return weatherResult{Summary: "clear"}, nil
		},
	)

	_ = weather.Descriptor()
}
