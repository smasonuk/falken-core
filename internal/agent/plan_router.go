package agent

import (
	"context"
	"strings"
)

// NewHeuristicPlanRouter returns a deterministic fallback/test router.
// Production session construction uses this router by default to avoid an
// extra LLM call for simple default-mode prompts.
func NewHeuristicPlanRouter() PlanRouter {
	return heuristicPlanRouter{}
}

type heuristicPlanRouter struct{}

func (heuristicPlanRouter) RoutePlan(_ context.Context, request PlanRoutingRequest) (PlanRoutingDecision, error) {
	text := strings.ToLower(latestUserRequest(request.Messages))
	signals := make([]string, 0)
	addSignal := func(signal string, needles ...string) {
		for _, needle := range needles {
			if strings.Contains(text, needle) {
				signals = append(signals, signal)
				return
			}
		}
	}

	addSignal("production_ready", "production-ready", "production ready")
	addSignal("new_application", "from scratch", "new application", "build the application")
	addSignal("multi_file_change", "multi-package", "multi package", "modular", "project structure")
	addSignal("database_schema", "sqlite", "database", "schema", "tables")
	addSignal("concurrency", "concurrent", "concurrency", "goroutine", "worker pool")
	addSignal("unit_tests", "unit test", "unit tests", "table-driven", "tests")
	addSignal("configuration", "yaml", "configuration", "config file")

	if len(signals) >= 3 {
		return PlanRoutingDecision{
			RequiresPlan:  true,
			RequiresTodos: true,
			Reason:        "The request contains multiple complexity signals and should start with a deliberate implementation plan.",
			Confidence:    "high",
			Signals:       signals,
		}, nil
	}
	return PlanRoutingDecision{
		RequiresPlan:  false,
		RequiresTodos: false,
		Reason:        "Simple or low-risk request without enough planning signals.",
		Confidence:    "high",
		Signals:       signals,
	}, nil
}

func latestUserRequest(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			return messages[i].Content
		}
	}
	return ""
}
