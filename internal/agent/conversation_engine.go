package agent

import (
	"context"
	"fmt"
	"io"

	"github.com/sashabaranov/go-openai"
)

type conversationEngine struct{}

func (e *conversationEngine) streamCompletion(r *Runner, ctx context.Context, req openai.ChatCompletionRequest, eventChan chan<- any) (string, []openai.ToolCall, openai.FinishReason, error) {
	stream, err := r.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return "", nil, "", fmt.Errorf("CreateChatCompletionStream error: %v", err)
	}
	defer stream.Close()

	var parser streamedContentParser
	var toolCalls []openai.ToolCall
	var finishReason openai.FinishReason

	for {
		response, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, "", fmt.Errorf("stream error: %v", err)
		}

		if len(response.Choices) == 0 {
			continue
		}

		delta := response.Choices[0].Delta
		if delta.Content != "" {
			parser.consume(delta.Content, eventChan)
		}

		if len(delta.ToolCalls) > 0 {
			toolCalls = mergeToolCallChunks(toolCalls, delta.ToolCalls)
		}

		if response.Choices[0].FinishReason != "" {
			finishReason = response.Choices[0].FinishReason
		}
	}

	return parser.finish(eventChan), toolCalls, finishReason, nil
}
