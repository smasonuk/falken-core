package api

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
)

// Tool is implemented by every built-in tool. All metadata and execution logic
// live in the same type, in the same file.
type Tool interface {
	Descriptor() Descriptor
	Execute(ctx context.Context, host *Host, args json.RawMessage) (agent.ToolExecutionResult, error)
}

// Descriptor holds every static field needed for tool registration and schema
// generation.
type Descriptor struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Category    string
	Keywords    []string
	AlwaysLoad  bool
	DefaultLoad bool
	Safety      tools.Safety
}

// Safety is kept as a source-compatible alias for built-in descriptors; the
// canonical internal safety metadata type lives in internal/extensions/tools.
type Safety = tools.Safety
