package store

// BlobBackend is a generic byte-oriented storage backend used by typed stores.
type BlobBackend interface {
	Read(key string) ([]byte, bool, error)
	Write(key string, data []byte) error
	Delete(key string) error
}

const (
	BlobKeyHistory         = "conversations/current/history.json"
	BlobKeyMemory          = "conversations/current/memory.json"
	BlobKeyTodos           = "conversations/current/todos.json"
	BlobKeyPlan            = "conversations/current/plan.txt"
	BlobKeyCommandEvidence = "conversations/current/command_evidence.json"
)
