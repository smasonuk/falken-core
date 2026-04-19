package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type AgentMemory struct {
	CurrentGoal    string   `json:"current_goal,omitempty"`
	ImportantFiles []string `json:"important_files,omitempty"`
	Decisions      []string `json:"decisions,omitempty"`
	OpenQuestions  []string `json:"open_questions,omitempty"`
	RecentSummary  string   `json:"recent_summary,omitempty"`
	LastUpdated    int64    `json:"last_updated,omitempty"`
}

type MemoryStore struct {
	path string
	mu   sync.Mutex
}

func NewMemoryStore(path string) *MemoryStore {
	os.MkdirAll(filepath.Dir(path), 0755)
	return &MemoryStore{path: path}
}

func (s *MemoryStore) Read() (*AgentMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &AgentMemory{}, nil
		}
		return nil, err
	}

	var mem AgentMemory
	if err := json.Unmarshal(data, &mem); err != nil {
		return nil, err
	}

	return &mem, nil
}

func (s *MemoryStore) Write(mem *AgentMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mem.LastUpdated = time.Now().Unix()

	data, err := json.MarshalIndent(mem, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0644)
}

func mergeUniqueStrings(existing, toAdd []string) []string {
	seen := make(map[string]bool)
	var merged []string

	for _, s := range existing {
		if !seen[s] {
			seen[s] = true
			merged = append(merged, s)
		}
	}

	for _, s := range toAdd {
		if !seen[s] {
			seen[s] = true
			merged = append(merged, s)
		}
	}

	// Truncate to reasonable limits
	if len(merged) > 50 {
		merged = merged[len(merged)-50:]
	}

	return merged
}

func removeStrings(existing, toRemove []string) []string {
	removeMap := make(map[string]bool)
	for _, s := range toRemove {
		removeMap[s] = true
	}

	var final []string
	for _, s := range existing {
		if !removeMap[s] {
			final = append(final, s)
		}
	}
	return final
}
