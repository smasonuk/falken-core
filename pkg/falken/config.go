package falken

import (
	"log"

	"github.com/smasonuk/falken-core/internal/runtimeapi"

	openai "github.com/sashabaranov/go-openai"
)

// StateMode controls whether .falken/state/current is reset or reused when
// constructing a session. The default is StateModeFresh.
type StateMode string

const (
	// StateModeFresh deletes .falken/state/current before the session loads.
	StateModeFresh StateMode = "fresh"
	// StateModeResume preserves .falken/state/current for the new session.
	StateModeResume StateMode = "resume"
)

// Config configures a reusable Falken runtime session.
type Config struct {
	Client             *openai.Client
	ModelName          string
	SystemPrompt       string
	WorkspaceDir       string
	StateDir           string
	StateMode          StateMode
	Logger             *log.Logger
	PermissionsConfig  *PermissionsConfig
	InteractionHandler InteractionHandler
	EventHandler       EventHandler
	ToolDir            string
	PluginDir          string
	SandboxImage       string
	Debug              bool
}

// Paths describes the resolved workspace and state directories for a session.
type Paths = runtimeapi.Paths

// NewPaths resolves workspace and state directories for a session.
// If stateDir is empty, it defaults to a `.falken` directory under workspaceDir.
func NewPaths(workspaceDir, stateDir string) (Paths, error) {
	return runtimeapi.NewPaths(workspaceDir, stateDir)
}
