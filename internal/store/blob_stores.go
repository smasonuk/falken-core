package store

import (
	"encoding/json"
	"fmt"
)

// BlobHistoryStore persists history entries through a generic byte backend.
type BlobHistoryStore struct {
	backend BlobBackend
	key     string
}

// NewBlobHistoryStore creates a history store backed by a generic byte backend.
func NewBlobHistoryStore(backend BlobBackend) BlobHistoryStore {
	return BlobHistoryStore{backend: backend, key: BlobKeyHistory}
}

// Read loads encoded history entries. A missing key returns an empty history.
func (s BlobHistoryStore) Read() ([]string, error) {
	var entries []string
	found, err := s.readJSON(&entries)
	if err != nil {
		return nil, fmt.Errorf("read history store: %w", err)
	}
	if !found || entries == nil {
		return []string{}, nil
	}
	return entries, nil
}

// Write persists encoded history entries.
func (s BlobHistoryStore) Write(entries []string) error {
	if entries == nil {
		entries = []string{}
	}
	if err := s.writeJSON(entries); err != nil {
		return fmt.Errorf("write history store: %w", err)
	}
	return nil
}

// Append appends and persists one encoded history entry.
func (s BlobHistoryStore) Append(entry string) error {
	entries, err := s.Read()
	if err != nil {
		return err
	}
	entries = append(entries, entry)
	return s.Write(entries)
}

// BlobMemoryStore persists memory through a generic byte backend.
type BlobMemoryStore struct {
	backend BlobBackend
	key     string
}

// NewBlobMemoryStore creates a memory store backed by a generic byte backend.
func NewBlobMemoryStore(backend BlobBackend) BlobMemoryStore {
	return BlobMemoryStore{backend: backend, key: BlobKeyMemory}
}

// Read loads memory state. A missing key returns empty memory.
func (s BlobMemoryStore) Read() (MemoryState, error) {
	memory := emptyMemoryState()
	found, err := s.readJSON(&memory)
	if err != nil {
		return MemoryState{}, fmt.Errorf("read memory store: %w", err)
	}
	if !found {
		return emptyMemoryState(), nil
	}
	return normalizeMemorySlices(memory), nil
}

// Write persists memory state.
func (s BlobMemoryStore) Write(memory MemoryState) error {
	if err := s.writeJSON(normalizeMemorySlices(memory)); err != nil {
		return fmt.Errorf("write memory store: %w", err)
	}
	return nil
}

// BlobTodoStore persists todos through a generic byte backend.
type BlobTodoStore struct {
	backend BlobBackend
	key     string
}

// NewBlobTodoStore creates a todo store backed by a generic byte backend.
func NewBlobTodoStore(backend BlobBackend) BlobTodoStore {
	return BlobTodoStore{backend: backend, key: BlobKeyTodos}
}

// Read loads todo state. A missing key returns empty todos.
func (s BlobTodoStore) Read() (TodoState, error) {
	todos := TodoState{Items: []TodoItem{}}
	found, err := s.readJSON(&todos)
	if err != nil {
		return TodoState{}, fmt.Errorf("read todo store: %w", err)
	}
	if !found || todos.Items == nil {
		return TodoState{Items: []TodoItem{}}, nil
	}
	return todos, nil
}

// Write persists todo state.
func (s BlobTodoStore) Write(todos TodoState) error {
	if todos.Items == nil {
		todos.Items = []TodoItem{}
	}
	if err := s.writeJSON(todos); err != nil {
		return fmt.Errorf("write todo store: %w", err)
	}
	return nil
}

// BlobPlanStore persists plan text through a generic byte backend.
type BlobPlanStore struct {
	backend BlobBackend
	key     string
}

// NewBlobPlanStore creates a plan store backed by a generic byte backend.
func NewBlobPlanStore(backend BlobBackend) BlobPlanStore {
	return BlobPlanStore{backend: backend, key: BlobKeyPlan}
}

// Read loads plan text. A missing key returns an empty plan.
func (s BlobPlanStore) Read() (string, error) {
	data, found, err := s.backend.Read(s.key)
	if err != nil {
		return "", fmt.Errorf("read plan store: %w", err)
	}
	if !found {
		return "", nil
	}
	return string(data), nil
}

// Write persists plan text.
func (s BlobPlanStore) Write(plan string) error {
	if err := s.backend.Write(s.key, []byte(plan)); err != nil {
		return fmt.Errorf("write plan store: %w", err)
	}
	return nil
}

// Clear removes the active plan text.
func (s BlobPlanStore) Clear() error {
	if err := s.backend.Delete(s.key); err != nil {
		return fmt.Errorf("clear plan store: %w", err)
	}
	return nil
}

// BlobCommandEvidenceStore persists command evidence through a generic byte backend.
type BlobCommandEvidenceStore struct {
	backend BlobBackend
	key     string
}

// NewBlobCommandEvidenceStore creates a command evidence store backed by a generic byte backend.
func NewBlobCommandEvidenceStore(backend BlobBackend) BlobCommandEvidenceStore {
	return BlobCommandEvidenceStore{backend: backend, key: BlobKeyCommandEvidence}
}

// Read loads command evidence state. A missing key returns empty command evidence.
func (s BlobCommandEvidenceStore) Read() (CommandEvidenceState, error) {
	evidence := emptyCommandEvidenceState()
	found, err := s.readJSON(&evidence)
	if err != nil {
		return CommandEvidenceState{}, fmt.Errorf("read command evidence store: %w", err)
	}
	if !found {
		return emptyCommandEvidenceState(), nil
	}
	return normalizeCommandEvidenceState(evidence), nil
}

// Write persists command evidence state.
func (s BlobCommandEvidenceStore) Write(evidence CommandEvidenceState) error {
	if err := s.writeJSON(normalizeCommandEvidenceState(evidence)); err != nil {
		return fmt.Errorf("write command evidence store: %w", err)
	}
	return nil
}

// Remove deletes command evidence state if present.
func (s BlobCommandEvidenceStore) Remove() error {
	if err := s.backend.Delete(s.key); err != nil {
		return fmt.Errorf("remove command evidence store: %w", err)
	}
	return nil
}

func (s BlobHistoryStore) readJSON(target any) (bool, error) {
	return readBlobJSON(s.backend, s.key, target)
}

func (s BlobHistoryStore) writeJSON(value any) error {
	return writeBlobJSON(s.backend, s.key, value)
}

func (s BlobMemoryStore) readJSON(target any) (bool, error) {
	return readBlobJSON(s.backend, s.key, target)
}

func (s BlobMemoryStore) writeJSON(value any) error {
	return writeBlobJSON(s.backend, s.key, value)
}

func (s BlobTodoStore) readJSON(target any) (bool, error) {
	return readBlobJSON(s.backend, s.key, target)
}

func (s BlobTodoStore) writeJSON(value any) error {
	return writeBlobJSON(s.backend, s.key, value)
}

func (s BlobCommandEvidenceStore) readJSON(target any) (bool, error) {
	return readBlobJSON(s.backend, s.key, target)
}

func (s BlobCommandEvidenceStore) writeJSON(value any) error {
	return writeBlobJSON(s.backend, s.key, value)
}

func readBlobJSON(backend BlobBackend, key string, target any) (bool, error) {
	data, found, err := backend.Read(key)
	if err != nil || !found {
		return found, err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return true, err
	}
	return true, nil
}

func writeBlobJSON(backend BlobBackend, key string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return backend.Write(key, data)
}
