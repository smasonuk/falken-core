package state

import (
	"fmt"
	"os"
)

// EnsureLayoutState creates the minimal canonical state-root structure needed for session use.
func EnsureLayoutState(layout Layout) error {
	for _, dir := range []string{
		layout.StateRoot,
		layout.CacheRoot,
		layout.BackupRoot,
		layout.TruncationRoot,
		layout.ArtifactRoot,
		layout.PluginStateRoot,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("ensure state directory %q: %w", dir, err)
		}
	}

	if err := EnsureConversationState(layout); err != nil {
		return err
	}

	return nil
}
