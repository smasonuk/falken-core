package state

import (
	"fmt"
	"os"
)

// ConversationStatePaths groups the v1 conversation-scoped paths that reset is allowed to clear.
type ConversationStatePaths struct {
	Root                string
	HistoryPath         string
	MemoryPath          string
	TodosPath           string
	PlanPath            string
	CommandEvidencePath string
	// Deprecated: legacy name retained only for migration.
	VerificationPath     string
	RecentTruncationRoot string
	RecentArtifactRoot   string
}

// ConversationPaths returns the canonical v1 conversation-scoped state boundary for a layout.
func ConversationPaths(layout Layout) ConversationStatePaths {
	return ConversationStatePaths{
		Root:                 layout.CurrentConversationRoot,
		HistoryPath:          layout.HistoryPath,
		MemoryPath:           layout.MemoryPath,
		TodosPath:            layout.TodosPath,
		PlanPath:             layout.PlanPath,
		CommandEvidencePath:  layout.CommandEvidencePath,
		VerificationPath:     layout.VerificationPath,
		RecentTruncationRoot: layout.RecentTruncationRoot,
		RecentArtifactRoot:   layout.RecentArtifactRoot,
	}
}

// EnsureConversationState creates the minimum conversation-scoped directory structure needed for runtime use.
func EnsureConversationState(layout Layout) error {
	paths := ConversationPaths(layout)
	for _, dir := range []string{
		paths.Root,
		paths.RecentTruncationRoot,
		paths.RecentArtifactRoot,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("ensure conversation state directory %q: %w", dir, err)
		}
	}

	return nil
}

// ResetConversationState clears only the canonical conversation-scoped state for the layout.
func ResetConversationState(layout Layout) error {
	paths := ConversationPaths(layout)

	for _, file := range []string{
		paths.HistoryPath,
		paths.MemoryPath,
		paths.TodosPath,
		paths.PlanPath,
		paths.CommandEvidencePath,
		paths.VerificationPath,
	} {
		if err := removeFileIfExists(file); err != nil {
			return err
		}
	}

	for _, dir := range []string{
		paths.RecentTruncationRoot,
		paths.RecentArtifactRoot,
	} {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove conversation state directory %q: %w", dir, err)
		}
	}

	if err := EnsureConversationState(layout); err != nil {
		return err
	}

	return nil
}

func removeFileIfExists(path string) error {
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}

	return fmt.Errorf("remove conversation state file %q: %w", path, err)
}

// DurableStatePaths returns the canonical v1 durable state paths that reset must preserve.
func DurableStatePaths(layout Layout) []string {
	return []string{
		layout.MetadataPath,
		layout.BackupRoot,
	}
}

// ConversationScopedPaths returns all canonical v1 conversation-scoped paths for reset callers/tests.
func ConversationScopedPaths(layout Layout) []string {
	paths := ConversationPaths(layout)
	return []string{
		paths.HistoryPath,
		paths.MemoryPath,
		paths.TodosPath,
		paths.PlanPath,
		paths.CommandEvidencePath,
		paths.VerificationPath,
		paths.RecentTruncationRoot,
		paths.RecentArtifactRoot,
	}
}
