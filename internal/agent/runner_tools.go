package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/smasonuk/falken-core/internal/extensions/tools"
)

// ActiveToolProvider exposes currently active runtime tools to the agent loop.
type ActiveToolProvider interface {
	ActiveTools(context.Context) ([]tools.Entry, error)
}

// ActiveToolProviderFunc adapts a function into an ActiveToolProvider.
type ActiveToolProviderFunc func(context.Context) ([]tools.Entry, error)

// ActiveTools returns active tool metadata.
func (f ActiveToolProviderFunc) ActiveTools(ctx context.Context) ([]tools.Entry, error) {
	return f(ctx)
}

// ToolExecutor executes one already-policy-checked active tool call.
type ToolExecutor interface {
	ExecuteTool(context.Context, ToolExecutionRequest) (ToolExecutionResult, error)
}

// ToolExecutorFunc adapts a function into a ToolExecutor.
type ToolExecutorFunc func(context.Context, ToolExecutionRequest) (ToolExecutionResult, error)

// ExecuteTool executes one tool call.
func (f ToolExecutorFunc) ExecuteTool(ctx context.Context, request ToolExecutionRequest) (ToolExecutionResult, error) {
	return f(ctx, request)
}

// ToolExecutionRequest is the runner-facing tool invocation shape.
type ToolExecutionRequest struct {
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Events    EventSink       `json:"-"`
}

// ToolExecutionResult is the runner-facing result shape returned by tool execution services.
type ToolExecutionResult struct {
	Success bool            `json:"success"`
	Status  string          `json:"status,omitempty"`
	Content string          `json:"content,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func (r *Runner) handleToolCall(ctx context.Context, call ToolCall, available []tools.Entry, events EventSink) ToolResult {
	emit(events, ToolCallEvent(call))

	if strings.TrimSpace(call.ID) == "" {
		return toolErrorResult(call, "missing_tool_call_id", "tool call id is required")
	}
	if strings.TrimSpace(call.Name) == "" {
		return toolErrorResult(call, "missing_tool_name", "tool name is required")
	}
	arguments := normalizeToolArguments(call.Arguments)
	if !json.Valid(arguments) {
		return toolErrorResult(call, "malformed_arguments", "tool arguments must be valid JSON")
	}

	entry, active := findTool(call.Name, available)
	if !active {
		return toolErrorResult(call, "inactive_or_unknown_tool", "tool is not active or unknown: "+call.Name)
	}

	decision := IsToolAllowed(r.currentMode(), entry)
	if !decision.Allowed {
		return toolErrorResult(call, "blocked_by_mode", decision.Reason)
	}
	if r.executor == nil {
		return toolErrorResult(call, "tool_executor_unavailable", "tool executor is unavailable")
	}

	execution, err := r.executor.ExecuteTool(ctx, ToolExecutionRequest{
		CallID:    call.ID,
		Name:      call.Name,
		Arguments: arguments,
		Events:    events,
	})
	if err != nil {
		return toolErrorResult(call, "execution_error", err.Error())
	}

	return toolResultFromExecution(call, execution)
}

func toolResultFromExecution(call ToolCall, execution ToolExecutionResult) ToolResult {
	payload := execution.Payload
	if len(payload) == 0 {
		payload = marshalToolPayload(map[string]any{
			"success": execution.Success,
			"status":  execution.Status,
			"content": execution.Content,
			"error":   execution.Error,
		})
	}
	content := execution.Content
	if content == "" && len(payload) != 0 {
		content = string(payload)
	}

	return ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: content,
		Payload: append(json.RawMessage(nil), payload...),
		Error:   execution.Error,
	}
}

func toolErrorResult(call ToolCall, status, reason string) ToolResult {
	payload := marshalToolPayload(map[string]any{
		"success": false,
		"status":  status,
		"error":   reason,
	})
	return ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: reason,
		Payload: payload,
		Error:   reason,
	}
}

func marshalToolPayload(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{"success":false,"status":"internal_error","error":"encode tool result"}`)
	}
	return data
}

func normalizeToolArguments(arguments json.RawMessage) json.RawMessage {
	if strings.TrimSpace(string(arguments)) == "" {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), arguments...)
}

func sanitizeToolCallsForHistory(calls []ToolCall) []ToolCall {
	sanitized := cloneToolCalls(calls)
	for i := range sanitized {
		if strings.TrimSpace(string(sanitized[i].Arguments)) == "" {
			sanitized[i].Arguments = json.RawMessage(`{}`)
			continue
		}
		if !json.Valid(sanitized[i].Arguments) {
			sanitized[i].Arguments = json.RawMessage(`null`)
		}
	}
	return sanitized
}

func findTool(name string, entries []tools.Entry) (tools.Entry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return tools.Entry{}, false
}

func toolResultStatus(result ToolResult) string {
	if len(result.Payload) == 0 {
		return ""
	}
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		return ""
	}
	return payload.Status
}

func toolResultSucceeded(result ToolResult) bool {
	if result.Error != "" || len(result.Payload) == 0 {
		return false
	}
	var payload struct {
		Success *bool `json:"success"`
	}
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		return false
	}
	return payload.Success != nil && *payload.Success
}
