package store

// HistoryBackend persists encoded conversation history entries.
type HistoryBackend interface {
	Read() ([]string, error)
	Write([]string) error
	Append(string) error
}

// MemoryBackend persists structured conversation memory.
type MemoryBackend interface {
	Read() (MemoryState, error)
	Write(MemoryState) error
}

// TodoBackend persists structured conversation todos.
type TodoBackend interface {
	Read() (TodoState, error)
	Write(TodoState) error
}

// PlanBackend persists the current implementation plan.
type PlanBackend interface {
	Read() (string, error)
	Write(string) error
	Clear() error
}

// CommandEvidenceBackend persists command evidence and review state.
type CommandEvidenceBackend interface {
	Read() (CommandEvidenceState, error)
	Write(CommandEvidenceState) error
	Remove() error
}

// OptionalPathProvider exposes a backing path for diagnostics when one exists.
type OptionalPathProvider interface {
	Path() string
}
