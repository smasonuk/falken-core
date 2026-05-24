package state

import (
	"fmt"
	"time"

	"github.com/smasonuk/falken-core/internal/persist"
)

// ProjectPermissionsDefaultsVersion tracks the current one-time default policy bootstrap version.
const ProjectPermissionsDefaultsVersion = 1

// Metadata is the canonical persisted metadata model for a Falken state root.
type Metadata struct {
	WorkspaceRoot                 string    `json:"workspace_root"`
	LayoutVersion                 int       `json:"layout_version"`
	CreatedAt                     time.Time `json:"created_at"`
	LastUsedAt                    time.Time `json:"last_used_at"`
	ProjectPermissionsInitialized bool      `json:"project_permissions_initialized,omitempty"`
	ProjectPermissionsVersion     int       `json:"project_permissions_version,omitempty"`
}

// ReadMetadata reads canonical state metadata from the layout's metadata path.
// The returned boolean reports whether metadata was present.
func ReadMetadata(layout Layout) (Metadata, bool, error) {
	var metadata Metadata
	found, err := persist.ReadJSON(layout.MetadataPath, &metadata)
	if err != nil {
		return Metadata{}, false, fmt.Errorf("read state metadata: %w", err)
	}

	return metadata, found, nil
}

// WriteMetadata atomically persists canonical state metadata to the layout's metadata path.
func WriteMetadata(layout Layout, metadata Metadata) error {
	if err := persist.WriteJSONAtomic(layout.MetadataPath, metadata, 0o644); err != nil {
		return fmt.Errorf("write state metadata: %w", err)
	}

	return nil
}

// TouchMetadata creates metadata if missing and updates last-used state if present.
func TouchMetadata(layout Layout) (Metadata, error) {
	metadata, exists, err := ReadMetadata(layout)
	if err != nil {
		return Metadata{}, err
	}

	now := time.Now().UTC()
	if !exists {
		metadata.CreatedAt = now
	}

	metadata.WorkspaceRoot = layout.WorkspaceRoot
	metadata.LayoutVersion = layout.LayoutVersion
	metadata.LastUsedAt = now

	if err := WriteMetadata(layout, metadata); err != nil {
		return Metadata{}, err
	}

	return metadata, nil
}
