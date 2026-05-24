package memorytools

import "github.com/smasonuk/falken-core/internal/store"

type memoryPayload struct {
	Entries        []string `json:"entries"`
	CurrentGoal    string   `json:"current_goal"`
	ImportantFiles []string `json:"important_files"`
	Decisions      []string `json:"decisions"`
	OpenQuestions  []string `json:"open_questions"`
}

func payloadMemory(memory store.MemoryState) memoryPayload {
	return memoryPayload{
		Entries:        append([]string(nil), memory.Entries...),
		CurrentGoal:    memory.CurrentGoal,
		ImportantFiles: append([]string(nil), memory.ImportantFiles...),
		Decisions:      append([]string(nil), memory.Decisions...),
		OpenQuestions:  append([]string(nil), memory.OpenQuestions...),
	}
}
