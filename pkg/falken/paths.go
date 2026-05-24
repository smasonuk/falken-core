package falken

import (
	"github.com/smasonuk/falken-core/internal/state"
	"github.com/smasonuk/falken-core/internal/workspace"
)

// LayoutVersion is the canonical Falken state layout version exposed by the v1 public API.
const LayoutVersion = state.LayoutVersion

// Paths exposes the canonical workspace and state paths for a Falken runtime.
type Paths struct {
	// WorkspaceRoot is the canonical absolute workspace root.
	WorkspaceRoot string
	// StateRoot is the canonical absolute state root.
	StateRoot string
	// MetadataPath stores session metadata.
	MetadataPath string
	// CurrentConversationRoot contains conversation-scoped state.
	CurrentConversationRoot string
	// HistoryPath stores agent conversation history.
	HistoryPath string
	// MemoryPath stores conversation memory.
	MemoryPath string
	// TodosPath stores conversation todos.
	TodosPath string
	// PlanPath stores Plan-mode content.
	PlanPath string
	// CommandEvidencePath stores runtime command evidence.
	CommandEvidencePath string
	// VerificationPath stores legacy runtime verification evidence.
	//
	// Deprecated: retained only for migration compatibility.
	VerificationPath string
	// CacheRoot stores cache data.
	CacheRoot string
	// BackupRoot stores managed mutation backups.
	BackupRoot string
	// TruncationRoot stores retained truncated output.
	TruncationRoot string
	// RecentTruncationRoot stores recent truncated output.
	RecentTruncationRoot string
	// ArtifactRoot stores durable artifacts.
	ArtifactRoot string
	// RecentArtifactRoot stores recent artifacts.
	RecentArtifactRoot string
	// PluginStateRoot stores hashed extension state.
	PluginStateRoot string
	// ProjectPermissionsPath stores project-scoped file, shell, and network permissions.
	ProjectPermissionsPath string
	// LayoutVersion is the canonical state layout version.
	LayoutVersion int
}

// NewPaths resolves the canonical absolute path layout for a workspace and optional explicit state root.
func NewPaths(workspaceDir, stateDir string) (Paths, error) {
	workspaceRoot, err := workspace.NormalizeRoot(workspaceDir)
	if err != nil {
		return Paths{}, err
	}

	layout, err := state.ResolveLayout(workspaceRoot, stateDir)
	if err != nil {
		return Paths{}, err
	}

	return newPaths(layout), nil
}

// newPaths converts an internal state layout into the public Paths struct.
func newPaths(layout state.Layout) Paths {
	return Paths{
		WorkspaceRoot:           layout.WorkspaceRoot,
		StateRoot:               layout.StateRoot,
		MetadataPath:            layout.MetadataPath,
		CurrentConversationRoot: layout.CurrentConversationRoot,
		HistoryPath:             layout.HistoryPath,
		MemoryPath:              layout.MemoryPath,
		TodosPath:               layout.TodosPath,
		PlanPath:                layout.PlanPath,
		CommandEvidencePath:     layout.CommandEvidencePath,
		VerificationPath:        layout.VerificationPath,
		CacheRoot:               layout.CacheRoot,
		BackupRoot:              layout.BackupRoot,
		TruncationRoot:          layout.TruncationRoot,
		RecentTruncationRoot:    layout.RecentTruncationRoot,
		ArtifactRoot:            layout.ArtifactRoot,
		RecentArtifactRoot:      layout.RecentArtifactRoot,
		PluginStateRoot:         layout.PluginStateRoot,
		ProjectPermissionsPath:  projectApprovalsPath(layout),
		LayoutVersion:           layout.LayoutVersion,
	}
}
