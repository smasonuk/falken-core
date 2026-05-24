package store

import (
	"fmt"

	"github.com/smasonuk/falken-core/internal/persist"
	"github.com/smasonuk/falken-core/internal/state"
)

// HistoryStore persists conversation history as a JSON array of strings.
type HistoryStore struct {
	path string
}

// NewHistoryStore builds a history store from the canonical state layout.
func NewHistoryStore(layout state.Layout) HistoryStore {
	return HistoryStore{path: layout.HistoryPath}
}

// Path returns the canonical backing file path for the history store.
func (s HistoryStore) Path() string {
	return s.path
}

// Read loads history entries. A missing file returns an empty history.
func (s HistoryStore) Read() ([]string, error) {
	var entries []string
	found, err := persist.ReadJSON(s.path, &entries)
	if err != nil {
		return nil, fmt.Errorf("read history store: %w", err)
	}
	if !found || entries == nil {
		return []string{}, nil
	}

	return entries, nil
}

// Write persists the complete history atomically.
func (s HistoryStore) Write(entries []string) error {
	if entries == nil {
		entries = []string{}
	}

	if err := persist.WriteJSONAtomic(s.path, entries, 0o600); err != nil {
		return fmt.Errorf("write history store: %w", err)
	}

	return nil
}

// Append appends a single history entry and persists the updated history atomically.
func (s HistoryStore) Append(entry string) error {
	entries, err := s.Read()
	if err != nil {
		return err
	}

	entries = append(entries, entry)
	return s.Write(entries)
}
