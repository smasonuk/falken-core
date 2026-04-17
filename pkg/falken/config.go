package falken

import (
	"log"

	"github.com/smasonuk/falken-core/internal/runtimeapi"

	openai "github.com/sashabaranov/go-openai"
)

// Config configures a reusable Falken runtime session.
type Config struct {
	Client             *openai.Client
	ModelName          string
	SystemPrompt       string
	WorkspaceDir       string
	StateDir           string
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
