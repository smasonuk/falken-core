package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sashabaranov/go-openai"
)

type historyManager struct{}

func (h *historyManager) appendToLog(r *Runner, msg openai.ChatCompletionMessage) error {
	f, err := os.OpenFile(r.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = f.Write(append(line, '\n'))
	return err
}

func (h *historyManager) prepareHistory(r *Runner, prompt string) {
	if len(r.History) == 0 {
		sysMsg := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: r.systemPrompt}
		r.History = append(r.History, sysMsg)
		r.appendToLog(sysMsg)

		fullPrompt := prompt
		tasks, err := r.taskStore.Load()
		if err == nil && len(tasks) > 0 {
			r.logger.Println("Found existing tasks, merging into initial prompt")
			var sb strings.Builder
			sb.WriteString("Existing tasks in your queue:\n")
			for _, t := range tasks {
				sb.WriteString(fmt.Sprintf("- [%s] #%s: %s\n", t.Status, t.ID, t.Subject))
			}
			fullPrompt = fmt.Sprintf("%s\n\nTask: %s", sb.String(), prompt)
		}

		userMsg := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: fullPrompt}
		r.History = append(r.History, userMsg)
		r.appendToLog(userMsg)
		return
	}

	userMsg := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: prompt}
	r.History = append(r.History, userMsg)
	r.appendToLog(userMsg)
}

func (h *historyManager) summarizeDroppedHistory(r *Runner, dropped []openai.ChatCompletionMessage) {
	var filesRead []string
	var filesModified []string
	var commandsRun []string

	for _, msg := range dropped {
		if msg.Role != openai.ChatMessageRoleAssistant {
			continue
		}
		for _, tc := range msg.ToolCalls {
			args := parseArgs(tc.Function.Arguments)
			switch tc.Function.Name {
			case "read_file":
				if path, ok := args["Path"].(string); ok {
					filesRead = append(filesRead, path)
				}
			case "write_file", "edit_file", "apply_patch":
				if path, ok := args["Path"].(string); ok {
					filesModified = append(filesModified, path)
				}
			case "execute_command":
				if cmd, ok := args["Command"].(string); ok {
					if len(cmd) > 30 {
						cmd = cmd[:27] + "..."
					}
					commandsRun = append(commandsRun, cmd)
				}
			}
		}
	}

	if len(filesRead) == 0 && len(filesModified) == 0 && len(commandsRun) == 0 {
		return
	}

	mem, err := r.memoryStore.Read()
	if err != nil {
		return
	}

	summary := "Previous actions summary:\n"
	if len(filesRead) > 0 {
		summary += fmt.Sprintf("- Read %d files\n", len(filesRead))
	}
	if len(filesModified) > 0 {
		summary += fmt.Sprintf("- Modified %d files (including %v)\n", len(filesModified), filesModified)
	}
	if len(commandsRun) > 0 {
		summary += fmt.Sprintf("- Ran %d commands (including %v)\n", len(commandsRun), commandsRun)
	}

	mem.RecentSummary = summary
	r.memoryStore.Write(mem)
}

func (h *historyManager) compactHistory(r *Runner) {
	if len(r.History) > 30 {
		recentThreshold := len(r.History) - 15
		for i := 1; i < recentThreshold; i++ {
			msg := r.History[i]
			if msg.Role == openai.ChatMessageRoleTool {
				switch msg.Name {
				case "read_file", "read_files", "grep", "glob", "fetch_url":
					r.History[i].Content = `{"result": "[Tool output truncated due to age. Rely on your memory or re-read if necessary.]"}`
				}
			}
		}
	}

	if len(r.History) > 80 {
		newHistory := []openai.ChatCompletionMessage{r.History[0]}
		targetDrop := len(r.History) - 50
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

		h.summarizeDroppedHistory(r, r.History[1:dropIndex])
		newHistory = append(newHistory, r.History[dropIndex:]...)
		r.History = newHistory
	}
}

func (h *historyManager) refreshMemoryPrompt(r *Runner) {
	mem, err := r.memoryStore.Read()
	if err != nil {
		return
	}
	memJSON, _ := json.MarshalIndent(mem, "", "  ")
	currentSysPrompt := r.systemPrompt + "\n\n--- CURRENT AGENT MEMORY ---\n" + string(memJSON)
	if len(r.History) > 0 && r.History[0].Role == openai.ChatMessageRoleSystem {
		r.History[0].Content = currentSysPrompt
	}
}
