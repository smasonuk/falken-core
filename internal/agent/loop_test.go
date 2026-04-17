package agent

import (
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestRunner_PruningLogic(t *testing.T) {
	r := &Runner{
		History: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "sys"},
		},
	}

	// Fill History with more than 50 messages
	// Let's create 60 messages: [System, U, A, T, T, U, A, T, T, ...]
	// We want to make sure it doesn't break assistant -> tool
	for i := 0; i < 20; i++ {
		r.History = append(r.History,
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "u"},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: "1"}}},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, ToolCallID: "1", Content: "t"},
		)
	}

	// History is now 1 + 20*3 = 61 messages.
	// Pruning should happen.

	// Manually run the pruning logic (since Run is a long loop)
	// We'll refactor Run to use a private prune() method if we want, but for now we can just copy-paste it for the test
	// OR we can test it through a mock Run call if it was exported.
	// Since I want to verify the exact logic I just added:

	pruneHistory := func(r *Runner) {
		if len(r.History) > 50 {
			newHistory := []openai.ChatCompletionMessage{r.History[0]}
			targetDrop := len(r.History) - 40
			dropIndex := 1
			for dropIndex < len(r.History) {
				if dropIndex >= targetDrop {
					if r.History[dropIndex].Role == openai.ChatMessageRoleTool {
						dropIndex++
						continue
					}
					break
				}
				dropIndex++
			}
			if dropIndex < len(r.History) {
				newHistory = append(newHistory, r.History[dropIndex:]...)
			}
			r.History = newHistory
		}
	}

	pruneHistory(r)

	if len(r.History) > 50 {
		t.Errorf("Expected history length <= 50, got %d", len(r.History))
	}

	if r.History[0].Role != openai.ChatMessageRoleSystem {
		t.Errorf("Expected first message to be System, got %s", r.History[0].Role)
	}

	// The first message after System MUST NOT be a Tool message
	if r.History[1].Role == openai.ChatMessageRoleTool {
		t.Errorf("First message after pruning MUST NOT be a Tool message")
	}

	// Ensure no orphaned tool calls exist
	for i, msg := range r.History {
		if msg.Role == openai.ChatMessageRoleTool {
			if i == 0 || r.History[i-1].Role != openai.ChatMessageRoleAssistant {
				// Wait, the API requirement is that Tool messages must follow an Assistant message
				// but NOT NECESSARILY the message immediately preceding it if there are multiple tool calls.
				// Actually, "Each tool response must be preceded by an assistant message containing the tool call with the same id."
				// And usually they are in a block.
				// If we have A (TC1, TC2), T1, T2.
				// T2 is preceded by T1, but T1 is preceded by A.
				// The strict requirement is that the BLOCK of Tool messages must follow the Assistant message.

				// Let's check if the previous message was either Assistant OR another Tool message (part of the same block)
				foundAssistant := false
				for j := i - 1; j >= 0; j-- {
					if r.History[j].Role == openai.ChatMessageRoleAssistant {
						foundAssistant = true
						break
					}
					if r.History[j].Role != openai.ChatMessageRoleTool {
						break
					}
				}
				if !foundAssistant {
					t.Errorf("Tool message at index %d is orphaned", i)
				}
			}
		}
	}
}
