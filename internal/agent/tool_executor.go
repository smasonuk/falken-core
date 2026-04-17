package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/smasonuk/falken-core/internal/runtimectx"

	"github.com/sashabaranov/go-openai"
)

type toolExecutor struct{}

func (e *toolExecutor) executeToolCall(r *Runner, ctx context.Context, tc openai.ToolCall, eventChan chan<- any) openai.ChatCompletionMessage {
	args := parseArgs(tc.Function.Arguments)
	eventChan <- ToolCallMsg{Name: tc.Function.Name, Args: args}

	if errMsg, blocked := r.modePolicy.blockedToolMessage(r, tc.Function.Name, args); blocked {
		return e.errorToolMessage(tc, eventChan, errMsg)
	}

	var toolResult map[string]any
	t, ok := r.ActiveTools[tc.Function.Name]
	if !ok {
		toolResult = map[string]any{"error": "Unknown tool requested"}
	} else {
		runnable, ok := t.(interface {
			Run(context.Context, any) (map[string]any, error)
		})
		if !ok {
			toolResult = map[string]any{"error": "Tool does not support running"}
		} else {
			toolCtx := runtimectx.WithEventChan(ctx, eventChan)
			res, err := runnable.Run(toolCtx, args)
			if err != nil {
				toolResult = map[string]any{"error": err.Error()}
			} else {
				toolResult = res
			}
		}
	}

	eventChan <- ToolResultMsg{Name: tc.Function.Name, Result: toolResult}

	resBytes, _ := json.Marshal(toolResult)
	if len(resBytes) > 15000 {
		id := tc.ID
		cacheDir := r.Paths.CacheDir()
		os.MkdirAll(cacheDir, 0755)
		cachePath := filepath.Join(cacheDir, fmt.Sprintf("tool_output_%s.txt", id))
		os.WriteFile(cachePath, resBytes, 0644)
		limit := 5000
		summary := fmt.Sprintf("[TRUNCATED: Output exceeded 15k chars. Full output saved to %s] \n\n%s...", cachePath, string(resBytes[:limit]))
		toolResult = map[string]any{"result": summary}
		resBytes, _ = json.Marshal(toolResult)
	}

	return openai.ChatCompletionMessage{
		Role:       openai.ChatMessageRoleTool,
		Content:    string(resBytes),
		ToolCallID: tc.ID,
		Name:       tc.Function.Name,
	}
}

func (e *toolExecutor) errorToolMessage(tc openai.ToolCall, eventChan chan<- any, errMsg string) openai.ChatCompletionMessage {
	toolResult := map[string]any{"error": errMsg}
	eventChan <- ToolResultMsg{Name: tc.Function.Name, Result: toolResult}
	resBytes, _ := json.Marshal(toolResult)
	return openai.ChatCompletionMessage{
		Role:       openai.ChatMessageRoleTool,
		Content:    string(resBytes),
		ToolCallID: tc.ID,
		Name:       tc.Function.Name,
	}
}
