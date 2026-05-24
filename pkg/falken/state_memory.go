package falken

import "sync"

// StateBackendProviderFunc adapts a function into a StateBackendProvider.
type StateBackendProviderFunc func(StateBackendRequest) (StateBackend, error)

// NewStateBackend creates a state backend for one session.
func (f StateBackendProviderFunc) NewStateBackend(req StateBackendRequest) (StateBackend, error) {
	return f(req)
}

type inMemoryStateBackend struct {
	mu     sync.RWMutex
	values map[string][]byte
}

// NewInMemoryStateBackendProvider creates a provider that returns a fresh
// in-memory state backend for each session.
func NewInMemoryStateBackendProvider() StateBackendProvider {
	return StateBackendProviderFunc(func(StateBackendRequest) (StateBackend, error) {
		return NewInMemoryStateBackend(), nil
	})
}

// NewInMemoryStateBackend creates a concurrency-safe in-memory state backend.
func NewInMemoryStateBackend() StateBackend {
	return &inMemoryStateBackend{values: make(map[string][]byte)}
}

// Read returns a defensive copy of the value stored for key.
func (b *inMemoryStateBackend) Read(key string) ([]byte, bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	value, found := b.values[key]
	if !found {
		return nil, false, nil
	}
	return append([]byte(nil), value...), true, nil
}

// Write stores a defensive copy of data for key.
func (b *inMemoryStateBackend) Write(key string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.values[key] = append([]byte(nil), data...)
	return nil
}

// Delete removes key from the backend.
func (b *inMemoryStateBackend) Delete(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.values, key)
	return nil
}
