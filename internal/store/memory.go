package store

import (
	"fmt"

	"github.com/smasonuk/falken-core/internal/persist"
	"github.com/smasonuk/falken-core/internal/state"
)

// MemoryState persists conversation memory as a structured JSON document.
type MemoryState struct {
	Entries        []string `json:"entries,omitempty"`
	CurrentGoal    string   `json:"current_goal,omitempty"`
	ImportantFiles []string `json:"important_files,omitempty"`
	Decisions      []string `json:"decisions,omitempty"`
	OpenQuestions  []string `json:"open_questions,omitempty"`
}

// MemoryStore persists conversation memory state.
type MemoryStore struct {
	path string
}

// NewMemoryStore builds a memory store from the canonical state layout.
func NewMemoryStore(layout state.Layout) MemoryStore {
	return MemoryStore{path: layout.MemoryPath}
}

// Path returns the canonical backing file path for the memory store.
func (s MemoryStore) Path() string {
	return s.path
}

// Read loads memory state. A missing file returns an empty memory state.
func (s MemoryStore) Read() (MemoryState, error) {
	memory := emptyMemoryState()
	found, err := persist.ReadJSON(s.path, &memory)
	if err != nil {
		return MemoryState{}, fmt.Errorf("read memory store: %w", err)
	}
	if !found {
		return emptyMemoryState(), nil
	}
	memory = normalizeMemorySlices(memory)

	return memory, nil
}

// Write persists the complete memory state atomically.
func (s MemoryStore) Write(memory MemoryState) error {
	memory = normalizeMemorySlices(memory)

	if err := persist.WriteJSONAtomic(s.path, memory, 0o600); err != nil {
		return fmt.Errorf("write memory store: %w", err)
	}

	return nil
}

func emptyMemoryState() MemoryState {
	return MemoryState{
		Entries:        []string{},
		ImportantFiles: []string{},
		Decisions:      []string{},
		OpenQuestions:  []string{},
	}
}

func normalizeMemorySlices(memory MemoryState) MemoryState {
	if memory.Entries == nil {
		memory.Entries = []string{}
	}
	if memory.ImportantFiles == nil {
		memory.ImportantFiles = []string{}
	}
	if memory.Decisions == nil {
		memory.Decisions = []string{}
	}
	if memory.OpenQuestions == nil {
		memory.OpenQuestions = []string{}
	}
	return memory
}
