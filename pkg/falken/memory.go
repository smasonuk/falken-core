package falken

import (
	"fmt"

	"github.com/smasonuk/falken-core/internal/agent"
)

// Memory is the public representation of current Falken agent memory.
type Memory struct {
	Entries        []string `json:"entries,omitempty"`
	CurrentGoal    string   `json:"current_goal,omitempty"`
	ImportantFiles []string `json:"important_files,omitempty"`
	Decisions      []string `json:"decisions,omitempty"`
	OpenQuestions  []string `json:"open_questions,omitempty"`
}

// ReadMemory returns the current conversation memory.
func (s *Session) ReadMemory() (Memory, error) {
	if s == nil {
		return Memory{}, ErrSessionClosed
	}

	s.mu.Lock()
	switch s.state {
	case lifecycleClosed:
		s.mu.Unlock()
		return Memory{}, ErrSessionClosed
	case lifecycleStarting:
		s.mu.Unlock()
		return Memory{}, ErrSessionStarting
	case lifecycleClosing:
		s.mu.Unlock()
		return Memory{}, ErrSessionClosing
	}
	memoryStore := s.stores.memory
	s.mu.Unlock()

	memory, err := agent.NewMemoryManager(memoryStore).Read()
	if err != nil {
		return Memory{}, fmt.Errorf("read memory: %w", err)
	}
	return Memory{
		Entries:        append([]string(nil), memory.Entries...),
		CurrentGoal:    memory.CurrentGoal,
		ImportantFiles: append([]string(nil), memory.ImportantFiles...),
		Decisions:      append([]string(nil), memory.Decisions...),
		OpenQuestions:  append([]string(nil), memory.OpenQuestions...),
	}, nil
}
