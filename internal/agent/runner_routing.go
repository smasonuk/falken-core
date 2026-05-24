package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	planRoutingToolName    = "decide_plan_mode"
	planRoutingInstruction = `Classify whether the latest user request requires plan mode before implementation.
Call decide_plan_mode exactly once.
Do not solve the task. Do not write a plan.`
)

// PlanRoutingDecision captures whether a request should start in plan mode.
type PlanRoutingDecision struct {
	RequiresPlan  bool     `json:"requires_plan"`
	RequiresTodos bool     `json:"requires_todos"`
	Reason        string   `json:"reason"`
	Confidence    string   `json:"confidence"`
	Signals       []string `json:"signals,omitempty"`
}

// PlanRouter decides whether a user request needs mandatory planning.
type PlanRouter interface {
	RoutePlan(context.Context, PlanRoutingRequest) (PlanRoutingDecision, error)
}

// PlanRouterFunc adapts a function into a PlanRouter.
type PlanRouterFunc func(context.Context, PlanRoutingRequest) (PlanRoutingDecision, error)

// RoutePlan implements PlanRouter.
func (f PlanRouterFunc) RoutePlan(ctx context.Context, request PlanRoutingRequest) (PlanRoutingDecision, error) {
	return f(ctx, request)
}

// PlanRoutingRequest is the prepared-message context passed to test/fallback routers.
type PlanRoutingRequest struct {
	CurrentMode string
	Messages    []Message
}

func (r *Runner) routePreparedMessages(ctx context.Context, messages []Message, events EventSink) error {
	if r == nil || r.mode == nil || r.currentMode() != ModeDefault {
		return nil
	}
	if r.planRouter == nil && !r.autoPlanMode {
		return nil
	}
	requiresSubmission, err := r.requiresImplementationSubmission()
	if err != nil {
		return err
	}
	if requiresSubmission {
		return nil
	}

	decision, source, err := r.planRoutingDecision(ctx, messages)
	if err != nil {
		return fmt.Errorf("route plan mode: %w", err)
	}
	emit(events, PlanRoutingEvent(decision, source))
	if decision.RequiresPlan {
		return r.mode.EnterPlan()
	}
	return nil
}

func (r *Runner) planRoutingDecision(ctx context.Context, messages []Message) (PlanRoutingDecision, string, error) {
	if r.planRouter != nil {
		decision, err := r.planRouter.RoutePlan(ctx, PlanRoutingRequest{
			CurrentMode: string(r.currentMode()),
			Messages:    cloneMessages(messages),
		})
		if err != nil {
			return PlanRoutingDecision{}, "", err
		}
		return decision, "router", nil
	}

	request := CompletionRequest{
		Messages: append(cloneMessages(messages), SystemMessage(planRoutingInstruction)),
		Tools:    []ToolDefinition{decidePlanModeToolDefinition()},
		ToolChoice: &ToolChoice{
			Type: "tool",
			Name: planRoutingToolName,
		},
	}
	response, err := r.llm.Complete(ctx, request)
	if err != nil {
		return PlanRoutingDecision{}, "", err
	}
	decision, err := decodePlanRoutingDecision(response.ToolCalls)
	if err != nil {
		return PlanRoutingDecision{}, "", err
	}
	return decision, "llm", nil
}

func (r *Runner) messagesWithRuntimeMode(messages []Message) []Message {
	return append(cloneMessages(messages), SystemMessage(RenderMode(r.currentMode())))
}

func decidePlanModeToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        planRoutingToolName,
		Description: "Classify whether the latest user request requires plan mode before implementation.",
		Parameters: json.RawMessage(`{
		  "type": "object",
		  "properties": {
		    "requires_plan": {
		      "type": "boolean",
		      "description": "True when the request should start in plan mode before implementation."
		    },
		    "requires_todos": {
		      "type": "boolean",
		      "description": "True when the request should maintain a runtime todo list before implementation."
		    },
		    "reason": {
		      "type": "string",
		      "description": "Brief explanation for the routing decision."
		    },
		    "confidence": {
		      "type": "string",
		      "enum": ["low", "medium", "high"]
		    },
		    "signals": {
		      "type": "array",
		      "items": {"type": "string"},
		      "description": "Complexity or simplicity signals used for the decision."
		    }
		  },
		  "required": ["requires_plan", "reason", "confidence", "signals"]
		}`),
	}
}

func decodePlanRoutingDecision(calls []ToolCall) (PlanRoutingDecision, error) {
	if len(calls) != 1 {
		return PlanRoutingDecision{}, fmt.Errorf("router did not call %s exactly once", planRoutingToolName)
	}
	call := calls[0]
	if call.Name != planRoutingToolName {
		return PlanRoutingDecision{}, fmt.Errorf("router called %q, want %s", call.Name, planRoutingToolName)
	}

	var raw struct {
		RequiresPlan  *bool           `json:"requires_plan"`
		RequiresTodos *bool           `json:"requires_todos"`
		Reason        string          `json:"reason"`
		Confidence    string          `json:"confidence"`
		Signals       json.RawMessage `json:"signals"`
	}
	if err := json.Unmarshal(call.Arguments, &raw); err != nil {
		return PlanRoutingDecision{}, fmt.Errorf("decode %s arguments: %w", planRoutingToolName, err)
	}
	if raw.RequiresPlan == nil {
		return PlanRoutingDecision{}, errors.New("plan routing decision missing requires_plan")
	}
	raw.Reason = strings.TrimSpace(raw.Reason)
	if raw.Reason == "" {
		return PlanRoutingDecision{}, errors.New("plan routing decision missing reason")
	}
	switch raw.Confidence {
	case "low", "medium", "high":
	default:
		return PlanRoutingDecision{}, fmt.Errorf("invalid plan routing confidence: %q", raw.Confidence)
	}
	if raw.Signals == nil {
		return PlanRoutingDecision{}, errors.New("plan routing decision missing signals")
	}
	if strings.TrimSpace(string(raw.Signals)) == "null" {
		return PlanRoutingDecision{}, errors.New("plan routing decision signals must be an array")
	}
	var signals []string
	if err := json.Unmarshal(raw.Signals, &signals); err != nil {
		return PlanRoutingDecision{}, fmt.Errorf("decode %s arguments: signals: %w", planRoutingToolName, err)
	}

	requiresTodos := *raw.RequiresPlan
	if raw.RequiresTodos != nil {
		requiresTodos = *raw.RequiresTodos
	}

	return PlanRoutingDecision{
		RequiresPlan:  *raw.RequiresPlan,
		RequiresTodos: requiresTodos,
		Reason:        raw.Reason,
		Confidence:    raw.Confidence,
		Signals:       append([]string(nil), signals...),
	}, nil
}
