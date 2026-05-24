package falken

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type typedLookupArgs struct {
	Email string `json:"email" falken:"required,description=User email address,format=email"`
}

type typedLookupResult struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

func TestFuncTypedToolSuccess(t *testing.T) {
	tool := Func(
		"lookup_user",
		"Look up a user by email.",
		func(_ context.Context, args typedLookupArgs) (typedLookupResult, error) {
			return typedLookupResult{ID: "u_123", Email: args.Email}, nil
		},
		WithToolSafety(ToolSafety{ReadsHostState: true}),
		WithToolCategory("users"),
		WithToolKeywords("lookup", "user"),
		WithToolDefaultLoad(true),
	)
	descriptor := tool.Descriptor()
	if err := ValidateToolDescriptor(descriptor); err != nil {
		t.Fatalf("ValidateToolDescriptor: %v", err)
	}
	if !descriptor.DefaultLoad || descriptor.Category != "users" || descriptor.Safety.ReadsHostState != true {
		t.Fatalf("descriptor options not applied: %+v", descriptor)
	}

	result, err := tool.Execute(context.Background(), ToolInvocation{
		Name:      "lookup_user",
		Arguments: json.RawMessage(`{"email":"user@example.com"}`),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success || result.Status != "ok" {
		t.Fatalf("result = %+v, want success", result)
	}
	var payload typedLookupResult
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.ID != "u_123" || payload.Email != "user@example.com" {
		t.Fatalf("payload = %+v, want typed result", payload)
	}
}

func TestJSONToolInvalidArgumentsReturnToolResult(t *testing.T) {
	tool := JSONTool[typedLookupArgs, typedLookupResult](
		typedLookupDescriptor(),
		func(context.Context, typedLookupArgs) (typedLookupResult, error) {
			t.Fatal("handler should not run")
			return typedLookupResult{}, nil
		},
	)
	result, err := tool.Execute(context.Background(), ToolInvocation{Arguments: json.RawMessage(`{`)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || result.Status != "invalid_arguments" || result.Error == "" {
		t.Fatalf("result = %+v, want invalid_arguments failure", result)
	}
}

func TestJSONToolRejectsUnknownFields(t *testing.T) {
	tool := JSONTool[typedLookupArgs, typedLookupResult](
		typedLookupDescriptor(),
		func(context.Context, typedLookupArgs) (typedLookupResult, error) {
			t.Fatal("handler should not run")
			return typedLookupResult{}, nil
		},
	)
	result, err := tool.Execute(context.Background(), ToolInvocation{Arguments: json.RawMessage(`{"email":"a@example.com","extra":true}`)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || result.Status != "invalid_arguments" || !strings.Contains(result.Error, "unknown field") {
		t.Fatalf("result = %+v, want unknown-field decode failure", result)
	}
}

func TestJSONToolHandlerErrorReturnsToolResult(t *testing.T) {
	tool := JSONTool[typedLookupArgs, typedLookupResult](
		typedLookupDescriptor(),
		func(context.Context, typedLookupArgs) (typedLookupResult, error) {
			return typedLookupResult{}, errors.New("database offline")
		},
	)
	result, err := tool.Execute(context.Background(), ToolInvocation{Arguments: json.RawMessage(`{"email":"a@example.com"}`)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || result.Status != "error" || !strings.Contains(result.Error, "database offline") {
		t.Fatalf("result = %+v, want handler error result", result)
	}
}

func TestFuncInvalidDescriptorFailsThroughValidation(t *testing.T) {
	tool := Func(
		"1bad",
		"",
		func(context.Context, typedLookupArgs) (typedLookupResult, error) {
			return typedLookupResult{}, nil
		},
	)
	provider := StaticToolProvider(tool)
	err := provider.Start(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("Start error = %v, want descriptor validation failure", err)
	}
}

func typedLookupDescriptor() ToolDescriptor {
	return ToolDescriptor{
		Name:        "lookup_user",
		Description: "Look up a user by email.",
		Parameters:  Object(Required("email", String())).JSON(),
	}
}
