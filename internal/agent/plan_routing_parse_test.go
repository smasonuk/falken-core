package agent

import (
	"encoding/json"
	"testing"
)

func TestDecodePlanRoutingDecisionValidToolCall(t *testing.T) {
	decision, err := decodePlanRoutingDecision([]ToolCall{{
		ID:        "route-1",
		Name:      planRoutingToolName,
		Arguments: json.RawMessage(`{"requires_plan":true,"reason":"complex","confidence":"high","signals":["multi_file"]}`),
	}})
	if err != nil {
		t.Fatalf("decodePlanRoutingDecision: %v", err)
	}
	if !decision.RequiresPlan || !decision.RequiresTodos || decision.Reason != "complex" || decision.Confidence != "high" || len(decision.Signals) != 1 {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecodePlanRoutingDecisionSupportsRequiresTodos(t *testing.T) {
	decision, err := decodePlanRoutingDecision([]ToolCall{{
		ID:        "route-1",
		Name:      planRoutingToolName,
		Arguments: json.RawMessage(`{"requires_plan":true,"requires_todos":false,"reason":"complex","confidence":"high","signals":["multi_file"]}`),
	}})
	if err != nil {
		t.Fatalf("decodePlanRoutingDecision: %v", err)
	}
	if !decision.RequiresPlan || decision.RequiresTodos {
		t.Fatalf("decision = %+v, want requires_plan true and requires_todos false", decision)
	}
}

func TestDecodePlanRoutingDecisionRejectsInvalidShape(t *testing.T) {
	tests := []json.RawMessage{
		json.RawMessage(`not-json`),
		json.RawMessage(`{"reason":"missing requires_plan","confidence":"high","signals":[]}`),
		json.RawMessage(`{"requires_plan":false,"reason":"","confidence":"high","signals":[]}`),
		json.RawMessage(`{"requires_plan":false,"reason":"ok","confidence":"certain","signals":[]}`),
		json.RawMessage(`{"requires_plan":false,"reason":"ok","confidence":"high"}`),
		json.RawMessage(`{"requires_plan":false,"reason":"ok","confidence":"high","signals":null}`),
		json.RawMessage(`{"requires_plan":false,"reason":"ok","confidence":"high","signals":"simple"}`),
	}
	for _, input := range tests {
		_, err := decodePlanRoutingDecision([]ToolCall{{
			ID:        "route-1",
			Name:      planRoutingToolName,
			Arguments: input,
		}})
		if err == nil {
			t.Fatalf("decodePlanRoutingDecision(%q) succeeded, want error", input)
		}
	}
}

func TestDecodePlanRoutingDecisionRejectsMissingOrWrongToolCall(t *testing.T) {
	if _, err := decodePlanRoutingDecision(nil); err == nil {
		t.Fatal("decodePlanRoutingDecision with no calls succeeded, want error")
	}
	if _, err := decodePlanRoutingDecision([]ToolCall{{Name: planRoutingToolName}, {Name: planRoutingToolName}}); err == nil {
		t.Fatal("decodePlanRoutingDecision with multiple calls succeeded, want error")
	}
	if _, err := decodePlanRoutingDecision([]ToolCall{{Name: "some_other_tool", Arguments: json.RawMessage(`{}`)}}); err == nil {
		t.Fatal("decodePlanRoutingDecision with wrong tool succeeded, want error")
	}
}
