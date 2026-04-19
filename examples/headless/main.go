package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/smasonuk/falken-core/pkg/falken"

	openai "github.com/sashabaranov/go-openai"
)

type headlessInteractions struct{}

func (headlessInteractions) RequestPermission(ctx context.Context, req falken.PermissionRequest) (falken.PermissionResponse, error) {
	return falken.PermissionResponse{Allowed: false, Scope: "deny"}, nil
}

func (headlessInteractions) RequestPlanApproval(ctx context.Context, req falken.PlanApprovalRequest) (falken.PlanApprovalResponse, error) {
	return falken.PlanApprovalResponse{Approved: true}, nil
}

func (headlessInteractions) OnSubmit(ctx context.Context, req falken.SubmitRequest) error {
	fmt.Printf("submitted: %s\n", req.Summary)
	return nil
}

func main() {
	apiKey := os.Getenv("PK")
	if apiKey == "" {
		log.Fatal("PK must be set")
	}

	clientCfg := openai.DefaultConfig(apiKey)
	clientCfg.BaseURL = "https://portkey.syngenta.com/v1"
	clientCfg.HTTPClient = &http.Client{Timeout: 5 * time.Minute}

	session, err := falken.NewSession(falken.Config{
		Client:             openai.NewClientWithConfig(clientCfg),
		ModelName:          "gpt-5.2",
		SystemPrompt:       "You are a headless Falken session.",

		// Default is StateModeFresh. Use StateModeResume to continue from
		// .falken/state/current on a future run.
		// StateMode: falken.StateModeResume,

		InteractionHandler: headlessInteractions{},
		EventHandler: falken.EventHandlerFunc(func(event falken.Event) {
			fmt.Printf("event=%s\n", event.Type)
		}),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close(context.Background())

	if err := session.Start(context.Background()); err != nil {
		log.Fatal(err)
	}

	if err := session.Run(context.Background(), "Summarize the repository layout."); err != nil {
		log.Fatal(err)
	}
}
