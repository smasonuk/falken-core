package agent

import (
	"context"
	"fmt"
	"strings"
)

func (r *Runner) complete(ctx context.Context, request CompletionRequest, events EventSink) (CompletionResponse, bool, string, error) {
	streaming, ok := r.llm.(StreamingLLM)
	if !ok {
		response, err := r.llm.Complete(ctx, request)
		return response, false, "", err
	}

	streamed := false
	var streamedText strings.Builder
	response, err := streaming.StreamComplete(ctx, request, func(text string) {
		if text == "" {
			return
		}
		streamed = true
		streamedText.WriteString(text)
		emit(events, AssistantTextEvent(text))
	})
	if err != nil {
		return CompletionResponse{}, streamed, streamedText.String(), err
	}
	if !streamed && response.AssistantText != "" {
		return response, false, "", nil
	}
	return response, streamed, streamedText.String(), nil
}

func (r *Runner) finishSuccess(ctx context.Context, events EventSink, callback CompletionCallback, finalOutput string) (RunResult, error) {
	result := RunResult{FinalOutput: finalOutput, Completed: true}
	if callback == nil {
		emit(events, RunCompletedEvent(result))
		return result, nil
	}
	if err := callback(ctx, result); err != nil {
		return r.finishFailure(events, finalOutput, fmt.Errorf("completion callback: %w", err))
	}

	emit(events, RunCompletedEvent(result))
	return result, nil
}

func (r *Runner) finishFailure(events EventSink, finalOutput string, err error) (RunResult, error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	result := RunResult{FinalOutput: finalOutput, Completed: false, Error: message}
	emit(events, RunFailedEvent(err))
	return result, err
}
